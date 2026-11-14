#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]
mod app;
mod pages;
mod punch_ui;
mod settings;
mod ui;
use gpui::{App, AppContext, Bounds, TitlebarOptions, WindowBounds, WindowOptions, px, size};
use hole_desktop::motion;
use hole_desktop::{config::StoredConfig, storage::Storage};

fn main() {
    let storage = Storage::open().unwrap_or_else(|error| {
        eprintln!("{error}");
        std::process::exit(2)
    });
    let (config, error) = match storage.load_config() {
        Ok(config) => (config, String::new()),
        Err(error) => (StoredConfig::default(), error),
    };
    let secrets = storage.load_secrets();
    let run_state = storage.load_run_state();
    gpui_ce_platform::application()
        .with_assets(ui::Assets)
        .run(move |cx: &mut App| {
            yororen_ui::renderer::install(cx, cx.window_appearance());
            yororen_ui::locale_zh_cn::install(cx);
            yororen_ui::headless::text_input::init(cx);
            let controller = hole_desktop::controller::Controller::start(
                storage,
                config.clone(),
                secrets.clone(),
                run_state,
                error,
            );
            cx.spawn(async move |cx| {
                cx.update(move |cx| {
                    let options = WindowOptions {
                        window_bounds: Some(WindowBounds::Windowed(Bounds::centered(
                            None,
                            size(px(1240.), px(820.)),
                            cx,
                        ))),
                        window_min_size: Some(size(px(780.), px(540.))),
                        titlebar: Some(TitlebarOptions {
                            title: Some("Hole".into()),
                            ..Default::default()
                        }),
                        app_id: Some("dev.hole.desktop".into()),
                        ..Default::default()
                    };
                    let options = WindowOptions {
                        icon: Some(std::sync::Arc::new(
                            image::load_from_memory(ui::APP_ICON)
                                .expect("应用图标无效")
                                .into_rgba8(),
                        )),
                        ..options
                    };
                    cx.open_window(options, move |window, cx| {
                        cx.new(|cx| app::Desktop::new(controller, config, secrets, window, cx))
                    })
                    .expect("创建桌面窗口失败");
                    cx.on_window_closed(|cx, _| {
                        if cx.windows().is_empty() {
                            cx.quit();
                        }
                    })
                    .detach();
                    cx.activate(true);
                });
            })
            .detach();
        });
}
