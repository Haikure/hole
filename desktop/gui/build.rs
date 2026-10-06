mod build_support;

fn main() {
    build_support::embed_core().expect("嵌入 Go 核心失败");
    // Fluent 风格作为桌面基础样式；视觉 token 由 ui/theme.slint 统一覆盖。
    let config = slint_build::CompilerConfiguration::new().with_style("fluent".into());
    slint_build::compile_with_config("ui/app.slint", config).expect("Slint UI 编译失败");
}
