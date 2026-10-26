//! Exercise real Slint route transitions and verify routing never invokes mutations.
use crate::{App, MainWindow, Theme};
use slint::{ComponentHandle, LogicalSize, Model};
use slint::platform::{Platform, PlatformError, WindowAdapter};
use slint::platform::software_renderer::{MinimalSoftwareWindow, RepaintBufferType};
use std::rc::Rc;

struct TestPlatform;
impl Platform for TestPlatform {
    fn create_window_adapter(&self) -> Result<Rc<dyn WindowAdapter>, PlatformError> {
        Ok(MinimalSoftwareWindow::new(RepaintBufferType::NewBuffer))
    }
}

#[test]
fn route_changes_and_reduced_motion_do_not_submit_commands() {
    slint::platform::set_platform(Box::new(TestPlatform)).unwrap();
    let window = MainWindow::new().unwrap();
    window.global::<Theme>().set_reduce_motion(true);
    window.window().set_size(LogicalSize::new(1180., 780.));
    let navigation = std::rc::Rc::new(std::cell::Cell::new(0));
    let count = navigation.clone();
    window.on_animate_navigation(move |_, _, _| count.set(count.get() + 1));
    let commands = std::rc::Rc::new(std::cell::Cell::new(0));
    let count = commands.clone();
    window.global::<App>().on_toggle_run(move |_| count.set(count.get() + 1));
    let count = commands.clone();
    window.global::<App>().on_set_voice_enabled(move |_| count.set(count.get() + 1));
    let count = commands.clone();
    window.global::<App>().on_save_connection(move |_| { count.set(count.get() + 1); "".into() });
    for route in ["settings", "transport", "appearance", "behavior", "exchange", "devices", "runtime", "diagnostics", "voice", "mappings", "overview"] {
        window.invoke_open_page(route.into());
        assert_eq!(window.get_page().as_str(), route);
    }
    assert_eq!(navigation.get(), 11);
    assert_eq!(commands.get(), 0, "routing or animation submitted a mutation");
    crate::visual_fixtures::apply(&window, "fixed");
    let peer = window.global::<App>().get_peers().row_data(0).unwrap();
    assert_eq!(peer.path_label, "固定入口直连 · IPv6");
    assert_eq!(peer.state_label, "已连接");
    assert_eq!(commands.get(), 0, "snapshot rendering submitted a mutation");
}
