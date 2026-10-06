//! Windows extraction lifecycle. The platform-neutral parts also run in Linux
//! tests; cleanup only touches recognized, unlocked directories and known files.
use sha2::{Digest, Sha256};
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read};
use std::path::{Path, PathBuf};

pub struct RuntimeFiles {
    directory: PathBuf,
    lock: Option<File>,
    image: Option<File>,
}

fn plain(path: &Path, directory: bool) -> bool {
    let Ok(meta) = fs::symlink_metadata(path) else { return false };
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if meta.file_attributes() & 0x400 != 0 { return false; } // Reparse point.
    }
    !meta.file_type().is_symlink() && if directory { meta.is_dir() } else { meta.is_file() }
}

fn lock_file(path: &Path, create: bool) -> io::Result<File> {
    let mut options = OpenOptions::new();
    options.read(true).write(true).create_new(create);
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;
        options.share_mode(0);
    }
    let file = options.open(path)?;
    #[cfg(target_os = "linux")]
    {
        use std::os::fd::AsRawFd;
        if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } < 0 { return Err(io::Error::last_os_error()); }
    }
    Ok(file)
}

#[cfg(windows)]
fn private_directory(path: &Path) -> io::Result<()> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::{Foundation::LocalFree, Security::{Authorization::{ConvertStringSecurityDescriptorToSecurityDescriptorW, SDDL_REVISION_1}, SECURITY_ATTRIBUTES}, Storage::FileSystem::CreateDirectoryW};
    let sddl: Vec<u16> = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;OW)\0".encode_utf16().collect();
    let mut descriptor = std::ptr::null_mut();
    if unsafe { ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl.as_ptr(), SDDL_REVISION_1, &mut descriptor, std::ptr::null_mut()) } == 0 { return Err(io::Error::last_os_error()); }
    let security = SECURITY_ATTRIBUTES { nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32, lpSecurityDescriptor: descriptor, bInheritHandle: 0 };
    let path: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
    let result = unsafe { CreateDirectoryW(path.as_ptr(), &security) };
    let error = io::Error::last_os_error();
    unsafe { LocalFree(descriptor); }
    if result == 0 { Err(error) } else { Ok(()) }
}

#[cfg(target_os = "linux")]
fn private_directory(path: &Path) -> io::Result<()> {
    use std::os::unix::fs::DirBuilderExt;
    fs::DirBuilder::new().mode(0o700).create(path)
}

impl RuntimeFiles {
    pub fn prepare(base: &Path, gzip: &[u8], size: usize, hash: &[u8; 32]) -> Result<Self, String> {
        fs::create_dir_all(base).map_err(|e| format!("无法创建核心临时目录：{e}"))?;
        if !plain(base, true) { return Err("核心临时目录不能是链接或重解析点".into()); }
        cleanup_stale(base);
        let digest = hash.iter().map(|b| format!("{b:02x}")).collect::<String>();
        let directory = base.join(format!("v1-{digest}-{}", uuid::Uuid::new_v4().simple()));
        private_directory(&directory).map_err(|e| format!("无法创建私有核心目录：{e}"))?;
        let mut files = Self { directory, lock: None, image: None };
        files.lock = Some(lock_file(&files.directory.join("owner.lock"), true).map_err(|e| format!("无法锁定核心临时目录：{e}"))?);
        let mut options = OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            options.share_mode(0);
        }
        let mut image = options.open(files.path()).map_err(|e| format!("无法创建临时核心：{e}"))?;
        crate::core_host::unpack(gzip, size, hash, &mut image)?;
        image.sync_all().map_err(|e| format!("无法写入临时核心：{e}"))?;
        drop(image);
        let mut options = OpenOptions::new();
        options.read(true);
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            options.share_mode(1); // FILE_SHARE_READ: deny changes until child exit.
        }
        let mut image = options.open(files.path()).map_err(|e| format!("无法锁定临时核心：{e}"))?;
        let mut digest = Sha256::new();
        let copied = io::copy(&mut (&mut image).take(size as u64 + 1), &mut digest).map_err(|e| e.to_string())?;
        let actual: [u8; 32] = digest.finalize().into();
        if copied != size as u64 || actual != *hash { return Err("临时核心完整性校验失败".into()); }
        files.image = Some(image);
        Ok(files)
    }

    pub fn path(&self) -> PathBuf { self.directory.join("core.exe") }
}

impl Drop for RuntimeFiles {
    fn drop(&mut self) {
        self.image.take();
        // Keep the lease while removing the image. A still-running Windows
        // child can refuse deletion; the next launch retries that directory.
        let removed = fs::remove_file(self.path()).map(|_| true).unwrap_or_else(|e| e.kind() == io::ErrorKind::NotFound);
        self.lock.take();
        if removed {
            let _ = fs::remove_file(self.directory.join("owner.lock"));
            let _ = fs::remove_dir(&self.directory);
        }
    }
}

fn owned_name(name: &str) -> bool {
    name.len() == 100 && name.starts_with("v1-") && name.as_bytes()[67] == b'-'
        && name[3..67].bytes().chain(name[68..].bytes()).all(|c| c.is_ascii_hexdigit())
}

pub fn cleanup_stale(base: &Path) {
    let Ok(entries) = fs::read_dir(base) else { return };
    for entry in entries.flatten() {
        let directory = entry.path();
        if !owned_name(&entry.file_name().to_string_lossy()) || !plain(&directory, true) { continue; }
        let Ok(children) = fs::read_dir(&directory) else { continue };
        if children.into_iter().any(|c| c.map(|c| !matches!(c.file_name().to_str(), Some("owner.lock" | "core.exe")) || !plain(&c.path(), false)).unwrap_or(true)) { continue; }
        let lock_path = directory.join("owner.lock");
        if !lock_path.exists() {
            let _ = fs::remove_dir(&directory); // Empty interrupted creation only.
            continue;
        }
        let Ok(lock) = lock_file(&lock_path, false) else { continue };
        drop(RuntimeFiles { directory, lock: Some(lock), image: None });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn live_instances_are_isolated_and_only_abandoned_files_are_removed() {
        let base = std::env::temp_dir().join(format!("hole-runtime-test-{}", uuid::Uuid::new_v4()));
        let data = b"a test core";
        let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
        std::io::Write::write_all(&mut encoder, data).unwrap();
        let gzip = encoder.finish().unwrap();
        let hash = Sha256::digest(data).into();
        let a = RuntimeFiles::prepare(&base, &gzip, data.len(), &hash).unwrap();
        let mut b = RuntimeFiles::prepare(&base, &gzip, data.len(), &hash).unwrap();
        assert_ne!(a.path(), b.path());
        cleanup_stale(&base);
        assert!(a.path().exists() && b.path().exists());
        // Simulate an interrupted GUI releasing OS handles but skipping Drop.
        b.image.take(); b.lock.take();
        let stale = b.directory.clone();
        std::mem::forget(b);
        cleanup_stale(&base);
        assert!(!stale.exists());
        assert!(a.path().exists());
        let live = a.directory.clone();
        drop(a);
        assert!(!live.exists());
        fs::write(base.join("unrelated.txt"), b"keep").unwrap();
        assert!(RuntimeFiles::prepare(&base, &gzip, data.len(), &[0; 32]).is_err());
        assert_eq!(fs::read_dir(&base).unwrap().count(), 1);
        fs::remove_file(base.join("unrelated.txt")).unwrap();
        fs::remove_dir(base).unwrap();
    }

    #[test]
    fn cleanup_does_not_follow_links_or_delete_unrecognized_content() {
        let base = std::env::temp_dir().join(format!("hole-runtime-test-{}", uuid::Uuid::new_v4()));
        fs::create_dir(&base).unwrap();
        let name = format!("v1-{}-{}", "a".repeat(64), "b".repeat(32));
        let directory = base.join(name);
        fs::create_dir(&directory).unwrap();
        fs::write(directory.join("keep.txt"), b"keep").unwrap();
        cleanup_stale(&base);
        assert!(directory.join("keep.txt").exists());
        fs::remove_file(directory.join("keep.txt")).unwrap();
        fs::remove_dir(directory).unwrap();
        fs::remove_dir(base).unwrap();
    }
}
