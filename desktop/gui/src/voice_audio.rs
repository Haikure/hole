//! Desktop device adapter only: shared Go core owns Opus, mixing and transport.
//! Device callbacks never perform IPC or wait for a mutex. Both PCM queues hold
//! at most four 20 ms frames; each new stream rejects old playback packets.
use crate::core_client::CoreClient;
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{FromSample, Sample, SampleFormat, SizedSample, Stream, StreamConfig};
use std::collections::VecDeque;
use std::sync::{atomic::{AtomicBool, Ordering}, mpsc, Arc, Mutex};
use std::time::Duration;

pub const FRAME_SAMPLES: usize = 960;
pub const PACKET_BYTES: usize = 17 + FRAME_SAMPLES * 2;
const QUEUE_SAMPLES: usize = FRAME_SAMPLES * 4;

#[derive(Default)]
pub struct Playback {
    pub stream_id: u64,
    samples: VecDeque<i16>,
}

impl Playback {
    pub fn reset(&mut self, id: u64) {
        self.stream_id = id;
        self.samples.clear();
        self.samples.reserve(QUEUE_SAMPLES);
    }

    pub fn receive(&mut self, packet: &[u8; PACKET_BYTES]) {
        let id = u64::from_le_bytes(packet[1..9].try_into().unwrap());
        if self.stream_id == 0 || id != self.stream_id { return; }
        while self.samples.len() > QUEUE_SAMPLES - FRAME_SAMPLES {
            self.samples.pop_front();
        }
        self.samples.extend(packet[17..].chunks_exact(2).map(|p| i16::from_le_bytes([p[0], p[1]])));
    }
}

pub fn packet(id: u64, sequence: u64, samples: &[i16; FRAME_SAMPLES]) -> [u8; PACKET_BYTES] {
    let mut bytes = [0; PACKET_BYTES];
    bytes[1..9].copy_from_slice(&id.to_le_bytes());
    bytes[9..17].copy_from_slice(&sequence.to_le_bytes());
    for (out, sample) in bytes[17..].chunks_exact_mut(2).zip(samples) {
        out.copy_from_slice(&sample.to_le_bytes());
    }
    bytes
}

// Streaming linear interpolation; maintains timing across callback boundaries.
struct Resampler { previous: f32, position: f64, step: f64, primed: bool }
impl Resampler {
    fn new(input_rate: u32) -> Self {
        Self { previous: 0., position: 0., step: input_rate as f64 / 48000., primed: false }
    }
    fn push(&mut self, sample: f32, mut emit: impl FnMut(f32)) {
        if !self.primed { self.previous = sample; self.primed = true; return; }
        while self.position < 1. {
            emit(self.previous + (sample - self.previous) * self.position as f32);
            self.position += self.step;
        }
        self.position -= 1.;
        self.previous = sample;
    }
}

pub struct VoiceAudio {
    // Explicitly stop callbacks before allowing the writer to finish.
    input: Option<Stream>,
    output: Option<Stream>,
    active: Arc<AtomicBool>,
    client: Arc<CoreClient>,
    error: Arc<Mutex<Option<String>>>,
    pub route: String,
}

fn device_config(device: &cpal::Device, input: bool) -> Result<cpal::SupportedStreamConfig, String> {
    let configs: Vec<_> = if input {
        device.supported_input_configs().map_err(|e| e.to_string())?.collect()
    } else {
        device.supported_output_configs().map_err(|e| e.to_string())?.collect()
    };
    if let Some(config) = configs.iter().filter(|c| c.min_sample_rate().0 <= 48000 && c.max_sample_rate().0 >= 48000)
        .min_by_key(|c| (format_priority(c.sample_format()), c.channels())) {
        return Ok(config.clone().with_sample_rate(cpal::SampleRate(48000)));
    }
    if input { device.default_input_config() } else { device.default_output_config() }.map_err(|e| e.to_string())
}

fn format_priority(format: SampleFormat) -> u8 {
    match format {
        SampleFormat::F32 => 0,
        SampleFormat::I16 => 1,
        SampleFormat::F64 | SampleFormat::I32 | SampleFormat::U32 | SampleFormat::I64 | SampleFormat::U64 => 2,
        SampleFormat::U16 => 3,
        _ => 4,
    }
}

impl VoiceAudio {
    pub fn open(client: Arc<CoreClient>, stream_id: u64) -> Result<Self, String> {
        let host = cpal::default_host();
        let input = host.default_input_device().ok_or("未找到麦克风，请连接设备后重试")?;
        let output = host.default_output_device().ok_or("未找到扬声器或耳机，请连接设备后重试")?;
        let input_config = device_config(&input, true).map_err(|e| format!("无法读取麦克风格式：{e}"))?;
        let output_config = device_config(&output, false).map_err(|e| format!("无法读取播放设备格式：{e}"))?;
        let error = Arc::new(Mutex::new(None));
        let active = Arc::new(AtomicBool::new(true));
        let (tx, rx) = mpsc::sync_channel(4);
        client.playback().lock().unwrap().reset(stream_id);
        let mut audio = Self {
            input: None, output: None, active, client: client.clone(), error: error.clone(),
            route: format!("{} → {}", input.name().unwrap_or_else(|_| "默认麦克风".into()), output.name().unwrap_or_else(|_| "默认播放设备".into())),
        };
        macro_rules! input_stream {
            ($t:ty) => { capture::<$t>(&input, &input_config.config(), stream_id, tx, error.clone()) };
        }
        macro_rules! output_stream {
            ($t:ty) => { playback::<$t>(&output, &output_config.config(), client.playback(), error.clone()) };
        }
        macro_rules! formats {
            ($format:expr, $build:ident) => { match $format {
                SampleFormat::F32 => $build!(f32), SampleFormat::F64 => $build!(f64),
                SampleFormat::I8 => $build!(i8), SampleFormat::U8 => $build!(u8),
                SampleFormat::I16 => $build!(i16), SampleFormat::U16 => $build!(u16),
                SampleFormat::I32 => $build!(i32), SampleFormat::U32 => $build!(u32),
                SampleFormat::I64 => $build!(i64), SampleFormat::U64 => $build!(u64),
                _ => Err("音频设备格式不受支持".into()),
            } };
        }
        audio.output = Some(formats!(output_config.sample_format(), output_stream)?);
        audio.input = Some(formats!(input_config.sample_format(), input_stream)?);
        let running = audio.active.clone();
        std::thread::Builder::new().name("hole-voice-ipc".into()).spawn(move || {
            while running.load(Ordering::Acquire) {
                match rx.recv_timeout(Duration::from_millis(20)) {
                    Ok(bytes) => {
                        if !running.load(Ordering::Acquire) { break; }
                        if let Err(message) = client.send_pcm(&bytes) {
                            *error.lock().unwrap() = Some(message);
                            break;
                        }
                    }
                    Err(mpsc::RecvTimeoutError::Timeout) => {},
                    Err(_) => break,
                }
            }
        }).map_err(|e| format!("无法启动音频线程：{e}"))?;
        audio.output.as_ref().unwrap().play().map_err(|e| format!("无法启动播放设备：{e}"))?;
        audio.input.as_ref().unwrap().play().map_err(|e| format!("无法启动麦克风，请检查系统权限：{e}"))?;
        Ok(audio)
    }

    pub fn error(&self) -> Option<String> { self.error.lock().unwrap().clone() }
}

impl Drop for VoiceAudio {
    fn drop(&mut self) {
        self.active.store(false, Ordering::Release);
        self.input.take();
        self.output.take();
        self.client.playback().lock().unwrap().reset(0);
    }
}

fn capture<T: SizedSample + Sample>(device: &cpal::Device, config: &StreamConfig, id: u64,
    tx: mpsc::SyncSender<[u8; PACKET_BYTES]>, errors: Arc<Mutex<Option<String>>>) -> Result<Stream, String>
where f32: FromSample<T> {
    let channels = config.channels as usize;
    let mut resampler = Resampler::new(config.sample_rate.0);
    let mut frame = [0i16; FRAME_SAMPLES];
    let (mut filled, mut sequence) = (0, 0);
    device.build_input_stream(config, move |data: &[T], _| {
        for samples in data.chunks_exact(channels) {
            let mono = samples.iter().map(|s| s.to_sample::<f32>()).sum::<f32>() / channels as f32;
            resampler.push(mono, |sample| {
                frame[filled] = (sample.clamp(-1., 1.) * 32767.) as i16;
                filled += 1;
                if filled == FRAME_SAMPLES {
                    let _ = tx.try_send(packet(id, sequence, &frame));
                    sequence += 1;
                    filled = 0;
                }
            });
        }
    }, move |e| { if let Ok(mut error) = errors.try_lock() { *error = Some(format!("麦克风已中断：{e}")); } }, None)
        .map_err(|e| format!("无法打开麦克风，请检查系统权限：{e}"))
}

fn playback<T: SizedSample + FromSample<f32>>(device: &cpal::Device, config: &StreamConfig,
    queue: Arc<Mutex<Playback>>, errors: Arc<Mutex<Option<String>>>) -> Result<Stream, String> {
    let channels = config.channels as usize;
    let step = 48000. / config.sample_rate.0 as f64;
    let (mut phase, mut previous, mut next) = (1.0f64, 0.0f32, 0.0f32);
    device.build_output_stream(config, move |data: &mut [T], _| {
        let Ok(mut playback) = queue.try_lock() else { data.fill(T::EQUILIBRIUM); return; };
        for samples in data.chunks_mut(channels) {
            while phase >= 1. {
                previous = next;
                next = playback.samples.pop_front().unwrap_or(0) as f32 / 32768.;
                phase -= 1.;
            }
            let value = previous + (next - previous) * phase as f32;
            samples.fill(T::from_sample(value));
            phase += step;
        }
    }, move |e| { if let Ok(mut error) = errors.try_lock() { *error = Some(format!("播放设备已中断：{e}")); } }, None)
        .map_err(|e| format!("无法打开播放设备：{e}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn playback_is_bounded_and_rejects_previous_stream() {
        let mut queue = Playback::default();
        queue.reset(7);
        queue.receive(&packet(6, 0, &[11; FRAME_SAMPLES]));
        assert!(queue.samples.is_empty());
        for seq in 0..20 { queue.receive(&packet(7, seq, &[seq as i16; FRAME_SAMPLES])); }
        assert_eq!(queue.samples.len(), QUEUE_SAMPLES);
        assert_eq!(queue.samples.front(), Some(&16));
        queue.reset(8);
        assert!(queue.samples.is_empty());
    }

    #[test]
    fn resampling_keeps_a_continuous_48khz_clock() {
        for rate in [44100, 48000, 96000] {
            let mut resampler = Resampler::new(rate);
            let mut count = 0;
            for _ in 0..=rate { resampler.push(0.5, |s| { assert!((s - 0.5).abs() < 0.001); count += 1; }); }
            assert!((count as i32 - 48000).abs() <= 1, "{rate}: {count}");
        }
    }

    #[test]
    fn device_format_selection_avoids_eight_bit_when_available() {
        assert!(format_priority(SampleFormat::F32) < format_priority(SampleFormat::I8));
        assert!(format_priority(SampleFormat::I16) < format_priority(SampleFormat::U8));
    }
}
