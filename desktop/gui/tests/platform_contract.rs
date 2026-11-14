//! Execute the actual PenMods form serializer against the desktop requests.
use hole_desktop::config::{self, Secrets, StoredConfig};
use serde_json::json;
use std::{
    io::Write,
    process::{Command, Stdio},
};

#[test]
fn active_transport_requests_match_penmods() {
    let mut cases = Vec::new();
    for mode in ["auto", "ice", "legacy"] {
        for turn in ["worker", "manual", "off"] {
            if mode == "legacy" && turn != "off" {
                continue;
            }
            for nat in [
                None,
                Some(vec![]),
                Some(vec!["stun:127.0.0.1:3478".to_string()]),
            ] {
                let mut config = StoredConfig::default();
                let c = &mut config.connection;
                c.server_url = "wss://fixture.invalid/ws".into();
                c.room = "room".into();
                c.device_name = "desktop".into();
                c.connection_mode = mode.into();
                c.turn.mode = turn.into();
                c.ice.nat_stun_urls = nat;
                c.relay.device = "relay".into();
                c.relay.order = Some(vec!["hole".into(), "tls".into(), "udp".into()]);
                if mode != "legacy" {
                    c.ice.relay_only = true;
                    c.ice.include_loopback = true;
                    c.ice.interface_allowlist = vec!["lo".into()];
                    c.ice.direct_probe_timeout = "4s".into();
                }
                if turn == "manual" {
                    c.turn.urls = vec!["turn:fixture.invalid:3478?transport=udp".into()];
                    c.turn.username = "user".into();
                }
                let secrets = Secrets {
                    password: " secret  ".into(),
                    token: " token ".into(),
                    turn_credential: " credential ".into(),
                };
                let request = config::to_run_request(&config, &secrets).unwrap();
                cases.push(json!({"config":config,"secrets":secrets,"request":request}));
            }
        }
    }
    let mut child = Command::new("node")
        .arg("tests/platform_contract.mjs")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("跨端配置契约测试需要 Node.js");
    child
        .stdin
        .take()
        .unwrap()
        .write_all(serde_json::to_string(&cases).unwrap().as_bytes())
        .unwrap();
    let result = child.wait_with_output().unwrap();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}
