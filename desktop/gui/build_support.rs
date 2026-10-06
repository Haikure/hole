use flate2::{write::GzEncoder, Compression};
use sha2::{Digest, Sha256};
use std::{env, fs, io::Write, path::Path};

const MAX_CORE_BYTES: usize = 128 * 1024 * 1024;

pub fn validate_target(data: &[u8], os: &str, arch: &str) -> Result<(), String> {
    if data.len() > MAX_CORE_BYTES { return Err("核心超过 128 MiB".into()); }
    let valid = match (os, arch) {
        ("linux", "x86_64" | "aarch64") => {
            data.len() >= 64 && &data[..4] == b"\x7fELF" && data[4] == 2 && data[5] == 1
                && u16::from_le_bytes([data[18], data[19]]) == if arch == "x86_64" { 62 } else { 183 }
        }
        ("windows", "x86_64" | "aarch64") => {
            if data.len() < 64 || &data[..2] != b"MZ" { false } else {
                let offset = u32::from_le_bytes(data[60..64].try_into().unwrap()) as usize;
                data.get(offset..).filter(|p| p.len() >= 26).is_some_and(|p| {
                    &p[..4] == b"PE\0\0" && u16::from_le_bytes([p[4], p[5]]) == if arch == "x86_64" { 0x8664 } else { 0xaa64 }
                        && u16::from_le_bytes([p[24], p[25]]) == 0x20b
                })
            }
        }
        _ => false,
    };
    if valid { Ok(()) } else { Err(format!("核心文件格式与 GUI 目标 {os}/{arch} 不匹配")) }
}

pub fn embed_core() -> Result<(), String> {
    println!("cargo:rerun-if-env-changed=HOLE_EMBED_CORE");
    println!("cargo:rerun-if-changed=build_support.rs");
    let output = env::var_os("OUT_DIR").ok_or("缺少 OUT_DIR")?;
    let output = Path::new(&output);
    let generated = if env::var_os("CARGO_FEATURE_EMBEDDED_CORE").is_some() {
        let source = env::var_os("HOLE_EMBED_CORE").ok_or("embedded-core 需要 HOLE_EMBED_CORE；请使用 ./build.sh desktop")?;
        let source = Path::new(&source);
        if !source.is_absolute() { return Err("HOLE_EMBED_CORE 必须是绝对路径".into()); }
        println!("cargo:rerun-if-changed={}", source.display());
        let size = fs::metadata(source).map_err(|e| e.to_string())?.len();
        if size > MAX_CORE_BYTES as u64 { return Err("核心超过 128 MiB".into()); }
        let data = fs::read(source).map_err(|e| e.to_string())?;
        validate_target(&data, &env::var("CARGO_CFG_TARGET_OS").unwrap_or_default(), &env::var("CARGO_CFG_TARGET_ARCH").unwrap_or_default())?;
        let hash: [u8; 32] = Sha256::digest(&data).into();
        let mut encoder = GzEncoder::new(Vec::new(), Compression::best());
        encoder.write_all(&data).map_err(|e| e.to_string())?;
        let compressed = encoder.finish().map_err(|e| e.to_string())?;
        fs::write(output.join("core.gz"), compressed).map_err(|e| e.to_string())?;
        format!("pub const CORE_SIZE: usize = {};\npub const CORE_HASH: [u8; 32] = {:?};\npub const CORE_GZIP: &[u8] = include_bytes!(concat!(env!(\"OUT_DIR\"), \"/core.gz\"));\n", data.len(), hash)
    } else {
        "pub const CORE_SIZE: usize = 0;\npub const CORE_HASH: [u8; 32] = [0; 32];\npub const CORE_GZIP: &[u8] = &[];\n".into()
    };
    fs::write(output.join("embedded_core.rs"), generated).map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_mismatched_or_truncated_payloads() {
        let mut elf = [0u8; 64];
        elf[..6].copy_from_slice(b"\x7fELF\x02\x01");
        elf[18..20].copy_from_slice(&62u16.to_le_bytes());
        assert!(validate_target(&elf, "linux", "x86_64").is_ok());
        assert!(validate_target(&elf, "linux", "aarch64").is_err());
        assert!(validate_target(&elf, "windows", "x86_64").is_err());
        assert!(validate_target(&elf[..20], "linux", "x86_64").is_err());
        let mut pe = [0u8; 128];
        pe[..2].copy_from_slice(b"MZ");
        pe[60..64].copy_from_slice(&64u32.to_le_bytes());
        pe[64..68].copy_from_slice(b"PE\0\0");
        pe[68..70].copy_from_slice(&0xaa64u16.to_le_bytes());
        pe[88..90].copy_from_slice(&0x20bu16.to_le_bytes());
        assert!(validate_target(&pe, "windows", "aarch64").is_ok());
        assert!(validate_target(&pe, "windows", "x86_64").is_err());
        pe[60..64].copy_from_slice(&u32::MAX.to_le_bytes());
        assert!(validate_target(&pe, "windows", "aarch64").is_err());
    }
}
