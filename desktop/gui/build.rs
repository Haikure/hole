mod build_support;

fn main() {
    build_support::embed_core().expect("嵌入 Go 核心失败");
    windows_icon().expect("嵌入应用图标失败");
}

fn windows_icon() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=ui/assets/app-icon.png");
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() != Ok("windows")
        || std::env::var_os("CARGO_FEATURE_GUI").is_none()
    {
        return Ok(());
    }
    let output = std::path::PathBuf::from(std::env::var_os("OUT_DIR").ok_or("OUT_DIR 缺失")?);
    let image = image::load_from_memory(include_bytes!("ui/assets/app-icon.png"))?.resize_exact(
        256,
        256,
        image::imageops::FilterType::Lanczos3,
    );
    let mut png = std::io::Cursor::new(Vec::new());
    image.write_to(&mut png, image::ImageFormat::Png)?;
    let png = png.into_inner();
    let mut ico = vec![0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 1, 0, 32, 0];
    ico.extend_from_slice(&(png.len() as u32).to_le_bytes());
    ico.extend_from_slice(&22u32.to_le_bytes());
    ico.extend_from_slice(&png);
    let icon = output.join("hole.ico");
    std::fs::write(&icon, ico)?;
    let resource = output.join("hole-icon.rc");
    std::fs::write(
        &resource,
        format!(
            "1 ICON \"{}\"\n",
            icon.to_string_lossy().replace('\\', "\\\\")
        ),
    )?;
    let msvc = std::env::var("CARGO_CFG_TARGET_ENV").as_deref() == Ok("msvc");
    let object = output.join(if msvc { "hole-icon.res" } else { "hole-icon.o" });
    let compiler =
        std::env::var_os("RC").unwrap_or_else(|| if msvc { "rc.exe" } else { "windres" }.into());
    let mut command = std::process::Command::new(compiler);
    if msvc {
        command
            .arg("/nologo")
            .arg("/fo")
            .arg(&object)
            .arg(&resource);
    } else {
        command.arg("-i").arg(&resource).arg("-o").arg(&object);
    }
    if !command.status()?.success() {
        return Err("Windows 资源编译失败".into());
    }
    println!("cargo:rustc-link-arg-bin=hole-desktop={}", object.display());
    Ok(())
}
