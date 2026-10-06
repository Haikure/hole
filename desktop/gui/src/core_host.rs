//! Resolve a development host or prepare the verified core embedded at build time.
//! PreparedCore owns its backing storage until the child has exited.
use flate2::read::GzDecoder;
use sha2::{Digest, Sha256};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};

include!(concat!(env!("OUT_DIR"), "/embedded_core.rs"));

pub struct PreparedCore {
    pub path: PathBuf,
    pub description: String,
    // The handles are intentionally kept alive, not read after preparation.
    #[allow(dead_code)]
    backing: Backing,
}

#[allow(dead_code)]
enum Backing {
    External,
    #[cfg(target_os = "linux")]
    Memory(std::fs::File),
    #[cfg(windows)]
    Temporary(crate::core_runtime::RuntimeFiles),
}

pub fn prepare() -> Result<PreparedCore, String> {
    if let Some(path) = std::env::var_os("HOLE_DESKTOP_CORE") {
        return external(Path::new(&path));
    }
    if !CORE_GZIP.is_empty() { return embedded(); }
    external(&crate::core_client::locate_bridge()?)
}

fn external(path: &Path) -> Result<PreparedCore, String> {
    if !path.is_absolute() { return Err("HOLE_DESKTOP_CORE 必须是绝对路径".into()); }
    let path = path.canonicalize().map_err(|e| format!("无法读取指定核心 {}：{e}", path.display()))?;
    if !path.is_file() { return Err("指定核心不是普通文件".into()); }
    Ok(PreparedCore { description: path.display().to_string(), path, backing: Backing::External })
}

pub fn unpack(data: &[u8], expected_size: usize, expected_hash: &[u8; 32], output: &mut impl Write) -> Result<(), String> {
    if expected_size == 0 || expected_size > 128 * 1024 * 1024 { return Err("内嵌核心大小无效".into()); }
    let mut decoder = GzDecoder::new(data).take(expected_size as u64 + 1);
    let mut hash = Sha256::new();
    let mut size = 0;
    let mut buffer = [0u8; 64 * 1024];
    loop {
        let count = decoder.read(&mut buffer).map_err(|_| "内嵌核心解压失败")?;
        if count == 0 { break; }
        size += count;
        if size > expected_size { return Err("内嵌核心超过预期大小".into()); }
        hash.update(&buffer[..count]);
        output.write_all(&buffer[..count]).map_err(|e| format!("无法释放内嵌核心：{e}"))?;
    }
    let actual: [u8; 32] = hash.finalize().into();
    if size != expected_size || actual != *expected_hash { return Err("内嵌核心完整性校验失败".into()); }
    Ok(())
}

pub fn embedded() -> Result<PreparedCore, String> {
    if CORE_GZIP.is_empty() { return Err("此开发构建未嵌入核心，请使用 ./build.sh desktop".into()); }
    #[cfg(target_os = "linux")]
    {
        use std::os::fd::{AsRawFd, FromRawFd};
        // MFD_EXEC is explicit on kernels with memfd_noexec policy. Older
        // kernels reject the flag with EINVAL; their default permits execution.
        let flags = libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING;
        let mut fd = unsafe { libc::memfd_create(c"hole-desktop-core".as_ptr(), flags | 0x0010) };
        if fd < 0 && std::io::Error::last_os_error().raw_os_error() == Some(libc::EINVAL) {
            fd = unsafe { libc::memfd_create(c"hole-desktop-core".as_ptr(), flags) };
        }
        if fd < 0 { return Err(format!("无法创建可执行 memfd：{}", std::io::Error::last_os_error())); }
        // SAFETY: a successful memfd_create returns a new owned descriptor.
        let mut file = unsafe { std::fs::File::from_raw_fd(fd) };
        unpack(CORE_GZIP, CORE_SIZE, &CORE_HASH, &mut file)?;
        if unsafe { libc::fchmod(fd, 0o500) } < 0 { return Err(format!("无法设置核心执行权限：{}", std::io::Error::last_os_error())); }
        let seals = libc::F_SEAL_WRITE | libc::F_SEAL_GROW | libc::F_SEAL_SHRINK | libc::F_SEAL_SEAL;
        if unsafe { libc::fcntl(fd, libc::F_ADD_SEALS, seals) } < 0 { return Err(format!("无法封存内存核心：{}", std::io::Error::last_os_error())); }
        // CLOEXEC closes the child's descriptor only after the ELF image has
        // been opened by execve. No inheritable descriptor or disk fallback.
        let path = PathBuf::from(format!("/proc/self/fd/{}", file.as_raw_fd()));
        Ok(PreparedCore { path, description: "内嵌核心 · Linux memfd".into(), backing: Backing::Memory(file) })
    }
    #[cfg(windows)]
    {
        let base = dirs::data_local_dir().ok_or("无法确定本机用户数据目录")?.join("hole-desktop").join("runtime");
        let files = crate::core_runtime::RuntimeFiles::prepare(&base, CORE_GZIP, CORE_SIZE, &CORE_HASH)?;
        Ok(PreparedCore { path: files.path(), description: "内嵌核心 · Windows 私有临时文件".into(), backing: Backing::Temporary(files) })
    }
    #[cfg(not(any(target_os = "linux", windows)))]
    Err("内嵌核心仅支持 Linux / Windows".into())
}

#[cfg(test)]
mod tests {
    use super::*;
    use flate2::{write::GzEncoder, Compression};

    pub fn fixture() -> (Vec<u8>, Vec<u8>, [u8; 32]) {
        let data = b"verified embedded executable fixture".repeat(1000);
        let mut encoder = GzEncoder::new(Vec::new(), Compression::default());
        encoder.write_all(&data).unwrap();
        let hash = Sha256::digest(&data).into();
        (data, encoder.finish().unwrap(), hash)
    }

    #[test]
    fn rejects_corruption_wrong_hash_and_excessive_expansion() {
        let (data, compressed, hash) = fixture();
        let mut out = Vec::new();
        unpack(&compressed, data.len(), &hash, &mut out).unwrap();
        assert_eq!(out, data);
        assert!(unpack(&compressed, data.len(), &[0; 32], &mut Vec::new()).is_err());
        assert!(unpack(&compressed, data.len() - 1, &hash, &mut Vec::new()).is_err());
        assert!(unpack(&compressed, data.len() + 1, &hash, &mut Vec::new()).is_err());
        assert!(unpack(&compressed[..compressed.len() - 8], data.len(), &hash, &mut Vec::new()).is_err());
        assert!(unpack(&compressed, 129 * 1024 * 1024, &hash, &mut Vec::new()).is_err());
    }

    #[cfg(all(feature = "embedded-core", target_os = "linux"))]
    #[test]
    fn sealed_memfd_runs_real_core_and_releases_descriptor() {
        use std::os::fd::AsRawFd;
        let host = embedded().unwrap();
        let Backing::Memory(file) = &host.backing else { panic!("core was not loaded in memory") };
        let fd = file.as_raw_fd();
        let seals = unsafe { libc::fcntl(fd, libc::F_GET_SEALS) };
        assert_eq!(seals & libc::F_SEAL_WRITE, libc::F_SEAL_WRITE);
        assert!(std::fs::read_link(&host.path).unwrap().to_string_lossy().starts_with("/memfd:hole-desktop-core"));
        assert_eq!(unsafe { libc::pwrite(fd, b"X".as_ptr().cast(), 1, 0) }, -1);
        let path = host.path.clone();
        let client = crate::core_client::CoreClient::spawn(host, |_| {}).unwrap();
        assert_eq!(client.hello().unwrap().bridge_version, 1);
        assert_eq!(client.call("snapshot", None).unwrap()["snapshot"]["run_requested"], false);
        client.shutdown(std::time::Duration::from_secs(3));
        assert!(!path.exists(), "memfd retained after shutdown");
    }
}
