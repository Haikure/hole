//! 配置、凭据与运行意图的持久化。
//!
//! - 普通配置：`config.json`，原子写入（临时文件 + rename）。
//! - 凭据：`secrets.json`，独立文件，POSIX 下 0600；Windows 依赖用户目录 ACL。
//!   系统凭据库（DPAPI / Secret Service）接入留在 platform 层，本框架先用受保护文件。
//! - 运行意图：`run-state.json`，记录用户是否请求运行，用于登录恢复。

use crate::config::{Secrets, StoredConfig};
use serde::{Deserialize, Serialize};
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};

pub const MAX_DOCUMENT_BYTES: usize = 128 * 1024;

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct RunState {
    pub run_requested: bool,
    pub last_resume_error: String,
}

pub struct Storage {
    pub dir: PathBuf,
}

impl Storage {
    pub fn open() -> Result<Self, String> {
        let base = std::env::var_os("HOLE_DESKTOP_CONFIG_DIR")
            .map(PathBuf::from)
            .or_else(|| dirs::config_dir().map(|d| d.join("hole-desktop")))
            .ok_or("无法确定配置目录")?;
        fs::create_dir_all(&base).map_err(|e| format!("无法创建配置目录 {}：{e}", base.display()))?;
        Ok(Storage { dir: base })
    }

    fn path(&self, name: &str) -> PathBuf {
        self.dir.join(name)
    }

    pub fn load_config(&self) -> Result<StoredConfig, String> {
        let path = self.path("config.json");
        if !path.exists() {
            return Ok(StoredConfig::default());
        }
        let text = fs::read_to_string(&path).map_err(|e| format!("读取配置失败：{e}"))?;
        serde_json::from_str(&text).map_err(|e| format!("配置文件格式无效，保留原文件：{e}"))
    }

    pub fn save_config(&self, cfg: &StoredConfig) -> Result<(), String> {
        let text = serde_json::to_string_pretty(cfg).map_err(|e| e.to_string())?;
        atomic_write(&self.path("config.json"), text.as_bytes(), false)
    }

    pub fn load_secrets(&self) -> Secrets {
        fs::read_to_string(self.path("secrets.json"))
            .ok()
            .and_then(|t| serde_json::from_str(&t).ok())
            .unwrap_or_default()
    }

    pub fn save_secrets(&self, secrets: &Secrets) -> Result<(), String> {
        let text = serde_json::to_string(secrets).map_err(|e| e.to_string())?;
        atomic_write(&self.path("secrets.json"), text.as_bytes(), true)
    }

    pub fn load_run_state(&self) -> RunState {
        fs::read_to_string(self.path("run-state.json"))
            .ok()
            .and_then(|t| serde_json::from_str(&t).ok())
            .unwrap_or_default()
    }

    pub fn save_run_state(&self, state: &RunState) -> Result<(), String> {
        let text = serde_json::to_string(state).map_err(|e| e.to_string())?;
        atomic_write(&self.path("run-state.json"), text.as_bytes(), false)
    }

    /// 读取导入文件，限制 128 KiB。
    pub fn read_document(path: &Path) -> Result<String, String> {
        let meta = fs::metadata(path).map_err(|e| format!("无法读取文件：{e}"))?;
        if meta.len() as usize > MAX_DOCUMENT_BYTES {
            return Err("文件超过 128 KiB".into());
        }
        let bytes = fs::read(path).map_err(|e| format!("无法读取文件：{e}"))?;
        String::from_utf8(bytes).map_err(|_| "文件不是 UTF-8 文本".into())
    }

    /// 写出导出文件；不覆盖已存在的文件，含凭据时收紧权限。
    pub fn write_document(path: &Path, text: &str, sensitive: bool) -> Result<(), String> {
        if path.exists() {
            return Err(format!("文件已存在，不覆盖：{}", path.display()));
        }
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                fs::create_dir_all(parent).map_err(|e| format!("无法创建目录：{e}"))?;
            }
        }
        atomic_write(path, text.as_bytes(), sensitive)
    }
}

fn atomic_write(path: &Path, data: &[u8], sensitive: bool) -> Result<(), String> {
    let parent = path.parent().ok_or("路径无效")?;
    let tmp = parent.join(format!(".{}.tmp-{}", path.file_name().and_then(|n| n.to_str()).unwrap_or("file"), std::process::id()));
    {
        let mut options = fs::OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(if sensitive { 0o600 } else { 0o644 });
        }
        let mut file = options.open(&tmp).map_err(|e| format!("无法写入 {}：{e}", tmp.display()))?;
        file.write_all(data).and_then(|_| file.sync_all()).map_err(|e| format!("写入失败：{e}"))?;
    }
    #[cfg(unix)]
    if sensitive {
        use std::os::unix::fs::PermissionsExt;
        let _ = fs::set_permissions(&tmp, fs::Permissions::from_mode(0o600));
    }
    fs::rename(&tmp, path).map_err(|e| {
        let _ = fs::remove_file(&tmp);
        format!("替换 {} 失败：{e}", path.display())
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roundtrip_in_temp_dir() {
        let dir = std::env::temp_dir().join(format!("hole-desktop-test-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let storage = Storage { dir: dir.clone() };
        let mut cfg = StoredConfig::default();
        cfg.connection.room = "r".into();
        storage.save_config(&cfg).unwrap();
        assert_eq!(storage.load_config().unwrap().connection.room, "r");
        storage.save_secrets(&Secrets { password: "p".into(), ..Default::default() }).unwrap();
        assert_eq!(storage.load_secrets().password, "p");
        storage.save_run_state(&RunState { run_requested: true, ..Default::default() }).unwrap();
        assert!(storage.load_run_state().run_requested);
        let out = dir.join("export.yaml");
        Storage::write_document(&out, "a: 1\n", false).unwrap();
        assert!(Storage::write_document(&out, "b", false).is_err());
        let _ = fs::remove_dir_all(&dir);
    }
}
