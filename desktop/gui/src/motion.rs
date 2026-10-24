//! Android DockPhysics 的解析弹簧与路由过渡；仅保存 GUI 展示状态。
use std::f32::consts::PI;

#[derive(Clone, Debug)]
pub struct Spring {
    pub value: f32,
    pub velocity: f32,
    target: f32,
}

impl Spring {
    pub fn new(value: f32) -> Self { Self { value, velocity: 0.0, target: value } }
    pub fn to(&mut self, target: f32) { self.target = target; }
    pub fn active(&self) -> bool { self.value != self.target || self.velocity != 0.0 }
    pub fn snap(&mut self) { self.value = self.target; self.velocity = 0.0; }
    pub fn step(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 0.064);
        let omega = 2.0 * PI / 0.5;
        let damping = 0.68;
        let offset = self.value - self.target;
        let decay = (-damping * omega * dt).exp();
        let wd = omega * (1.0_f32 - damping * damping).sqrt();
        let (sine, cosine) = (wd * dt).sin_cos();
        let next = decay * (offset * cosine + (self.velocity + damping * omega * offset) / wd * sine);
        let speed = decay * (self.velocity * cosine
            - (damping * omega * self.velocity + omega * omega * offset) / wd * sine);
        self.value = self.target + next;
        self.velocity = speed;
        if next.abs() < 0.001 && speed.abs() < 0.001 { self.snap(); }
    }
}

#[derive(Debug)]
pub struct NavigationMotion {
    pub spring: Spring,
    pub incoming: f32,
    pub outgoing: f32,
    elapsed: f32,
    secondary: bool,
}

impl Default for NavigationMotion {
    fn default() -> Self {
        Self { spring: Spring::new(0.0), incoming: 1.0, outgoing: 0.0, elapsed: 1.0, secondary: false }
    }
}

impl NavigationMotion {
    pub fn begin(&mut self, index: f32, secondary: bool, reduce: bool) {
        self.spring.to(index);
        self.elapsed = 0.0;
        self.secondary = secondary;
        self.incoming = 0.0;
        self.outgoing = 1.0;
        if reduce { self.finish(); }
    }
    pub fn finish(&mut self) {
        self.spring.snap();
        self.incoming = 1.0;
        self.outgoing = 0.0;
        self.elapsed = 1.0;
    }
    pub fn active(&self) -> bool { self.spring.active() || self.incoming < 1.0 || self.outgoing > 0.0 }
    pub fn step(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 0.064);
        self.spring.step(dt);
        self.elapsed += dt;
        let enter = if self.secondary { 0.320 } else { 0.220 };
        let exit = if self.secondary { 0.320 } else { 0.140 };
        self.incoming = (self.elapsed / enter).min(1.0);
        self.outgoing = 1.0 - (self.elapsed / exit).min(1.0);
    }
}

/// 只在接受导航后启动；重定向沿用弹簧速度及上一帧时间。
pub fn bind(window: &crate::MainWindow) {
    use slint::{ComponentHandle, Timer, TimerMode};
    use std::{cell::RefCell, rc::Rc, time::{Duration, Instant}};
    let state = Rc::new(RefCell::new((NavigationMotion::default(), Instant::now())));
    let timer = Rc::new(Timer::default());
    let weak = window.as_weak();
    let state_for_start = state.clone();
    let timer_for_start = timer.clone();
    window.on_animate_navigation(move |index, secondary, backwards| {
        let Some(w) = weak.upgrade() else { return };
        let reduce = w.global::<crate::Theme>().get_reduce_motion();
        {
            let mut state = state_for_start.borrow_mut();
            if !timer_for_start.running() { state.1 = Instant::now(); }
            state.0.begin(index as f32, secondary, reduce);
            w.set_nav_position(state.0.spring.value);
            w.set_nav_velocity(state.0.spring.velocity);
            w.set_transition_in(state.0.incoming);
            w.set_transition_out(state.0.outgoing);
            w.set_transition_secondary(secondary);
            w.set_transition_direction(if backwards { -1.0 } else { 1.0 });
        }
        if reduce { timer_for_start.stop(); w.set_previous_page("".into()); return; }
        if timer_for_start.running() { return; }
        let weak = weak.clone();
        let state = state_for_start.clone();
        let timer = Rc::downgrade(&timer_for_start);
        timer_for_start.start(TimerMode::Repeated, Duration::from_millis(16), move || {
            let Some(w) = weak.upgrade() else { if let Some(t) = timer.upgrade() { t.stop(); } return };
            let mut state = state.borrow_mut();
            let now = Instant::now();
            let dt = (now - state.1).as_secs_f32();
            state.1 = now;
            if w.global::<crate::Theme>().get_reduce_motion() || !w.window().is_visible() {
                state.0.finish();
            } else { state.0.step(dt); }
            w.set_nav_position(state.0.spring.value);
            w.set_nav_velocity(state.0.spring.velocity);
            w.set_transition_in(state.0.incoming);
            w.set_transition_out(state.0.outgoing);
            if !state.0.active() {
                w.set_previous_page("".into());
                if let Some(t) = timer.upgrade() { t.stop(); }
            }
        });
    });
    let weak = window.as_weak();
    let finish = move || {
        timer.stop();
        let mut state = state.borrow_mut();
        state.0.finish();
        if let Some(w) = weak.upgrade() {
            w.set_nav_position(state.0.spring.value);
            w.set_nav_velocity(0.0);
            w.set_transition_in(1.0);
            w.set_transition_out(0.0);
            w.set_previous_page("".into());
        }
    };
    window.global::<crate::Theme>().on_finish_motion(finish);
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn spring_converges_and_stops() {
        let mut spring = Spring::new(0.0);
        spring.to(4.0);
        for _ in 0..240 { spring.step(0.016); }
        assert_eq!(spring.value, 4.0);
        assert!(!spring.active());
    }
    #[test]
    fn retarget_preserves_momentum_and_finishes_at_last_target() {
        let mut spring = Spring::new(0.0);
        spring.to(4.0); spring.step(0.032);
        let speed = spring.velocity;
        spring.to(1.0);
        assert_eq!(spring.velocity, speed);
        spring.step(0.016); spring.to(3.0);
        for _ in 0..240 { spring.step(0.016); }
        assert_eq!(spring.value, 3.0);
    }
    #[test]
    fn frame_gap_is_capped_at_64ms() {
        let mut a = Spring::new(0.0); a.to(4.0);
        let mut b = a.clone();
        a.step(10.0); b.step(0.064);
        assert_eq!(a.value, b.value); assert_eq!(a.velocity, b.velocity);
    }
    #[test]
    fn reduced_motion_finishes_every_transition_immediately() {
        let mut motion = NavigationMotion::default();
        motion.begin(3.0, true, true);
        assert!(!motion.active());
        assert_eq!((motion.incoming, motion.outgoing, motion.spring.value), (1.0, 0.0, 3.0));
        motion.begin(1.0, false, false); motion.step(0.032); motion.finish();
        assert!(!motion.active());
    }
    #[test]
    fn rapid_navigation_keeps_only_the_latest_transition() {
        let mut motion = NavigationMotion::default();
        for index in [1.0, 4.0, 2.0, 3.0] {
            motion.begin(index, false, false); motion.step(0.016);
        }
        for _ in 0..240 { motion.step(0.016); }
        assert!(!motion.active()); assert_eq!(motion.spring.value, 3.0);
    }
    #[test]
    fn primary_and_secondary_transition_timings() {
        let mut motion = NavigationMotion::default(); motion.begin(1.0, false, false);
        for _ in 0..10 { motion.step(0.016); }
        assert_eq!(motion.outgoing, 0.0); assert!(motion.incoming < 1.0);
        motion.begin(1.0, true, false);
        for _ in 0..20 { motion.step(0.016); }
        assert!((motion.incoming - 1.0).abs() < 0.00001);
    }
}
