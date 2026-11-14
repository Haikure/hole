#!/usr/bin/env python3
"""Deterministic stdio fixture for UI checks; never opens network or audio."""
import copy
import json
import os
from pathlib import Path
import sys

root = Path(os.environ["HOLE_DESKTOP_CONFIG_DIR"])
snapshot = {
    "api_version": 1, "configured": True, "run_requested": False, "engine_state": "stopped", "signal_state": "stopped",
    "generation": "0", "transport_generation": "0", "core_version": "fixture", "mappings": [],
    "peer_transports": [{"transport_id": "peer-fixture", "peer_id": "Android 手机", "state": "active", "profile": "ice-quic-mux-v1", "phase": "ice",
        "path_type": "direct", "rtt_ms": "18", "local_type": "host", "remote_type": "srflx", "local_address": "192.0.2.2:4000",
        "remote_address": "198.51.100.2:5000", "active_channels": 1, "mapping_count": 1}],
    "nat_diagnostic": {"state": "complete", "title": "端口受限锥形 NAT", "summary": "固定测试数据，不代表真实网络检测结果。",
        "highlights": [{"label": "映射", "value": "端点无关"}, {"label": "过滤", "value": "地址与端口相关"}, {"label": "端口", "value": "稳定"}],
        "sections": [{"title": "采样结果", "fields": [{"label": "公网映射", "value": "198.51.100.2:5000"}, {"label": "采样时间", "value": "2026-11-14T00:00:00Z"}]}]},
    "voice": {"enabled": False, "state": "off"},
}
events = [{"sequence": str(i), "generation": "3", "elapsed_ms": str(i * 10), "round": 1, "trial": 1,
    "kind": "probe_sent", "title": "发送 PROBE", "stage": "udp", "direction": "out", "packet": "PROBE", "socket_id": "udp-1",
    "local_address": "192.0.2.2:4000", "remote_address": "198.51.100.2:5000", "validation": "sent", "bytes": 64} for i in range(2, 300)]
events.insert(0, {"sequence": "1", "generation": "3", "elapsed_ms": "0", "stage": "ice", "kind": "stage", "title": "开始 ICE 候选检查"})
events.append({"sequence": "300", "generation": "3", "elapsed_ms": "3000", "stage": "connected", "kind": "ack_verified", "title": "ACK 回显验证成功",
    "socket_id": "udp-1", "round": 1, "trial": 1, "remote_address": "198.51.100.2:5000", "validation": "echo_verified"})
run = {"generation": "3", "phase": "prediction", "state": "active", "stage": "connected", "started_at": "2026-11-14T00:00:00Z", "elapsed_ms": "3000",
    "rounds": [{"round": 1, "trial": 1, "mode": "prediction", "targets": ["198.51.100.2:5000", "198.51.100.2:5001"],
        "local_model": {"type": "restricted_cone"}, "peer_model": {"type": "symmetric"},
        "local_plan": {"ip": "203.0.113.2", "port": 4000, "target": 4000, "width": 1, "sockets": 2},
        "peer_plan": {"ip": "198.51.100.2", "port": 4999, "target": 5000, "width": 2, "sockets": 4}}],
    "sockets": [{"id": "udp-1", "round": 1, "trial": 1, "role": "dialer", "bound_address": "0.0.0.0:4000", "local_address": "192.0.2.2:4000",
        "mapped_address": "198.51.100.2:4000", "target": "198.51.100.2:5000", "state": "selected"}]}

for line in sys.stdin:
    query = json.loads(line)
    method, params = query["method"], query.get("params") or {}
    with (root / "rpc.jsonl").open("a") as log:
        log.write(json.dumps(query) + "\n")
    result = {}
    if method == "hello":
        result = {"bridge_version": 1, "api_version": 1, "methods": ["snapshot", "nat_punch_process"]}
    elif method in ("start", "stop"):
        running = method == "start"
        snapshot.update(run_requested=running, engine_state="running" if running else "stopped", signal_state="joined" if running else "stopped", generation="1")
    elif method == "snapshot":
        result = {"snapshot": snapshot}
    elif method == "nat_punch_process":
        after, limit = int(params.get("after_sequence") or 0), params.get("limit", 256)
        selected = [event for event in events if int(event["sequence"]) > after]
        page = selected[:limit]
        result = {"selected_generation": "3", "stream": params.get("stream", "connection"), "attempts": [{key: run[key] for key in ("generation", "phase", "state", "stage", "started_at", "elapsed_ms")}],
            "has_discovery": False, "more": len(selected) > limit,
            "nat_punch_process": {"transport_id": "peer-fixture", "sequence": page[-1]["sequence"] if len(selected) > limit else "300", "events_dropped": "0", "events_evicted": "0", "attempts": [copy.deepcopy(run)], "events": page}}
    print(json.dumps({"jsonrpc": "2.0", "id": query["id"], "result": result}), flush=True)
    if method == "shutdown":
        break
