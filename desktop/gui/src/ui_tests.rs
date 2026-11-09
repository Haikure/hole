//! Exercise real Slint route transitions and verify routing never invokes mutations.
use crate::{App, MainWindow, Theme};
use slint::{ComponentHandle, LogicalSize, Model};
use slint::platform::{Platform, PlatformError, WindowAdapter};
use slint::platform::software_renderer::{MinimalSoftwareWindow, RepaintBufferType};
use std::rc::Rc;

struct TestPlatform;
static CLOCK_MS: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
impl Platform for TestPlatform {
    fn duration_since_start(&self) -> std::time::Duration {
        std::time::Duration::from_millis(CLOCK_MS.load(std::sync::atomic::Ordering::Relaxed))
    }
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
    exercise_saved_forms_and_session_state(&window);
}

fn exercise_saved_forms_and_session_state(window: &MainWindow) {
    use crate::{config::StoredConfig, controller::{Controller, Shared}, storage::Storage};
    use std::sync::{Arc, Mutex};
    let dir = std::env::temp_dir().join(format!("hole-ui-{}", uuid::Uuid::new_v4()));
    std::fs::create_dir_all(&dir).unwrap();
    let shared = Arc::new(Mutex::new(Shared {
        storage: Storage { dir: dir.clone() }, config: StoredConfig::default(),
        secrets: Default::default(), run_state: Default::default(), config_error: None,
        undo: None, pending_import: None,
    }));
    let controller = Rc::new(Controller::start(window, shared.clone()));
    crate::bind_callbacks(window, controller.clone(), shared.clone());
    crate::view_state::bind(window);
    crate::sync_config_forms(window, &shared);
    window.invoke_reset_forms();
    window.invoke_open_page("settings".into());
    for room in [" first ", " second "] {
        let mut draft = window.get_connection_draft();
        draft.server_url = " wss://example.invalid/ws ".into();
        draft.room = room.into();
        draft.session_timeout = " 10m ".into();
        draft.candidate_interfaces = " eth0, wlan0, eth0 ".into();
        draft.password = " secret with spaces ".into();
        draft.token = " token with spaces ".into();
        window.set_connection_draft(draft);
        assert!(window.invoke_page_dirty());
        window.invoke_save_connection_draft();
        assert!(!window.invoke_page_dirty(), "normalized save left dirty state");
        assert_eq!(window.get_connection_draft().room, room.trim());
        assert_eq!(window.get_connection_draft().password, " secret with spaces ");
        assert_eq!(shared.lock().unwrap().storage.load_secrets().token, " token with spaces ");
        window.invoke_open_page("overview".into());
        assert_eq!(window.get_page(), "overview", "saved form blocked navigation");
        window.invoke_open_page("settings".into());
    }
    let mut draft = window.get_connection_draft();
    draft.room = "keep-editing".into();
    window.set_connection_draft(draft);
    let app = window.global::<App>();
    assert!(app.get_connection_ready(), "saved valid configuration must be startable before core initialization");
    app.invoke_save_prefs(app.get_prefs());
    assert_eq!(window.get_connection_draft().room, "keep-editing");
    assert!(window.invoke_page_dirty());
    window.invoke_reset_forms();
    assert!(!window.invoke_page_dirty());
    // A storage error must leave both the draft and saved baseline intact.
    let saved_room = app.get_connection().room;
    let mut draft = window.get_connection_draft();
    draft.room = "unsaved-on-disk".into();
    window.set_connection_draft(draft);
    shared.lock().unwrap().storage.dir = dir.join("missing-parent");
    window.invoke_save_connection_draft();
    assert!(window.invoke_page_dirty());
    assert_eq!(app.get_connection().room, saved_room);
    assert_eq!(window.get_connection_draft().room, "unsaved-on-disk");
    shared.lock().unwrap().storage.dir = dir.clone();
    window.invoke_reset_forms();
    let mut draft = window.get_connection_draft();
    draft.session_timeout = "invalid".into();
    window.set_connection_draft(draft);
    window.invoke_save_connection_draft();
    assert!(window.invoke_page_dirty());
    assert_eq!(window.get_connection_draft().session_timeout, "invalid");
    window.invoke_reset_forms();
    window.invoke_open_page("transport".into());
    let mut draft = window.get_transport_draft();
    draft.stun_urls = " stun:example.invalid:3478 \n\nstun:example.invalid:3478 ".into();
    draft.turn_credential = " credential with spaces ".into();
    window.set_transport_draft(draft);
    window.invoke_save_transport_draft();
    assert!(!window.invoke_page_dirty());
    assert_eq!(window.get_transport_draft().turn_credential, " credential with spaces ");
    window.invoke_open_page("overview".into());
    assert_eq!(window.get_page(), "overview");

    let peers = |ids: &[&str]| ids.iter().map(|id| crate::PeerRow { peer_id: (*id).into(), ..Default::default() }).collect();
    crate::view_state::update(window, vec![], vec![], vec![], peers(&["a", "b"]));
    app.set_selected_peer("b".into());
    app.invoke_set_expanded("peer:b".into(), true);
    for ids in [["b", "a"], ["a", "b"]] {
        crate::view_state::update(window, vec![], vec![], vec![], peers(&ids));
        assert_eq!(app.get_selected_peer(), "b");
        assert!(app.invoke_is_expanded("peer:b".into()));
    }
    window.invoke_open_page("devices".into());
    window.invoke_open_page("overview".into());
    assert!(app.invoke_is_expanded("peer:b".into()));
    crate::view_state::update(window, vec![], vec![], vec![], peers(&["a"]));
    assert_eq!(app.get_selected_peer(), "a");
    assert!(!app.invoke_is_expanded("peer:b".into()));
    crate::show_toast(window, "same", "info", false);
    let sequence = app.get_toast_sequence();
    CLOCK_MS.store(3000, std::sync::atomic::Ordering::Relaxed);
    slint::platform::update_timers_and_animations();
    crate::show_toast(window, "same", "info", false);
    assert_ne!(sequence, app.get_toast_sequence());
    CLOCK_MS.store(4500, std::sync::atomic::Ordering::Relaxed);
    slint::platform::update_timers_and_animations();
    assert_eq!(app.get_toast(), "same", "old timer dismissed a newer identical toast");
    CLOCK_MS.store(7500, std::sync::atomic::Ordering::Relaxed);
    slint::platform::update_timers_and_animations();
    assert_eq!(app.get_toast(), "");
    controller.finish();
    std::fs::remove_dir_all(dir).unwrap();
}
