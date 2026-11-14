//! 与 Android DockSpring 相同的阻尼弹簧；仅驱动展示。
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
