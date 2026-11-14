//! 与 Android DockSpring 相同的阻尼弹簧；仅驱动展示。
#[cfg(feature = "gui")]
use gpui::{Animation, AnimationExt, AnyElement, Div, IntoElement, Styled, px};
use std::time::Duration;

pub const FAST: Duration = Duration::from_millis(160);
pub const NORMAL: Duration = Duration::from_millis(260);

#[cfg(feature = "gui")]
pub fn enter(id: impl Into<gpui::ElementId>, element: Div) -> AnyElement {
    element
        .with_animation(
            id,
            Animation::new(NORMAL).with_easing(gpui::ease_out_quint()),
            |el, t| el.relative().top(px(12. * (1. - t))).opacity(t),
        )
        .into_any_element()
}

/// 保留离场内容直到弹簧归零，避免关闭抽屉、对话框和提示时突然消失。
pub struct Presence<T> {
    pub content: Option<T>,
    pub progress: Spring,
}
impl<T: Clone> Presence<T> {
    pub fn new() -> Self {
        Self {
            content: None,
            progress: Spring::new(0.),
        }
    }
    pub fn sync(&mut self, content: Option<T>, reduce: bool) {
        self.progress.target = if content.is_some() { 1. } else { 0. };
        if content.is_some() {
            self.content = content;
        }
        if reduce {
            self.step(0., true);
        }
    }
    pub fn step(&mut self, dt: f32, reduce: bool) -> bool {
        let changed = self.progress.step(dt, reduce);
        if self.progress.target == 0. && self.progress.value <= 0. {
            self.progress.value = 0.;
            self.progress.velocity = 0.;
            self.content = None;
        }
        changed
    }
    pub fn value(&self) -> f32 {
        self.progress.value.clamp(0., 1.)
    }
}

pub struct Spring {
    pub value: f32,
    pub velocity: f32,
    pub target: f32,
}
impl Spring {
    pub fn new(value: f32) -> Self {
        Self {
            value,
            velocity: 0.,
            target: value,
        }
    }
    pub fn active(&self) -> bool {
        self.value != self.target || self.velocity != 0.
    }
    pub fn step(&mut self, dt: f32, reduce: bool) -> bool {
        if !self.active() {
            return false;
        }
        if reduce {
            self.value = self.target;
            self.velocity = 0.;
            return true;
        }
        let omega = 2. * std::f32::consts::PI / 0.5;
        let damping = 0.68;
        let offset = self.value - self.target;
        let decay = (-damping * omega * dt).exp();
        let wd = omega * (1. - damping * damping).sqrt();
        let cosine = (wd * dt).cos();
        let sine = (wd * dt).sin();
        let next =
            decay * (offset * cosine + (self.velocity + damping * omega * offset) / wd * sine);
        self.velocity = decay
            * (self.velocity * cosine
                - (damping * omega * self.velocity + omega * omega * offset) / wd * sine);
        self.value = self.target + next;
        if next.abs() < 0.001 && self.velocity.abs() < 0.001 {
            self.value = self.target;
            self.velocity = 0.;
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn overlay_retains_departing_content_and_can_reverse_before_settling() {
        let mut overlay = Presence::new();
        overlay.sync(Some("draft"), false);
        overlay.step(0.1, false);
        assert!(overlay.value() > 0.);
        overlay.sync(None, false);
        overlay.step(0.016, false);
        assert_eq!(overlay.content, Some("draft"));
        overlay.sync(Some("replacement"), false);
        for _ in 0..100 {
            overlay.step(0.016, false);
        }
        assert_eq!(overlay.content, Some("replacement"));
        assert_eq!(overlay.value(), 1.);
        overlay.sync(None, true);
        assert!(overlay.content.is_none());
        assert!(!overlay.progress.active());
    }
    #[test]
    fn closed_overlay_releases_content_and_stops_requesting_frames() {
        let mut overlay = Presence::new();
        overlay.sync(Some("detail"), true);
        overlay.sync(None, false);
        assert_eq!(overlay.content, Some("detail"));
        for _ in 0..100 {
            overlay.step(0.016, false);
        }
        assert!(overlay.content.is_none());
        assert_eq!(overlay.value(), 0.);
        assert!(!overlay.progress.active());
        assert!(!overlay.step(0.016, false));
    }
}
