//! 与图形框架无关的数据，快照刷新不会覆盖表单草稿。
#[derive(Debug, Clone, Default, PartialEq)]
pub struct VoiceMemberRow {
    pub name: String,
    pub local: bool,
    pub enabled: bool,
    pub state: String,
    pub media: String,
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct VoicePeerRow {
    pub name: String,
    pub state: String,
    pub media: String,
    pub path: String,
    pub bitrate: String,
    pub stats: String,
    pub buffering: String,
    pub error: String,
}

/// 手动重选与系统切网语义分离，和 PenMods / Android 使用相同的桥接方法。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum NetworkIntent {
    ReselectPath,
    NetworkChanged,
}
impl NetworkIntent {
    pub fn method(self) -> &'static str {
        match self {
            Self::ReselectPath => "renominate_transports",
            Self::NetworkChanged => "network_changed",
        }
    }
}
