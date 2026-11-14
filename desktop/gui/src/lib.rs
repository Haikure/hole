//! 桌面适配层。网络算法、路径调度与会话生命周期统一由 hole-core 执行。
pub mod config;
pub mod controller;
pub mod core_client;
pub mod core_host;
#[cfg(any(windows, all(test, target_os = "linux")))]
pub mod core_runtime;
pub mod model;
pub mod motion;
pub mod punch;
pub mod report;
pub mod snapshot;
pub mod storage;
pub mod voice_audio;
pub use model::{VoiceMemberRow, VoicePeerRow};
#[cfg(test)]
mod visual_fixtures {
    pub(crate) fn fixed_snapshot() -> crate::snapshot::Snapshot {
        crate::snapshot::parse_snapshot(
            &serde_json::from_str(include_str!("../tests/fixtures/fixed_snapshot.json")).unwrap(),
        )
        .unwrap()
    }
}
#[cfg(test)]
#[path = "../build_support.rs"]
mod build_support;
