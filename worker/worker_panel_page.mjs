// Self-contained UI: no remote fonts, scripts, images, or credentials in storage.
export const PANEL_PAGE = String.raw`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<title>Hole · 房间面板</title>
<style>
:root {
  color-scheme: light;
  --bg: #f3f2ee; --surface: #fff; --soft: #f5f5f2; --field: #f0f1ed;
  --ink: #272f31; --muted: #737b7a; --subtle: #959c96;
  --green: #28755b; --green-soft: #e9f3ec; --orange: #c25a29; --orange-soft: #fff0e5;
  --red: #b74949; --red-soft: #fbecec; --line: #8aafa0; --signal: #d69a74; --pending: #929ba2;
  --dot: #d6dad1; --shadow: 0 12px 40px -20px #303d3238, 0 2px 8px #303d3205;
  --lift: 0 14px 32px -12px #34493f35, 0 3px 9px #34493f08;
  --focus: 0 6px 22px -4px #527e6966; --easing: cubic-bezier(.22,.8,.25,1);
  font-family: "Geist", "SF Pro Display", "Noto Sans SC", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-synthesis: none; text-rendering: optimizeLegibility;
}
:root[data-theme="dark"] {
  color-scheme: dark;
  --bg: #191d1e; --surface: #242a2b; --soft: #2b3132; --field: #303738;
  --ink: #eeefea; --muted: #a4ada9; --subtle: #778480;
  --green: #a5d7b8; --green-soft: #303f37; --orange: #f1b286; --orange-soft: #44372f;
  --red: #f0a39e; --red-soft: #493333; --line: #709f89; --signal: #ac825f; --pending: #89969c;
  --dot: #3c4542; --shadow: 0 16px 46px -20px #0008, 0 2px 8px #0001;
  --lift: 0 16px 32px -10px #0007, 0 3px 10px #0002; --focus: 0 6px 22px -4px #9ad9b466;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    color-scheme: dark;
    --bg: #191d1e; --surface: #242a2b; --soft: #2b3132; --field: #303738;
    --ink: #eeefea; --muted: #a4ada9; --subtle: #778480;
    --green: #a5d7b8; --green-soft: #303f37; --orange: #f1b286; --orange-soft: #44372f;
    --red: #f0a39e; --red-soft: #493333; --line: #709f89; --signal: #ac825f; --pending: #89969c;
    --dot: #3c4542; --shadow: 0 16px 46px -20px #0008, 0 2px 8px #0001;
    --lift: 0 16px 32px -10px #0007, 0 3px 10px #0002; --focus: 0 6px 22px -4px #9ad9b466;
  }
}
* { box-sizing: border-box; }
[hidden] { display: none !important; }
html { min-width: 320px; background: var(--bg); }
body { margin: 0; color: var(--ink); background: var(--bg); min-height: 100vh; min-height: 100dvh; }
button, input { font: inherit; outline: none; }
button { border: 0; cursor: pointer; color: inherit; -webkit-tap-highlight-color: transparent; }
button:disabled { cursor: wait; opacity: .6; }
button:focus-visible, input:focus-visible { box-shadow: var(--focus); }
svg { display: block; flex-shrink: 0; }
.icon { width: 22px; height: 22px; fill: none; stroke: currentColor; stroke-width: 1.65; stroke-linecap: round; stroke-linejoin: round; }
.sprite { position: absolute; width: 0; height: 0; overflow: hidden; }
.app { width: min(1520px, 100%); margin: auto; padding: 0 40px; }
.topbar { min-height: 100px; display: flex; align-items: center; justify-content: space-between; gap: 24px; }
.brand { display: flex; align-items: center; gap: 12px; }
.brand-mark { display: grid; place-items: center; flex: 0 0 auto; width: 44px; height: 44px; background: #fff; box-shadow: var(--shadow); border-radius: 15px; }
.brand-emoji { display: grid; place-items: center; width: 28px; height: 28px; font-family: "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", emoji; font-size: 25px; line-height: 1; transform: translateX(1px); }
.brand-emoji canvas { display: block; width: 28px; height: 28px; }
.brand-name { font-size: 23px; letter-spacing: -.9px; font-weight: 750; }
.brand-caption { margin-left: 10px; padding-left: 16px; color: var(--muted); font-size: 12px; }
.top-actions { display: flex; align-items: center; gap: 9px; }
.connection-state { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--muted); margin-right: 15px; }
.status-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--subtle); flex-shrink: 0; }
.connection-state[data-kind="live"] .status-dot { background: var(--green); box-shadow: 0 0 0 4px var(--green-soft); }
.connection-state[data-kind="error"] .status-dot { background: var(--red); }
.connection-state[data-kind="busy"] .status-dot { background: var(--orange); animation: breathe 1.5s ease-in-out infinite; }
.icon-button { display: grid; place-items: center; width: 42px; height: 42px; border-radius: 50%; background: var(--surface); box-shadow: var(--shadow); transition: transform .2s, background .2s; }
.icon-button:hover { transform: translateY(-2px); background: var(--soft); }
.icon-button .icon { width: 19px; height: 19px; }
.text-button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; padding: 11px 15px; border-radius: 12px; background: transparent; color: var(--muted); font-size: 12px; transition: color .2s, background .2s; }
.text-button:hover { color: var(--ink); background: var(--soft); }
.text-button .icon { width: 17px; height: 17px; }
.auth-layout { min-height: calc(100dvh - 175px); max-width: 1050px; margin: auto; display: grid; grid-template-columns: 1.1fr 1fr; align-items: center; gap: 90px; padding: 42px 0 72px; }
.auth-story { align-self: center; }
.mini-label { display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: 12px; }
.mini-label .icon { width: 17px; height: 17px; color: var(--green); }
.auth-story h1 { margin: 20px 0 17px; font-size: clamp(30px, 3.5vw, 43px); line-height: 1.35; font-weight: 650; letter-spacing: -1.7px; }
.auth-story > p { color: var(--muted); font-size: 14px; line-height: 1.9; margin: 0; max-width: 340px; }
.auth-art { height: 255px; position: relative; margin: 36px 0 10px; max-width: 400px; }
.auth-art::before { content: ""; position: absolute; inset: 15px 38px; background: radial-gradient(ellipse, var(--green-soft), transparent 70%); }
.auth-wires { position: absolute; inset: 0; width: 100%; height: 100%; overflow: visible; fill: none; stroke: var(--signal); stroke-width: 1.5; stroke-dasharray: 4 6; }
.art-cloud { position: absolute; left: 50%; top: 26px; transform: translateX(-50%) rotate(-4deg); background: var(--surface); box-shadow: var(--lift); border-radius: 23px; padding: 22px 26px 18px; display: grid; justify-items: center; gap: 8px; }
.cloud-icon { width: 57px; height: auto; aspect-ratio: 49 / 22; fill: none; stroke: none; }
.art-cloud span { font-size: 9px; letter-spacing: 1.4px; color: var(--muted); font-weight: 650; }
.art-device { position: absolute; top: 177px; width: 61px; height: 61px; border-radius: 20px; box-shadow: var(--lift); background: var(--surface); display: grid; place-items: center; color: var(--muted); }
.art-device:nth-last-child(2) { left: 18%; transform: rotate(-8deg); }
.art-device:last-child { right: 17%; transform: rotate(8deg); }
.art-device .icon { width: 27px; height: 27px; }
.story-note { display: flex; align-items: center; gap: 7px; color: var(--muted); font-size: 11px; }
.story-note .icon { width: 14px; height: 14px; }
.auth-card { background: var(--surface); padding: 37px; border-radius: 28px; box-shadow: var(--shadow); }
.steps { display: flex; align-items: center; gap: 12px; color: var(--subtle); font-size: 11px; margin-bottom: 34px; }
.step { display: flex; align-items: center; gap: 7px; white-space: nowrap; }
.step-number { display: grid; place-items: center; width: 23px; height: 23px; border-radius: 50%; background: var(--soft); font-size: 10px; font-weight: 650; }
.step.active { color: var(--ink); }
.step.active .step-number { color: var(--surface); background: var(--ink); }
.step.done .step-number { background: var(--green-soft); color: var(--green); }
.step.done .icon { width: 13px; height: 13px; }
.step-bridge { height: 2px; flex: 1; border-radius: 2px; background: var(--field); }
.form-symbol { display: grid; place-items: center; width: 48px; height: 48px; color: var(--green); background: var(--green-soft); border-radius: 16px; margin-bottom: 20px; }
.auth-card h2 { margin: 0 0 9px; font-size: 23px; font-weight: 650; letter-spacing: -.6px; }
.form-caption { margin: 0 0 29px; color: var(--muted); line-height: 1.8; font-size: 12px; }
.field-label { display: block; font-size: 12px; font-weight: 600; margin: 21px 0 9px; }
.input-wrap { display: flex; align-items: center; border-radius: 13px; background: var(--field); transition: box-shadow .2s; }
.input-wrap:focus-within { background: var(--green-soft); box-shadow: var(--focus); }
.input-wrap > .icon { width: 18px; height: 18px; margin-left: 16px; color: var(--subtle); }
.input-wrap input { border: 0; min-width: 0; width: 100%; height: 51px; background: transparent; color: var(--ink); padding: 14px 13px; font-size: 16px; border-radius: 13px; box-shadow: none; }
.input-wrap input::-ms-reveal { display: none; }
input::placeholder { color: var(--subtle); font-size: 12px; }
.password-toggle { display: grid; place-items: center; background: transparent; color: var(--muted); width: 44px; height: 44px; flex-shrink: 0; border-radius: 11px; margin-right: 4px; }
.password-toggle .icon { width: 18px; height: 18px; }
.primary-button { display: flex; align-items: center; justify-content: space-between; width: 100%; gap: 12px; margin-top: 27px; padding: 16px 18px; border-radius: 13px; font-size: 13px; font-weight: 600; background: var(--ink); color: var(--surface); box-shadow: 0 6px 16px -9px #0009; transition: transform .2s, opacity .2s; }
.primary-button:hover { transform: translateY(-2px); opacity: .9; }
.primary-button .icon { width: 18px; height: 18px; }
.form-error { color: var(--red); font-size: 12px; line-height: 1.7; margin: 12px 0 0; }
.form-error:empty { display: none; }
.form-foot { display: flex; align-items: center; gap: 6px; color: var(--muted); font-size: 10px; margin: 20px 0 0; line-height: 1.7; }
.form-foot .icon { width: 13px; height: 13px; }
.back-button { margin: 18px 0 -10px -10px; }
.dashboard { padding: 26px 0 22px; }
.room-heading { display: flex; align-items: center; justify-content: space-between; gap: 24px; margin-bottom: 27px; }
.room-title-wrap { display: flex; align-items: center; gap: 16px; min-width: 0; }
.room-symbol { background: var(--surface); box-shadow: var(--shadow); width: 53px; height: 53px; border-radius: 19px; display: grid; place-items: center; color: var(--green); flex-shrink: 0; }
.room-title { min-width: 0; }
.room-title h1 { margin: 0; font-size: 28px; font-weight: 650; letter-spacing: -.8px; overflow-wrap: anywhere; }
.room-title p { color: var(--muted); font-size: 12px; line-height: 1.7; margin: 6px 0 0; }
.room-switch { background: var(--surface); box-shadow: var(--shadow); white-space: nowrap; }
.workspace { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 22px; align-items: start; }
.map-card { background: var(--surface); border-radius: 28px; box-shadow: var(--shadow); overflow: hidden; min-width: 0; position: relative; }
.map-toolbar { display: flex; justify-content: space-between; align-items: center; gap: 16px; padding: 23px 25px 14px; }
.map-title { font-size: 13px; font-weight: 650; display: flex; align-items: center; gap: 9px; }
.map-title .icon { color: var(--muted); width: 17px; height: 17px; }
.layer-tabs { display: flex; gap: 2px; background: var(--soft); border-radius: 11px; padding: 4px; }
.layer-tabs button { background: transparent; color: var(--muted); padding: 6px 10px; font-size: 10px; border-radius: 8px; white-space: nowrap; transition: background .2s, color .2s, box-shadow .2s; }
.layer-tabs button[aria-pressed="true"] { background: var(--surface); color: var(--ink); box-shadow: 0 2px 5px #0000000b; }
.map-viewport { height: min(65vh, 640px); min-height: 500px; overflow: hidden; position: relative; touch-action: none; cursor: grab; background-image: radial-gradient(circle, var(--dot) .75px, transparent .85px); background-size: 20px 20px; }
.map-viewport.dragging { cursor: grabbing; }
.scene { position: absolute; left: 0; top: 0; transform-origin: 0 0; will-change: transform; }
.map-card[data-stale="true"] .scene { opacity: .55; }
.edge-layer { position: absolute; inset: 0; overflow: visible; }
.edge { cursor: pointer; outline: none; transition: opacity .2s; }
.edge-line { fill: none; stroke: var(--line); stroke-width: 2.2; stroke-linecap: round; vector-effect: non-scaling-stroke; transition: stroke .2s, stroke-width .2s; }
.edge.signal .edge-line { stroke: var(--signal); stroke-width: 1.6; stroke-dasharray: 4 7; }
.edge.pending .edge-line { stroke: var(--pending); stroke-dasharray: 8 6; }
.edge.blocked .edge-line { stroke: var(--red); stroke-dasharray: 3 6; }
.edge.turn:not(.pending):not(.blocked) .edge-line { stroke: #5d83c3; }
.edge.hole:not(.pending):not(.blocked) .edge-line { stroke: #8265b7; }
.edge.peer-relay:not(.pending):not(.blocked) .edge-line { stroke: #269b9b; }
.edge-hit { fill: none; stroke: transparent; stroke-width: 19px; vector-effect: non-scaling-stroke; }
.edge:hover .edge-line, .edge:focus-visible .edge-line, .edge.selected .edge-line { stroke-width: 3.5; }
.edge.dim { opacity: .16; }
.edge.layer-hidden { display: none; }
.graph-node { position: absolute; width: 166px; min-height: 88px; margin: 0; padding: 15px; text-align: left; background: var(--surface); border-radius: 19px; box-shadow: var(--lift); transform: translate(-50%, -50%); user-select: none; transition: opacity .2s, box-shadow .2s; }
.graph-node:hover { box-shadow: 0 18px 36px -9px #243c3a40; z-index: 2; }
:root[data-theme="dark"] .graph-node:hover { box-shadow: 0 18px 36px -9px #0009; }
.graph-node.selected { background: var(--green-soft); box-shadow: var(--lift); z-index: 3; }
.graph-node:focus-visible { background: var(--green-soft); box-shadow: var(--focus), var(--lift); }
.graph-node.dim { opacity: .42; }
.relay-node { border: 1px dashed var(--orange); }
.relay-node .device-icon { color: var(--orange); background: var(--orange-soft); }
.relay-node .node-status .status-dot { background: var(--orange); }
.relay-node.relay-turn { border-color: #5d83c3; }
.relay-node.relay-turn .device-icon { color: #5d83c3; background: #5d83c31c; }
.relay-node.relay-turn .node-status .status-dot { background: #5d83c3; }
.relay-node.relay-hole { border-color: #8265b7; }
.relay-node.relay-hole .device-icon { color: #8265b7; background: #8265b71c; }
.relay-node.relay-hole .node-status .status-dot { background: #8265b7; }
.relay-node.relay-peer_relay { border-color: #269b9b; }
.relay-node.relay-peer_relay .device-icon { color: #269b9b; background: #269b9b1c; }
.relay-node.relay-peer_relay .node-status .status-dot { background: #269b9b; }
.node-head { display: flex; align-items: center; gap: 10px; }
.device-icon { width: 33px; height: 33px; border-radius: 11px; color: var(--green); background: var(--green-soft); display: grid; place-items: center; flex-shrink: 0; }
.device-icon .icon { width: 20px; height: 20px; }
.node-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; font-weight: 650; }
.node-status { display: flex; align-items: center; gap: 5px; margin-top: 3px; font-size: 9px; color: var(--muted); white-space: nowrap; }
.node-status .status-dot { width: 4px; height: 4px; background: var(--green); }
.node-status.recovering .status-dot { background: var(--orange); }
.node-status.delegated .status-dot { background: var(--subtle); }
.node-foot { display: flex; align-items: center; justify-content: space-between; gap: 4px; margin-top: 10px; color: var(--muted); font-size: 9px; }
.node-foot span { min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.node-foot .icon { width: 12px; height: 12px; color: var(--green); }
.cloud-node { width: 176px; padding: 20px 15px 17px; text-align: center; border-radius: 24px; }
.cloud-node .cloud-icon { width: 66px; margin: 0 auto 10px; }
.cloud-name { font-size: 12px; font-weight: 650; }
.cloud-caption { font-size: 10px; color: var(--muted); margin-top: 6px; }
.cloud-node.selected { background: var(--orange-soft); box-shadow: var(--lift); }
.map-empty { position: absolute; bottom: 62px; left: 24px; right: 24px; text-align: center; pointer-events: none; }
.map-empty h3 { font-size: 15px; font-weight: 600; margin: 0 0 8px; }
.map-empty p { font-size: 11px; color: var(--muted); line-height: 1.8; margin: auto; max-width: 300px; }
.map-bottom { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 15px 24px 21px; }
.legend { display: flex; gap: 18px; font-size: 10px; color: var(--muted); flex-wrap: wrap; }
.legend span { display: flex; align-items: center; gap: 6px; }
.legend-line { display: inline-block; width: 16px; height: 2px; background: var(--line); border-radius: 3px; }
.legend-line.signal { background: repeating-linear-gradient(90deg, var(--signal) 0 3px, transparent 3px 6px); }
.legend-line.pending { background: repeating-linear-gradient(90deg, var(--pending) 0 6px, transparent 6px 10px); }
.legend-line.blocked { background: repeating-linear-gradient(90deg, var(--red) 0 2px, transparent 2px 5px); }
.legend-line.turn { background: #5d83c3; }
.legend-line.hole { background: #8265b7; }
.legend-line.peer-relay { background: #269b9b; }
.map-hint { color: var(--subtle); font-size: 10px; white-space: nowrap; }
.zoom-controls { position: absolute; bottom: 16px; left: 18px; display: flex; align-items: center; padding: 4px; gap: 1px; background: var(--surface); border-radius: 12px; box-shadow: var(--lift); z-index: 4; }
.zoom-controls button { width: 33px; height: 33px; background: transparent; display: grid; place-items: center; border-radius: 9px; color: var(--muted); }
.zoom-controls button:hover { background: var(--soft); color: var(--ink); }
.zoom-controls .icon { width: 16px; height: 16px; }
.zoom-level { min-width: 39px; color: var(--muted); font-size: 9px; text-align: center; font-variant-numeric: tabular-nums; }
.map-tooltip { position: absolute; pointer-events: none; z-index: 8; max-width: 230px; border-radius: 13px; padding: 12px 14px; background: var(--ink); color: var(--surface); box-shadow: var(--lift); font-size: 11px; line-height: 1.7; white-space: pre-line; }
.inspector { min-width: 0; background: var(--surface); box-shadow: var(--shadow); border-radius: 26px; padding: 25px; }
.inspector-header { display: flex; justify-content: space-between; gap: 12px; align-items: center; margin-bottom: 24px; }
.inspector-header h2 { font-size: 12px; margin: 0; font-weight: 600; color: var(--muted); }
.close-details { width: 29px; height: 29px; box-shadow: none; background: var(--soft); }
.close-details .icon { width: 15px; height: 15px; }
.detail-symbol { width: 50px; height: 50px; border-radius: 17px; background: var(--green-soft); color: var(--green); display: grid; place-items: center; margin-bottom: 18px; }
.detail-symbol.cloud { background: var(--orange-soft); }
.detail-symbol .cloud-icon { width: 41px; }
.detail-symbol .icon { width: 25px; height: 25px; }
.detail-title { font-size: 21px; font-weight: 600; letter-spacing: -.5px; margin: 0 0 8px; overflow-wrap: anywhere; }
.detail-caption { color: var(--muted); font-size: 11px; line-height: 1.8; margin: 0 0 22px; overflow-wrap: anywhere; }
.detail-badge { display: inline-flex; align-items: center; gap: 6px; font-size: 10px; background: var(--green-soft); color: var(--green); border-radius: 7px; padding: 6px 9px; margin-bottom: 20px; }
.detail-badge.pending { color: var(--orange); background: var(--orange-soft); }
.detail-badge.blocked { color: var(--red); background: var(--red-soft); }
.detail-badge .status-dot { width: 4px; height: 4px; background: currentColor; }
.detail-rows { display: flex; flex-direction: column; gap: 15px; margin: 0; }
.detail-row { display: flex; align-items: baseline; justify-content: space-between; gap: 18px; font-size: 11px; line-height: 1.7; }
.detail-row dt { color: var(--muted); flex-shrink: 0; }
.detail-row dd { text-align: right; margin: 0; overflow-wrap: anywhere; min-width: 0; }
.detail-heading { display: flex; justify-content: space-between; color: var(--muted); font-size: 10px; font-weight: 550; margin: 27px 0 10px; }
.peer-button { display: flex; align-items: center; gap: 9px; width: 100%; padding: 10px; margin-top: 5px; background: var(--soft); border-radius: 11px; text-align: left; }
.peer-button > .icon { width: 16px; height: 16px; color: var(--green); }
.peer-button span { min-width: 0; flex: 1; font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.peer-button small { color: var(--muted); font-size: 9px; }
.peer-button:hover { background: var(--green-soft); }
.detail-note { margin: 22px 0 0; background: var(--soft); padding: 13px; border-radius: 13px; font-size: 10px; color: var(--muted); line-height: 1.85; }
.update-feed { margin-top: 25px; }
.update-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: 10px; color: var(--muted); margin-bottom: 14px; }
.update-heading .icon { width: 14px; height: 14px; }
.feed-event { display: flex; gap: 10px; align-items: baseline; margin: 12px 0; font-size: 10px; color: var(--muted); line-height: 1.7; }
.feed-event .status-dot { width: 4px; height: 4px; background: var(--green); position: relative; top: -1px; }
.feed-event time { margin-left: auto; color: var(--subtle); font-size: 9px; flex-shrink: 0; }
.feed-event span { overflow-wrap: anywhere; }
.panel-note { display: flex; align-items: flex-start; gap: 8px; color: var(--muted); font-size: 10px; line-height: 1.9; margin: 19px 4px 0; }
.panel-note .icon { margin-top: 3px; width: 13px; height: 13px; }
.footer { display: flex; align-items: center; justify-content: space-between; gap: 20px; padding: 25px 0 27px; color: var(--subtle); font-size: 10px; }
.footer-brand { letter-spacing: 2px; font-weight: 650; }
.detail-backdrop { position: fixed; inset: 0; background: #101d2440; backdrop-filter: blur(3px); z-index: 19; }
@keyframes breathe { 50% { opacity: .35; } }
@media (min-width: 1500px) { .map-viewport { height: 650px; } }
@media (max-width: 1100px) {
  .app { padding: 0 26px; }
  .workspace { grid-template-columns: minmax(0, 1fr) 265px; gap: 18px; }
  .inspector { padding: 22px; }
  .auth-layout { gap: 45px; max-width: 900px; }
  .map-hint { display: none; }
  .map-toolbar { padding: 20px 19px 12px; }
}
@media (max-width: 800px) {
  .app { padding: 0 20px; }
  .topbar { min-height: 85px; }
  .brand-caption { display: none; }
  .auth-layout { gap: 30px; padding-top: 34px; }
  .auth-card { padding: 28px; }
  .auth-story h1 { font-size: 31px; letter-spacing: -.9px; }
  .auth-story > p { font-size: 12px; }
  .auth-art { height: 220px; margin-top: 22px; }
  .art-cloud { top: 12px; padding: 18px 20px 15px; }
  .art-device { top: 161px; width: 52px; height: 52px; border-radius: 17px; }
  .workspace { grid-template-columns: 1fr; }
  .inspector { padding: 25px; }
  .inspector:not(.is-open) .inspector-content { display: grid; grid-template-columns: 1fr 1fr; gap: 0 30px; }
  .inspector:not(.is-open) .detail-symbol { grid-column: 1; grid-row: 1 / 3; margin: 0; align-self: start; }
  .inspector:not(.is-open) .detail-title { grid-column: 1; grid-row: 3; margin-top: 15px; }
  .inspector:not(.is-open) .detail-caption { grid-column: 1; grid-row: 4; margin-bottom: 0; }
  .inspector:not(.is-open) .detail-rows { grid-column: 2; grid-row: 1 / 5; }
  .inspector:not(.is-open) .detail-note { display: none; }
  .inspector.is-open { position: fixed; bottom: max(12px, env(safe-area-inset-bottom)); left: 12px; right: 12px; width: min(480px, calc(100% - 24px)); margin: auto; max-height: 75vh; max-height: 75dvh; overflow-y: auto; overscroll-behavior: contain; z-index: 20; box-shadow: 0 20px 80px #0005; }
  .inspector.is-open .inspector-header { margin-bottom: 18px; }
  .map-viewport { min-height: 540px; height: 58dvh; }
}
@media (max-width: 600px) {
  .app { padding: 0 16px; }
  .topbar { min-height: 77px; gap: 8px; }
  .brand { gap: 9px; }
  .brand-mark { width: 39px; height: 39px; border-radius: 13px; }
  .brand-name { font-size: 21px; }
  .top-actions { gap: 7px; }
  .connection-state { margin-right: 6px; font-size: 10px; gap: 6px; }
  .icon-button { width: 36px; height: 36px; }
  .logout-button { padding: 9px; }
  .logout-button span { display: none; }
  .auth-layout { display: flex; flex-direction: column; align-items: stretch; gap: 29px; min-height: auto; padding: 32px 3px 25px; max-width: 420px; }
  .auth-story { align-self: stretch; }
  .auth-story h1 { font-size: 29px; margin: 13px 0 10px; letter-spacing: -1px; }
  .auth-story > p { font-size: 12px; max-width: none; }
  .auth-art, .story-note { display: none; }
  .auth-card { padding: 27px; border-radius: 24px; }
  .steps { margin-bottom: 26px; }
  .auth-card h2 { font-size: 21px; }
  .form-caption { margin-bottom: 24px; }
  .form-symbol { width: 43px; height: 43px; border-radius: 14px; margin-bottom: 18px; }
  .dashboard { padding-top: 17px; }
  .room-heading { margin-bottom: 22px; gap: 12px; align-items: flex-start; }
  .room-title-wrap { gap: 11px; }
  .room-symbol { width: 43px; height: 43px; border-radius: 15px; }
  .room-symbol .icon { width: 21px; height: 21px; }
  .room-title h1 { font-size: 22px; }
  .room-title p { font-size: 10px; margin-top: 5px; }
  .room-switch { padding: 11px; border-radius: 12px; }
  .room-switch span { display: none; }
  .map-card { border-radius: 23px; }
  .map-toolbar { gap: 8px; padding: 18px 16px 15px; }
  .map-title { font-size: 11px; gap: 6px; }
  .layer-tabs button { padding: 6px 9px; font-size: 9px; }
  .map-bottom { padding: 16px 17px 20px; }
  .legend { gap: 14px; font-size: 9px; }
  .map-viewport { height: 560px; min-height: 470px; max-height: 70dvh; }
  .graph-node { width: 142px; padding: 12px; min-height: 80px; border-radius: 17px; }
  .node-head { gap: 8px; }
  .device-icon { width: 29px; height: 29px; border-radius: 10px; }
  .device-icon .icon { width: 18px; height: 18px; }
  .node-name { font-size: 11px; }
  .node-status { font-size: 8px; }
  .node-foot { font-size: 8px; margin-top: 9px; }
  .cloud-node { width: 152px; padding: 17px 12px 15px; border-radius: 21px; }
  .cloud-node .cloud-icon { width: 54px; margin-bottom: 8px; }
  .cloud-caption { font-size: 9px; }
  .panel-note { font-size: 9px; margin-top: 15px; }
  .inspector { border-radius: 23px; padding: 22px; }
  .inspector:not(.is-open) .detail-title { font-size: 18px; }
  .inspector:not(.is-open) .detail-caption { font-size: 10px; }
  .inspector:not(.is-open) .detail-row { gap: 8px; font-size: 10px; }
  .inspector:not(.is-open) .detail-symbol { width: 42px; height: 42px; border-radius: 14px; }
  .footer { font-size: 9px; padding: 21px 2px 25px; }
}
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: .01ms !important; animation-iteration-count: 1 !important; transition-duration: .01ms !important; scroll-behavior: auto !important; }
}
</style>
</head>
<body>
<svg class="sprite" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
<defs>
  <!-- Cloudflare glyph from https://github.com/cloudflare/kumo/blob/main/packages/kumo/src/components/cloudflare-logo/cloudflare-logo.tsx -->
  <symbol id="i-cloud" viewBox="0 0 49 22">
    <path fill="#FF5E1F" d="M33.204 20.4C33.3649 19.9741 33.4217 19.5159 33.3695 19.0636C33.3173 18.6113 33.1577 18.1781 32.904 17.8C32.6435 17.4876 32.3239 17.2297 31.9636 17.0409C31.6032 16.8522 31.2092 16.7363 30.804 16.7L13.404 16.5C13.304 16.5 13.204 16.4 13.104 16.4C13.0808 16.3825 13.0618 16.3599 13.0488 16.3339C13.0358 16.3078 13.029 16.2791 13.029 16.25C13.029 16.2209 13.0358 16.1922 13.0488 16.1662C13.0618 16.1401 13.0808 16.1175 13.104 16.1C13.204 15.9 13.304 15.8 13.504 15.8L31.004 15.6C32.115 15.4767 33.1731 15.0597 34.0695 14.3918C34.9659 13.7239 35.6681 12.8293 36.104 11.8L37.104 9.20002C37.104 9.10002 37.204 9.00001 37.104 8.90001C36.5604 6.47843 35.2411 4.30052 33.3466 2.69721C31.4521 1.09391 29.086 0.152865 26.6079 0.0170769C24.1298 -0.118712 21.675 0.558179 19.6167 1.94489C17.5584 3.33161 16.009 5.35233 15.204 7.70002C14.159 6.95365 12.8843 6.59957 11.604 6.70002C10.4291 6.83102 9.33369 7.35777 8.49774 8.19372C7.66179 9.02966 7.13505 10.1251 7.00404 11.3C6.93745 11.9014 6.97125 12.5097 7.10404 13.1C5.20298 13.1526 3.39743 13.9448 2.07147 15.3081C0.745511 16.6714 0.00377461 18.4982 0.00403983 20.4C-0.0123708 20.7695 0.0212659 21.1395 0.104038 21.5C0.10863 21.5781 0.141713 21.6517 0.19701 21.707C0.252307 21.7623 0.325975 21.7954 0.404041 21.8H32.504C32.704 21.8 32.904 21.7 32.904 21.5L33.204 20.4Z"/>
    <path fill="#FF9911" d="M38.704 9.20002H38.204C38.104 9.20002 38.004 9.30001 37.904 9.40001L37.204 11.8C37.0431 12.2259 36.9864 12.6841 37.0386 13.1364C37.0908 13.5887 37.2504 14.0219 37.504 14.4C37.7646 14.7124 38.0842 14.9704 38.4445 15.1591C38.8049 15.3479 39.1989 15.4637 39.604 15.5L43.304 15.7C43.404 15.7 43.504 15.8 43.604 15.8C43.6273 15.8175 43.6462 15.8401 43.6592 15.8662C43.6723 15.8922 43.679 15.9209 43.679 15.95C43.679 15.9791 43.6723 16.0078 43.6592 16.0339C43.6462 16.0599 43.6273 16.0826 43.604 16.1C43.504 16.3 43.404 16.4 43.204 16.4L39.404 16.6C38.293 16.7233 37.2349 17.1403 36.3386 17.8082C35.4422 18.4761 34.74 19.3707 34.304 20.4L34.104 21.3C34.004 21.4 34.104 21.6 34.304 21.6H47.504C47.5448 21.6058 47.5863 21.6021 47.6254 21.5891C47.6644 21.5761 47.6999 21.5541 47.729 21.525C47.7581 21.4959 47.7801 21.4604 47.7931 21.4214C47.8061 21.3823 47.8099 21.3408 47.804 21.3C48.0421 20.4527 48.1764 19.5797 48.204 18.7C48.1882 16.1854 47.1822 13.7782 45.404 12C43.6259 10.2218 41.2187 9.21587 38.704 9.20002Z"/>
  </symbol>
  <symbol id="i-lock" viewBox="0 0 24 24"><rect x="5" y="10" width="14" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2"/></symbol>
  <symbol id="i-key" viewBox="0 0 24 24"><circle cx="8" cy="9" r="4.5"/><path d="m11.5 12.5 8 8m-3-3 2-2m-5-1 2-2"/></symbol>
  <symbol id="i-room" viewBox="0 0 24 24"><path d="m3 7 9-4 9 4-9 4Z M3 7v10l9 4 9-4V7m-9 4v10"/></symbol>
  <symbol id="i-arrow" viewBox="0 0 24 24"><path d="M4 12h15m-6-6 6 6-6 6"/></symbol>
  <symbol id="i-back" viewBox="0 0 24 24"><path d="M20 12H5m6-6-6 6 6 6"/></symbol>
  <symbol id="i-sun" viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5"/></symbol>
  <symbol id="i-moon" viewBox="0 0 24 24"><path d="M20.5 14a8.5 8.5 0 0 1-10.5-10.5A8.5 8.5 0 1 0 20.5 14Z"/></symbol>
  <symbol id="i-eye" viewBox="0 0 24 24"><path d="M2.5 12s3.3-6 9.5-6 9.5 6 9.5 6-3.3 6-9.5 6-9.5-6-9.5-6Z"/><circle cx="12" cy="12" r="2.5"/></symbol>
  <symbol id="i-eye-off" viewBox="0 0 24 24"><path d="m3 3 18 18M6 6.5A14 14 0 0 0 2.5 12s3.3 6 9.5 6a11 11 0 0 0 5.5-1.5M10 6.2A10 10 0 0 1 12 6c6.2 0 9.5 6 9.5 6a19 19 0 0 1-2.5 3M10 10a2.8 2.8 0 0 0 4 4"/></symbol>
  <symbol id="i-check" viewBox="0 0 24 24"><path d="m5 12 4 4L19 6"/></symbol>
  <symbol id="i-monitor" viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="13" rx="2.5"/><path d="M8 21h8m-4-4v4"/></symbol>
  <symbol id="i-phone" viewBox="0 0 24 24"><rect x="6" y="2" width="12" height="20" rx="3"/><path d="M10 5h4m-3 14h2"/></symbol>
  <symbol id="i-server" viewBox="0 0 24 24"><rect x="4" y="3" width="16" height="8" rx="2.5"/><rect x="4" y="13" width="16" height="8" rx="2.5"/><path d="M8 7h.01M8 17h.01m5-10h3m-3 10h3"/></symbol>
  <symbol id="i-watch" viewBox="0 0 24 24"><rect x="6" y="6" width="12" height="12" rx="4"/><path d="m9 6 1-4h4l1 4m-6 12 1 4h4l1-4m-3-8v3l2 1"/></symbol>
  <symbol id="i-network" viewBox="0 0 24 24"><circle cx="12" cy="5" r="3"/><circle cx="5" cy="18" r="3"/><circle cx="19" cy="18" r="3"/><path d="m10.5 8-4 7m7-7 4 7M8 18h8"/></symbol>
  <symbol id="i-link" viewBox="0 0 24 24"><path d="m9 8 2-2a5 5 0 0 1 7 7l-2 2m-1 1-2 2a5 5 0 0 1-7-7l2-2m1 6 6-6"/></symbol>
  <symbol id="i-mic" viewBox="0 0 24 24"><rect x="9" y="2" width="6" height="13" rx="3"/><path d="M5 11v1a7 7 0 0 0 14 0v-1m-7 8v3m-3 0h6"/></symbol>
  <symbol id="i-swap" viewBox="0 0 24 24"><path d="M4 7h15m-4-4 4 4-4 4M20 17H5m4-4-4 4 4 4"/></symbol>
  <symbol id="i-logout" viewBox="0 0 24 24"><path d="M10 4H5v16h5m3-8h8m-4-4 4 4-4 4"/></symbol>
  <symbol id="i-info" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10h.01"/></symbol>
  <symbol id="i-close" viewBox="0 0 24 24"><path d="m6 6 12 12M6 18 18 6"/></symbol>
  <symbol id="i-plus" viewBox="0 0 24 24"><path d="M5 12h14m-7-7v14"/></symbol>
  <symbol id="i-minus" viewBox="0 0 24 24"><path d="M5 12h14"/></symbol>
  <symbol id="i-fit" viewBox="0 0 24 24"><path d="M4 9V4h5m6 0h5v5M4 15v5h5m6 0h5v-5"/></symbol>
  <symbol id="i-activity" viewBox="0 0 24 24"><path d="M3 12h4l3-7 4 14 3-7h4"/></symbol>
</defs>
</svg>
<div class="app">
  <header class="topbar" id="topbar">
    <div class="brand"><span class="brand-mark" aria-hidden="true"><span class="brand-emoji">🎯</span></span><span class="brand-name">hole</span><span class="brand-caption">房间面板</span></div>
    <div class="top-actions">
      <div class="connection-state" id="connectionState" role="status" data-kind="idle"><i class="status-dot"></i><span id="connectionText">尚未连接</span></div>
      <button class="icon-button" id="themeToggle" type="button" aria-label="切换到暗色主题" title="切换主题"><svg class="icon"><use href="#i-moon"/></svg></button>
      <button class="text-button logout-button" id="logout" type="button" hidden title="退出面板" aria-label="退出面板"><svg class="icon"><use href="#i-logout"/></svg><span>退出</span></button>
    </div>
  </header>
  <main>
    <section class="auth-layout" id="entry" aria-label="验证与进入房间">
      <div class="auth-story">
        <div class="mini-label"><svg class="icon"><use href="#i-network"/></svg><span>Hole / 房间连接</span></div>
        <h1 id="entryTitle">先连接信令服务。</h1>
        <p id="entryCaption">验证信令密码后，选择你的房间，查看设备之间的连接。</p>
        <div class="auth-art" aria-hidden="true">
          <svg class="auth-wires" viewBox="0 0 400 255" preserveAspectRatio="none"><path d="M195 85C195 162 100 128 100 199M205 85C205 155 303 133 303 199"/></svg>
          <div class="art-cloud"><svg class="cloud-icon"><use href="#i-cloud"/></svg><span>CLOUDFLARE</span></div>
          <div class="art-device"><svg class="icon"><use href="#i-monitor"/></svg></div>
          <div class="art-device"><svg class="icon"><use href="#i-phone"/></svg></div>
        </div>
        <div class="story-note"><svg class="icon"><use href="#i-eye"/></svg>只读查看，连接由设备自行建立。</div>
      </div>
      <div class="auth-card" id="authCard">
        <div class="steps" aria-label="验证进度">
          <span class="step active" id="signalStep" aria-current="step"><span class="step-number" id="signalStepNumber">1</span>信令验证</span><span class="step-bridge"></span><span class="step" id="roomStep"><span class="step-number">2</span>进入房间</span>
        </div>
        <form id="authForm">
          <div class="form-symbol"><svg class="icon"><use href="#i-lock"/></svg></div>
          <h2>验证信令密码</h2><p class="form-caption">使用部署 Worker 时设置的信令密码。</p>
          <label class="field-label" for="password">信令密码</label>
          <div class="input-wrap"><svg class="icon"><use href="#i-key"/></svg><input id="password" name="password" type="password" placeholder="输入信令密码" autocomplete="current-password" aria-describedby="authError" required><button class="password-toggle" type="button" data-password="password" aria-label="显示信令密码" aria-pressed="false"><svg class="icon"><use href="#i-eye"/></svg></button></div>
          <label class="form-foot"><input id="remember" name="remember" type="checkbox">在此设备保持登录 7 天（仅限个人设备）</label>
          <button class="primary-button" id="authSubmit" type="submit"><span>验证并继续</span><svg class="icon"><use href="#i-arrow"/></svg></button>
          <p class="form-error" id="authError" role="alert"></p><p class="form-foot"><svg class="icon"><use href="#i-lock"/></svg>刷新可恢复登录；不在浏览器中保存密码。退出可清除登录状态。</p>
          <button class="text-button" id="restoreRetry" type="button" hidden>重试恢复登录</button>
        </form>
        <form id="roomForm" hidden>
          <div class="form-symbol"><svg class="icon"><use href="#i-room"/></svg></div>
          <h2>进入你的房间</h2><p class="form-caption">房间号与密码需和设备上的配置一致。</p>
          <label class="field-label" for="roomId">房间号</label>
          <div class="input-wrap"><svg class="icon"><use href="#i-room"/></svg><input id="roomId" name="room" type="text" maxlength="128" placeholder="输入房间号" autocomplete="off" autocapitalize="none" spellcheck="false" aria-describedby="roomError" required></div>
          <label class="field-label" for="token">房间密码</label>
          <div class="input-wrap"><svg class="icon"><use href="#i-key"/></svg><input id="token" name="token" type="password" placeholder="输入房间密码" autocomplete="off" aria-describedby="roomError" required><button class="password-toggle" type="button" data-password="token" aria-label="显示房间密码" aria-pressed="false"><svg class="icon"><use href="#i-eye"/></svg></button></div>
          <button class="primary-button" id="roomSubmit" type="submit"><span>查看房间连接</span><svg class="icon"><use href="#i-arrow"/></svg></button><p class="form-error" id="roomError" role="alert"></p>
          <button class="text-button back-button" id="backToAuth" type="button"><svg class="icon"><use href="#i-back"/></svg>返回信令验证</button>
        </form>
      </div>
    </section>
    <section class="dashboard" id="dashboard" hidden aria-label="房间连接拓扑">
      <div class="room-heading" id="roomHeading"><div class="room-title-wrap"><div class="room-symbol"><svg class="icon"><use href="#i-room"/></svg></div><div class="room-title"><h1 id="roomTitle"></h1><p id="roomSummary"></p></div></div><button class="text-button room-switch" id="switchRoom" type="button" aria-label="切换房间" title="切换房间"><svg class="icon"><use href="#i-swap"/></svg><span>切换房间</span></button></div>
      <div class="workspace">
        <section class="map-card" id="mapCard" aria-label="交互连接图">
          <div class="map-toolbar"><div class="map-title"><svg class="icon"><use href="#i-network"/></svg>连接拓扑</div><div class="layer-tabs" id="layerTabs" role="group" aria-label="显示连接类型"><button type="button" data-layer="all" aria-pressed="true">全部</button><button type="button" data-layer="transport" aria-pressed="false">设备</button><button type="button" data-layer="signal" aria-pressed="false">信令</button></div></div>
          <div class="map-viewport" id="viewport" tabindex="0" aria-label="连接画布，可拖动，使用加减键缩放，0 键居中">
            <div class="scene" id="scene"><svg class="edge-layer" id="edges" aria-label="设备连接"></svg><div id="nodes"></div></div>
            <div class="map-empty" id="mapEmpty" hidden><h3>暂无在线设备</h3><p>设备加入后，会在这里自动出现。</p></div>
            <div class="zoom-controls" id="zoomControls" aria-label="画布缩放"><button type="button" id="zoomOut" aria-label="缩小" title="缩小"><svg class="icon"><use href="#i-minus"/></svg></button><span class="zoom-level" id="zoomLevel">100%</span><button type="button" id="zoomIn" aria-label="放大" title="放大"><svg class="icon"><use href="#i-plus"/></svg></button><button type="button" id="fitGraph" aria-label="居中显示" title="居中显示"><svg class="icon"><use href="#i-fit"/></svg></button></div>
            <div class="map-tooltip" id="tooltip" role="tooltip" hidden></div>
          </div>
          <div class="map-bottom"><div class="legend"><span><i class="legend-line"></i>直连</span><span><i class="legend-line turn"></i>TURN</span><span><i class="legend-line hole"></i>Hole</span><span><i class="legend-line peer-relay"></i>节点中继</span><span><i class="legend-line pending"></i>协商中</span><span><i class="legend-line signal"></i>信令</span><span><i class="legend-line blocked"></i>受阻</span></div><span class="map-hint">拖动探索 · 点选查看详情</span></div>
        </section>
        <div>
          <aside class="inspector" id="inspector" role="region" aria-label="房间详情" tabindex="-1"><div class="inspector-header"><h2 id="detailLabel">房间一览</h2><button class="icon-button close-details" id="closeDetails" type="button" aria-label="关闭详情" title="关闭详情" hidden><svg class="icon"><use href="#i-close"/></svg></button></div><div class="inspector-content" id="detailContent"></div></aside>
          <section class="update-feed" id="updateFeed" aria-label="最近变化"><div class="update-heading"><span>最近变化</span><svg class="icon"><use href="#i-activity"/></svg></div><div id="feedEvents"></div></section>
        </div>
      </div>
      <p class="panel-note" id="panelNote"><svg class="icon"><use href="#i-info"/></svg><span>状态随房间事件更新。</span></p>
    </section>
  </main>
  <footer class="footer" id="footer"><span class="footer-brand">HOLE</span><span id="footerCaption">房间连接面板</span></footer>
</div>
<div class="detail-backdrop" id="detailBackdrop" hidden></div>
<script>
(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const ns = 'http://www.w3.org/2000/svg';
  const smallScreen = matchMedia('(max-width: 800px)');
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
  const systemTheme = matchMedia('(prefers-color-scheme: dark)');
  const CLOUD = 'cloud';
  const deviceKey = name => 'device:' + name;
  const pairKey = (a, b) => 'pair:' + JSON.stringify([a, b].sort());
  const nodes = new Map(), edges = new Map();
  let snapshot = null, selected = null, returnFocus = null, layer = 'all', feed = [];
  let roomName = '', watching = false, expiryTimer = null;
  let requestChain = Promise.resolve();
  let socket = null, retryTimer = null, connectionTimer = null, epoch = 0, retryAttempt = 0;
  let frame = 0, world = { width: 1000, height: 600 }, camera = { x: 0, y: 0, scale: 1 }, manualCamera = false;
  let suppressClickUntil = 0, dragging = false;
  const pointers = new Map();

  function renderBrandEmoji() {
    const node = document.querySelector('.brand-emoji'), style = getComputedStyle(node);
    const ratio = Math.max(2, devicePixelRatio || 1), size = Math.round(parseFloat(style.width) * ratio);
    const source = document.createElement('canvas');
    source.width = source.height = size * 3;
    const context = source.getContext('2d', { willReadFrequently: true });
    if (!context) return;
    context.font = parseFloat(style.fontSize) * ratio + 'px ' + style.fontFamily;
    context.textAlign = 'center'; context.textBaseline = 'middle';
    context.fillText('🎯', source.width / 2, source.height / 2);
    // Center the visible emoji itself; font baselines and side bearings vary by platform.
    const pixels = context.getImageData(0, 0, source.width, source.height).data;
    let left = source.width, top = source.height, right = -1, bottom = -1;
    for (let y = 0; y < source.height; y++) for (let x = 0; x < source.width; x++) {
      if (!pixels[(y * source.width + x) * 4 + 3]) continue;
      left = Math.min(left, x); top = Math.min(top, y);
      right = Math.max(right, x); bottom = Math.max(bottom, y);
    }
    if (right < left) return;
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = size;
    const output = canvas.getContext('2d');
    if (!output) return;
    const width = right - left + 1, height = bottom - top + 1, scale = Math.min(1, size / width, size / height);
    output.drawImage(source, left, top, width, height, (size - width * scale) / 2, (size - height * scale) / 2, width * scale, height * scale);
    node.replaceChildren(canvas);
  }
  renderBrandEmoji();

  function element(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }
  function icon(name) {
    const svg = document.createElementNS(ns, 'svg'), use = document.createElementNS(ns, 'use');
    svg.setAttribute('class', name === 'cloud' ? 'cloud-icon' : 'icon');
    svg.setAttribute('aria-hidden', 'true');
    use.setAttribute('href', '#i-' + name);
    svg.append(use);
    return svg;
  }
  function animate(node, frames, duration = 350) {
    if (reducedMotion.matches || !node.animate) return null;
    return node.animate(frames, { duration, easing: 'cubic-bezier(.22,.8,.25,1)' });
  }
  function removeAnimated(node) {
    node.style.pointerEvents = 'none';
    node.setAttribute('aria-hidden', 'true');
    node.setAttribute('tabindex', '-1');
    const animation = animate(node, [{ opacity: 1, scale: '1' }, { opacity: 0, scale: '.86' }], 220);
    if (animation) animation.finished.then(() => node.remove(), () => node.remove());
    else node.remove();
  }
  function setState(text, kind = 'idle') {
    $('connectionText').textContent = text;
    $('connectionState').dataset.kind = kind;
  }
  function busy(id, value, label) {
    const button = $(id);
    button.disabled = value;
    button.setAttribute('aria-busy', String(value));
    button.querySelector('span').textContent = value ? label : id === 'authSubmit' ? '验证并继续' : '查看房间连接';
  }
  function screen(page) {
    $('entry').hidden = page === 'dashboard';
    $('dashboard').hidden = page !== 'dashboard';
    $('authForm').hidden = page !== 'auth';
    $('roomForm').hidden = page !== 'room';
    $('logout').hidden = page === 'auth';
    $('entryTitle').textContent = page === 'auth' ? '先连接信令服务。' : '接下来，进入房间。';
    $('entryCaption').textContent = page === 'auth' ? '验证信令密码后，选择你的房间，查看设备之间的连接。' : '信令服务已验证。输入房间信息，查看属于你的连接图。';
    $('signalStep').className = 'step ' + (page === 'auth' ? 'active' : 'done');
    $('roomStep').className = 'step' + (page === 'room' ? ' active' : '');
    $('signalStepNumber').replaceChildren(page === 'auth' ? document.createTextNode('1') : icon('check'));
    $('signalStep').removeAttribute('aria-current');
    $('roomStep').removeAttribute('aria-current');
    $(page === 'auth' ? 'signalStep' : 'roomStep').setAttribute('aria-current', 'step');
    if (page !== 'dashboard') animate($('authCard'), [{ opacity: 0, translate: '0 10px' }, { opacity: 1, translate: '0 0' }]);
  }
  function hidePasswords() {
    for (const button of document.querySelectorAll('[data-password]')) {
      $(button.dataset.password).type = 'password';
      button.setAttribute('aria-pressed', 'false');
      button.setAttribute('aria-label', button.dataset.password === 'password' ? '显示信令密码' : '显示房间密码');
      button.replaceChildren(icon('eye'));
    }
  }
  for (const button of document.querySelectorAll('[data-password]')) button.addEventListener('click', () => {
    const input = $(button.dataset.password), visible = input.type === 'password';
    input.type = visible ? 'text' : 'password';
    button.setAttribute('aria-pressed', String(visible));
    button.setAttribute('aria-label', (visible ? '隐藏' : '显示') + (input.id === 'password' ? '信令密码' : '房间密码'));
    button.replaceChildren(icon(visible ? 'eye-off' : 'eye'));
  });

  let chosenTheme = '';
  try { chosenTheme = localStorage.getItem('hole-theme') || ''; } catch {}
  if (!['light', 'dark'].includes(chosenTheme)) chosenTheme = '';
  function applyTheme() {
    const theme = chosenTheme || (systemTheme.matches ? 'dark' : 'light');
    document.documentElement.dataset.theme = theme;
    const label = theme === 'dark' ? '切换到亮色主题' : '切换到暗色主题';
    $('themeToggle').replaceChildren(icon(theme === 'dark' ? 'sun' : 'moon'));
    $('themeToggle').setAttribute('aria-label', label);
    $('themeToggle').title = label;
  }
  $('themeToggle').addEventListener('click', () => {
    chosenTheme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
    try { localStorage.setItem('hole-theme', chosenTheme); } catch {}
    applyTheme();
  });
  systemTheme.addEventListener('change', () => { if (!chosenTheme) applyTheme(); });
  applyTheme();

  const signalLabel = member => member.signal_online ? '信令直连' : member.signal_recovering ? '信令恢复中' : '信令委托';
  const pathLabel = path => ({ direct: '直连', fixed: '固定入口', relay: 'TURN 中继', hole: 'Hole 中继', peer_relay: '节点中继' })[path] || path || '尚未确认';
  const phaseLabel = phase => ({ direct: '直连协商', fixed: '固定入口建连', relay: 'TURN 协商', hole: 'Hole 中继协商', peer_relay: '节点中继协商' })[phase] || phase || '等待协商';
  const punchLabel = kind => ({ ready: '准备就绪', attempt: '尝试打洞', result: '打洞结果', calibrated: '校准完成', pool_select: '选择探测端口', exhausted: '尝试耗尽' })[kind] || kind;
  const resultLabel = result => ({ ok: '成功', success: '成功', failed: '失败', retry: '重试', refresh: '重新校准', exhausted: '尝试耗尽', timeout: '超时' })[result] || result;
  function phase(record) {
    if (record.blocked) return record.blocked === 'peer_identity_mismatch' ? '对端身份校验失败' : '协议不兼容';
    if (record.established?.length === 2 && record.established.every(Boolean)) return '双方已确认连接';
    if (record.established?.some(Boolean)) return '一端已确认连接';
    if (record.profile === 'fixed-quic-mux-v1') return '固定入口建连中';
    if (record.phases?.every(p => p === 'direct')) return record.direct_stage === 'punch' ? '预测打洞中' : '直连协商中';
    return [...new Set(record.phases || [])].map(phaseLabel).join(' / ') || '等待协商';
  }
  function deviceIcon(name) {
    if (/phone|android|iphone|手机/i.test(name)) return 'phone';
    if (/watch|wear|手表/i.test(name)) return 'watch';
    if (/server|nas|router|服务器|路由/i.test(name)) return 'server';
    return 'monitor';
  }
  function groupLabel(member) {
    const index = snapshot.groups.findIndex(group => group.group_id === member.group_id);
    return index >= 0 ? '分组 ' + (index + 1) : '未分组';
  }
  function edgeLabel(edge) {
    if (edge.kind === 'signal') return '信令直连';
    if (!edge.record) return '房间图已确认';
    const route = edge.route;
    if (route?.kind === 'turn') return 'TURN 中继 · ' + phase(edge.record);
    if (route?.kind === 'hole') return 'Hole 中继 · ' + phase(edge.record);
    if (route?.kind === 'peer_relay') return '节点中继 · ' + phase(edge.record);
    return phase(edge.record);
  }
  function routeLabel(route) {
    return route?.kind === 'turn' ? 'TURN 中继' : route?.kind === 'hole' ? 'Hole 中继' : route?.kind === 'peer_relay' ? '节点中继' : '直连';
  }
  function edgeCount(edgesToCount) {
    return new Set(edgesToCount.map(edge => edge.logicalId || edge.id)).size;
  }
  function logicalConnections(edgesToFilter, predicate) {
    const unique = new Map();
    for (const edge of edgesToFilter) if (predicate(edge)) unique.set(edge.logicalId || edge.id, edge);
    return [...unique.values()];
  }
  function edgePeerLabel(edge, member) {
    if (member === edge.a) return edge.b + (edge.route ? ' · ' + routeLabel(edge.route) : '');
    if (member === edge.b) return edge.a + (edge.route ? ' · ' + routeLabel(edge.route) : '');
    return edge.a + ' ↔ ' + edge.b;
  }
  function buildEdges(data) {
    const next = new Map(), visible = new Set(data.members.map(member => member.device_name));
    for (const link of data.links) {
      if (!visible.has(link.from) || !visible.has(link.to)) continue;
      const id = pairKey(link.from, link.to);
      next.set(id, { id, logicalId: id, kind: 'transport', from: deviceKey(link.from), to: deviceKey(link.to), a: link.from, b: link.to, participants: [link.from, link.to], graphConfirmed: true, confirmed: true });
    }
    for (const record of data.transports) {
      if (!visible.has(record.a) || !visible.has(record.b)) continue;
      const logicalId = pairKey(record.a, record.b), graphConfirmed = next.get(logicalId)?.graphConfirmed === true;
      const confirmed = !record.blocked && (graphConfirmed || (record.established?.length === 2 && record.established.every(Boolean)));
      const route = record.route;
      if (!route) {
        next.set(logicalId, { id: logicalId, logicalId, kind: 'transport', from: deviceKey(record.a), to: deviceKey(record.b), a: record.a, b: record.b, participants: [record.a, record.b], record, graphConfirmed, confirmed });
        continue;
      }
      next.delete(logicalId);
      const relayName = route.kind === 'turn' ? 'TURN 中继' : route.device;
      const relayId = route.kind === 'turn' ? 'relay:turn:' + (record.id || logicalId) : deviceKey(relayName);
      const relayNode = route.kind === 'turn' ? { kind: 'relay', node_id: relayId, device_name: relayName, relayKind: route.kind } :
        visible.has(relayName) ? null : { kind: 'relay', node_id: relayId, device_name: relayName, relayKind: route.kind, synthetic: true };
      const segments = [[record.a, relayName], [relayName, record.b]];
      for (const [index, [fromName, toName]] of segments.entries()) {
        const id = 'transport:' + (record.id || logicalId) + ':segment:' + index;
        next.set(id, { id, logicalId, kind: 'transport', route, relayNode, from: index === 0 ? deviceKey(record.a) : relayId,
          to: index === 0 ? relayId : deviceKey(record.b), a: record.a, b: record.b,
          participants: [record.a, record.b, relayName], record, graphConfirmed, confirmed });
      }
    }
    for (const member of data.members) if (member.signal_online) {
      const id = 'signal:' + member.device_name;
      next.set(id, { id, kind: 'signal', from: deviceKey(member.device_name), to: CLOUD, a: member.device_name, b: 'Cloudflare', participants: [member.device_name, 'Cloudflare'], confirmed: true });
    }
    return next;
  }

  function rows(parent, values) {
    const list = element('dl', 'detail-rows');
    for (const [label, value] of values) {
      const row = element('div', 'detail-row');
      row.append(element('dt', '', label), element('dd', '', value));
      list.append(row);
    }
    parent.append(list);
  }
  function detailIntro(parent, name, title, caption) {
    const symbol = element('div', 'detail-symbol' + (name === 'cloud' ? ' cloud' : ''));
    symbol.append(icon(name));
    parent.append(symbol, element('h3', 'detail-title', title), element('p', 'detail-caption', caption));
  }
  function badge(parent, label, tone = '') {
    const node = element('div', 'detail-badge ' + tone);
    node.append(element('i', 'status-dot'), document.createTextNode(label));
    parent.append(node);
  }
  function peerButton(parent, member, detail, action) {
    const button = element('button', 'peer-button');
    button.type = 'button';
    button.append(icon(deviceIcon(member)), element('span', '', member), element('small', '', detail));
    button.addEventListener('click', () => choose(action, button));
    parent.append(button);
  }
  function renderDetails() {
    if (!snapshot) return;
    const parent = $('detailContent');
    parent.replaceChildren();
    $('closeDetails').hidden = !selected;
    $('inspector').classList.toggle('is-open', !!selected);
    const connections = [...edges.values()].map(value => value.data).filter(edge => edge.kind === 'transport');
    const online = snapshot.members.filter(member => member.signal_online);
    if (!selected) {
      $('detailLabel').textContent = '房间一览';
      detailIntro(parent, 'room', '房间连接', '选择设备或连线，查看详情。');
      rows(parent, [['可见设备', snapshot.members.length + ' 台'], ['信令直连', online.length + ' 台'], ['已确认连接', edgeCount(connections.filter(edge => edge.confirmed)) + ' 条'], ['连通分组', snapshot.groups.length + ' 组']]);
      parent.append(element('p', 'detail-note', '绿色表示直连，蓝色表示 TURN，紫色表示 Hole，青色表示节点中继。灰色虚线表示协商中，红色虚线表示受阻，橙色虚线表示信令。'));
    } else if (selected.type === 'node' && selected.id === CLOUD) {
      $('detailLabel').textContent = '信令服务';
      detailIntro(parent, 'cloud', 'Cloudflare', 'Worker 负责房间协调，业务数据由设备之间的路径传输。');
      badge(parent, '当前房间的信令入口');
      rows(parent, [['Worker 版本', snapshot.worker_version || '未上报'], ['信令直连', online.length + ' 台'], ['信令委托', snapshot.members.filter(member => !member.signal_online && !member.signal_recovering).length + ' 台'], ['信令恢复中', snapshot.members.filter(member => !member.signal_online && member.signal_recovering).length + ' 台']]);
      if (online.length) parent.append(element('h4', 'detail-heading', '直接连接的设备'));
      for (const member of online) peerButton(parent, member.device_name, '查看', { type: 'node', id: deviceKey(member.device_name) });
    } else if (selected.type === 'node') {
      const member = nodes.get(selected.id)?.data;
      if (!member) { choose(null); return; }
      if (member.kind === 'relay') {
        const related = logicalConnections(connections, edge => edge.from === selected.id || edge.to === selected.id);
        $('detailLabel').textContent = '中继节点';
        detailIntro(parent, 'server', member.device_name, routeLabel({ kind: member.relayKind }));
        const status = related.some(edge => edge.record?.blocked) ? 'blocked' : related.length && related.every(edge => edge.confirmed) ? '' : 'pending';
        badge(parent, related[0] ? edgeLabel(related[0]) : routeLabel({ kind: member.relayKind }), status);
        rows(parent, [['承载连接', edgeCount(related) + ' 条'], ['节点类型', member.relayKind === 'turn' ? '独立 TURN 服务' : '房间设备']]);
        parent.append(element('p', 'detail-note', member.relayKind === 'turn' ? '这是当前传输专用的 TURN 逻辑节点。业务数据经 TURN 服务转发。' : '业务数据经这个房间设备转发，图中两段线属于同一条端到端传输。'));
        for (const edge of related) peerButton(parent, edge.a + ' ↔ ' + edge.b, edgeLabel(edge), { type: 'edge', id: edge.id });
        syncSheet();
        return;
      }
      $('detailLabel').textContent = '设备详情';
      detailIntro(parent, deviceIcon(member.device_name), member.device_name, groupLabel(member));
      badge(parent, signalLabel(member), member.signal_recovering ? 'pending' : '');
      const related = logicalConnections(connections, edge => (edge.participants || [edge.a, edge.b]).includes(member.device_name));
      rows(parent, [['核心版本', member.core_version || '未上报'], ['语音', member.voice ? '已启用' : '未启用'], ['已确认路径', edgeCount(related.filter(edge => edge.confirmed)) + ' 条'], ['分组标识', member.group_id || '未上报']]);
      parent.append(element('h4', 'detail-heading', '连接的设备'));
      if (!related.length) parent.append(element('p', 'detail-caption', '尚未收到这台设备的连接关系。'));
      for (const edge of related) peerButton(parent, edgePeerLabel(edge, member.device_name), edge.record ? edgeLabel(edge) : edge.record?.blocked ? '受阻' : edge.confirmed ? '已连接' : '协商中', { type: 'edge', id: edge.id });
      if (!member.signal_online) parent.append(element('p', 'detail-note', member.signal_recovering ? '设备正在恢复信令连接，图中保留最近的房间状态。' : '信令由同组入口同步，因此不会绘制到 Cloudflare 的直接连线。'));
    } else {
      const edge = edges.get(selected.id)?.data;
      if (!edge) { choose(null); return; }
      $('detailLabel').textContent = edge.kind === 'signal' ? '信令连接' : '路径详情';
      detailIntro(parent, edge.kind === 'signal' ? 'cloud' : 'link', edge.a + ' ↔ ' + edge.b, edge.kind === 'signal' ? '设备与 Worker 的 WebSocket 连接。' : (edge.route ? routeLabel(edge.route) + '路径，点击任一段查看同一条传输。' : '设备双方最近上报的协商与路径状态。'));
      badge(parent, edgeLabel(edge), edge.record?.blocked ? 'blocked' : edge.confirmed ? '' : 'pending');
      if (edge.kind === 'signal') {
        rows(parent, [['连接类型', '直接信令'], ['Worker 版本', snapshot.worker_version || '未上报'], ['用途', '房间协调与通知']]);
        parent.append(element('p', 'detail-note', '这条线表示信令连接，不表示业务流量经过 Cloudflare。'));
      } else if (edge.record) {
        const record = edge.record;
        rows(parent, [['连接代次', String(record.generation)], ['路径类型', routeLabel(edge.route)], [record.a + ' 路径', pathLabel(record.established?.[0])], [record.b + ' 路径', pathLabel(record.established?.[1])], ['房间图确认', edge.graphConfirmed ? '已确认' : '未确认'], ['语音', record.voice ? '已启用' : '未启用'], ['房间控制', record.control ? '已启用' : '未启用']]);
        if (!edge.confirmed && !record.blocked) {
          parent.append(element('h4', 'detail-heading', '协商阶段'));
          rows(parent, [[record.a, phaseLabel(record.phases?.[0])], [record.b, phaseLabel(record.phases?.[1])]]);
        }
        if (record.mappings?.length) {
          parent.append(element('h4', 'detail-heading', '映射 ID'));
          parent.append(element('p', 'detail-caption', record.mappings.join('、')));
        }
        for (const [index, punch] of (record.punch || []).entries()) if (punch) {
          parent.append(element('h4', 'detail-heading', (index ? record.b : record.a) + ' · 打洞进度'));
          const values = [['阶段', punchLabel(punch.kind)]];
          if (punch.round !== undefined) values.push(['轮次', String(punch.round)]);
          if (punch.result) values.push(['结果', resultLabel(punch.result)]);
          rows(parent, values);
        }
      } else parent.append(element('p', 'detail-note', '双方房间图已确认这条连接，Worker 尚未收到对应的传输详情。'));
    }
    syncSheet();
  }
  function syncSheet() {
    const modal = smallScreen.matches && !!selected;
    $('detailBackdrop').hidden = !modal;
    $('inspector').setAttribute('role', modal ? 'dialog' : 'region');
    if (modal) $('inspector').setAttribute('aria-modal', 'true');
    else $('inspector').removeAttribute('aria-modal');
    for (const id of ['topbar', 'roomHeading', 'mapCard', 'updateFeed', 'panelNote', 'footer']) $(id).inert = modal;
    document.body.style.overflow = modal ? 'hidden' : '';
  }
  function choose(next, source) {
    if (source && $('viewport').contains(source)) returnFocus = source;
    selected = next;
    $('tooltip').hidden = true;
    renderDetails();
    updateEmphasis();
    if (selected && (smallScreen.matches || $('inspector').contains(source))) $('closeDetails').focus({ preventScroll: true });
    if (!selected && returnFocus?.isConnected) { returnFocus.focus({ preventScroll: true }); returnFocus = null; }
  }
  $('closeDetails').addEventListener('click', () => choose(null));
  $('detailBackdrop').addEventListener('click', () => choose(null));
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape') { $('tooltip').hidden = true; if (selected) choose(null); }
    if (event.key !== 'Tab' || !smallScreen.matches || !selected) return;
    const buttons = [...$('inspector').querySelectorAll('button:not([hidden])')], first = buttons[0], last = buttons.at(-1);
    if (event.shiftKey && (document.activeElement === first || document.activeElement === $('inspector'))) { last.focus(); event.preventDefault(); }
    else if (!event.shiftKey && document.activeElement === last) { first.focus(); event.preventDefault(); }
  });
  smallScreen.addEventListener('change', () => {
    syncSheet();
    if (smallScreen.matches && selected) $('closeDetails').focus({ preventScroll: true });
  });

  function updateEmphasis() {
    const highlighted = new Set(), related = new Set();
    if (selected?.type === 'node') {
      highlighted.add(selected.id);
      const logicalIds = new Set();
      for (const [id, edge] of edges) if (edge.data.from === selected.id || edge.data.to === selected.id) {
        if (layer !== 'all' && layer !== edge.data.kind) continue;
        logicalIds.add(edge.data.logicalId || id);
      }
      for (const [id, edge] of edges) if (logicalIds.has(edge.data.logicalId || id)) {
        related.add(id); highlighted.add(edge.data.from); highlighted.add(edge.data.to);
      }
    } else if (selected?.type === 'edge' && edges.has(selected.id)) {
      const edge = edges.get(selected.id).data;
      for (const [id, segment] of edges) if ((segment.data.logicalId || id) === (edge.logicalId || selected.id)) {
        related.add(id); highlighted.add(segment.data.from); highlighted.add(segment.data.to);
      }
    }
    for (const [id, node] of nodes) {
      node.element.classList.toggle('selected', selected?.type === 'node' && selected.id === id);
      node.element.classList.toggle('dim', !!selected && !highlighted.has(id));
      node.element.setAttribute('aria-pressed', String(selected?.type === 'node' && selected.id === id));
    }
    for (const [id, edge] of edges) {
      const hidden = layer !== 'all' && layer !== edge.data.kind;
      edge.element.classList.toggle('selected', selected?.type === 'edge' && selected.id === id);
      edge.element.classList.toggle('dim', !!selected && !related.has(id));
      edge.element.classList.toggle('layer-hidden', hidden);
      edge.element.setAttribute('tabindex', hidden ? '-1' : '0');
    }
  }
  $('layerTabs').addEventListener('click', event => {
    const button = event.target.closest('[data-layer]');
    if (!button) return;
    layer = button.dataset.layer;
    for (const tab of $('layerTabs').children) tab.setAttribute('aria-pressed', String(tab === button));
    if (selected?.type === 'edge' && layer !== 'all' && edges.get(selected.id)?.data.kind !== layer) choose(null);
    updateEmphasis();
  });

  function nodeContent(node, data) {
    node.element.replaceChildren();
    node.data = data;
    node.element.classList.toggle('relay-node', data?.kind === 'relay');
    for (const kind of ['turn', 'hole', 'peer_relay']) node.element.classList.toggle('relay-' + kind, data?.kind === 'relay' && data.relayKind === kind);
    if (node.id === CLOUD) {
      const count = snapshot.members.filter(member => member.signal_online).length;
      node.element.append(icon('cloud'), element('div', 'cloud-name', 'Cloudflare'), element('div', 'cloud-caption', count + ' 台设备直连信令'));
      node.element.setAttribute('aria-label', 'Cloudflare 信令服务，' + count + ' 台设备直接连接，点击查看详情');
      return;
    }
    if (data?.kind === 'relay') {
      const head = element('div', 'node-head'), symbol = element('span', 'device-icon'), text = element('div');
      symbol.append(icon('server'));
      text.append(element('div', 'node-name', data.device_name), element('div', 'node-status', routeLabel({ kind: data.relayKind })));
      head.append(symbol, text);
      node.element.append(head, element('div', 'node-foot', data.relayKind === 'turn' ? '外部 TURN 服务' : '房间中继设备'));
      node.element.setAttribute('aria-label', data.device_name + '，' + routeLabel({ kind: data.relayKind }) + '，点击查看详情');
      return;
    }
    const head = element('div', 'node-head'), symbol = element('span', 'device-icon'), text = element('div');
    text.style.minWidth = '0';
    symbol.append(icon(deviceIcon(data.device_name)));
    const status = element('div', 'node-status' + (data.signal_online ? '' : data.signal_recovering ? ' recovering' : ' delegated'));
    status.append(element('i', 'status-dot'), document.createTextNode(signalLabel(data)));
    text.append(element('div', 'node-name', data.device_name), status);
    head.append(symbol, text);
    const foot = element('div', 'node-foot');
    foot.append(element('span', '', groupLabel(data) + ' · ' + (data.core_version ? 'v' + data.core_version : '版本未上报')));
    if (data.voice) foot.append(icon('mic'));
    node.element.append(head, foot);
    node.element.setAttribute('aria-label', data.device_name + '，' + signalLabel(data) + '，' + groupLabel(data) + '，点击查看详情');
  }
  function reconcileGraph(data, nextEdges) {
    const relayNodes = new Map([...nextEdges.values()].filter(edge => edge.relayNode).map(edge => [edge.relayNode.node_id, edge.relayNode]));
    const nextNodes = new Map([[CLOUD, null], ...data.members.map(member => [deviceKey(member.device_name), member]), ...relayNodes]);
    for (const [id, node] of nodes) if (!nextNodes.has(id)) {
      nodes.delete(id); removeAnimated(node.element);
    }
    for (const [id, member] of nextNodes) {
      let node = nodes.get(id);
      if (!node) {
      const button = element('button', 'graph-node' + (id === CLOUD ? ' cloud-node' : ''));
        button.type = 'button'; button.dataset.nodeKey = id;
        node = { id, element: button, position: null };
        nodes.set(id, node); $('nodes').append(button);
        button.addEventListener('click', () => { if (performance.now() > suppressClickUntil) choose({ type: 'node', id }, button); });
        animate(button, [{ opacity: 0, scale: '.82' }, { opacity: 1, scale: '1' }], 460);
      }
      nodeContent(node, member);
    }
    for (const [id, edge] of edges) if (!nextEdges.has(id)) {
      edges.delete(id); removeAnimated(edge.element);
    }
    for (const [id, data] of nextEdges) {
      let edge = edges.get(id);
      if (!edge) {
        const group = document.createElementNS(ns, 'g'), line = document.createElementNS(ns, 'path'), hit = document.createElementNS(ns, 'path');
        line.setAttribute('class', 'edge-line'); hit.setAttribute('class', 'edge-hit');
        line.setAttribute('aria-hidden', 'true'); hit.setAttribute('aria-hidden', 'true');
        group.setAttribute('role', 'button'); group.setAttribute('tabindex', '0'); group.dataset.edgeKey = id;
        group.append(line, hit); $('edges').append(group);
        edge = { element: group, line, hit, data }; edges.set(id, edge);
        group.addEventListener('click', () => { if (performance.now() > suppressClickUntil) choose({ type: 'edge', id }, group); });
        group.addEventListener('keydown', event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); choose({ type: 'edge', id }, group); } });
        animate(group, [{ opacity: 0 }, { opacity: 1 }], 500);
      }
      edge.data = data;
      const routeClass = data.route?.kind === 'peer_relay' ? ' peer-relay' : data.route ? ' ' + data.route.kind : '';
      edge.element.setAttribute('class', 'edge ' + data.kind + routeClass + (data.record?.blocked ? ' blocked' : data.confirmed ? '' : ' pending'));
      edge.element.setAttribute('aria-label', data.a + ' 与 ' + data.b + '，' + edgeLabel(data) + '，点击查看详情');
    }
  }
  function layoutGraph() {
    if (!snapshot || !$('viewport').clientWidth) return;
    const viewport = $('viewport'), width = viewport.clientWidth, height = viewport.clientHeight;
    const members = [...snapshot.members].sort((a, b) => (a.group_id || '').localeCompare(b.group_id || '') || a.device_name.localeCompare(b.device_name));
    const compact = width < 540, positions = new Map();
    if (compact) {
      world = { width: Math.max(width, 340), height: Math.max(height, 350 + Math.ceil(members.length / 2) * 150) };
      positions.set(CLOUD, { x: world.width / 2, y: 95 });
      members.forEach((member, index) => positions.set(deviceKey(member.device_name), { x: members.length === 1 ? world.width / 2 : index % 2 ? world.width - 86 : 86, y: 252 + Math.floor(index / 2) * 150 + (index % 2) * 44 }));
    } else {
      const rx = Math.max(260, members.length > 6 ? members.length * 34 : Math.min(365, width * .4));
      const ry = members.length > 6 ? Math.max(210, members.length * 22) : 200;
      world = { width: rx * 2 + 210, height: ry * 2 + (members.length > 6 ? 320 : 160) };
      const center = { x: world.width / 2, y: members.length > 6 ? ry + 230 : world.height / 2 };
      // Keep the cloud above device paths so a peer connection cannot appear to terminate there.
      positions.set(CLOUD, { x: center.x, y: members.length === 0 ? center.y - 35 : members.length === 1 ? 155 : 95 });
      const angles = [-155, -25, 155, 25, 115, 65];
      members.forEach((member, index) => {
        const angle = (members.length <= 6 ? angles[index] : -90 + index * 360 / members.length) * Math.PI / 180;
        positions.set(deviceKey(member.device_name), members.length === 1 ? { x: center.x, y: 370 } : { x: center.x + rx * Math.cos(angle), y: center.y + ry * Math.sin(angle) });
      });
    }
    for (const [id, node] of nodes) if (node.data?.kind === 'relay') {
      const edge = [...edges.values()].find(item => item.data.relayNode?.node_id === id)?.data;
      const a = edge && positions.get(deviceKey(edge.a)), b = edge && positions.get(deviceKey(edge.b));
      if (a && b) positions.set(id, { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 + 100 });
    }
    $('scene').style.width = world.width + 'px'; $('scene').style.height = world.height + 'px';
    $('edges').setAttribute('width', world.width); $('edges').setAttribute('height', world.height);
    if (frame) cancelAnimationFrame(frame);
    const starts = new Map([...nodes].map(([id, node]) => [id, node.position || positions.get(id)]));
    const startTime = performance.now(), duration = reducedMotion.matches ? 0 : 520;
    function tick(time) {
      const progress = duration ? Math.min(1, (time - startTime) / duration) : 1, ease = 1 - Math.pow(1 - progress, 3);
      for (const [id, target] of positions) {
        const node = nodes.get(id); if (!node) continue;
        const from = starts.get(id) || target;
        node.position = { x: from.x + (target.x - from.x) * ease, y: from.y + (target.y - from.y) * ease };
        node.element.style.left = node.position.x + 'px'; node.element.style.top = node.position.y + 'px';
      }
      for (const edge of edges.values()) {
        const from = nodes.get(edge.data.from)?.position, to = nodes.get(edge.data.to)?.position;
        if (!from || !to) continue;
        const dx = to.x - from.x, dy = to.y - from.y, length = Math.hypot(dx, dy) || 1;
        const bend = edge.data.kind === 'signal' ? 0 : Math.min(40, length * .08);
        const c1 = compact ? { x: from.x, y: from.y + dy * .45 } : { x: from.x + dx * .38 - dy / length * bend, y: from.y + dy * .15 + dx / length * bend };
        const c2 = compact ? { x: to.x, y: to.y - dy * .45 } : { x: to.x - dx * .38 - dy / length * bend, y: to.y - dy * .15 + dx / length * bend };
        const path = 'M' + from.x + ' ' + from.y + 'C' + c1.x + ' ' + c1.y + ' ' + c2.x + ' ' + c2.y + ' ' + to.x + ' ' + to.y;
        edge.line.setAttribute('d', path); edge.hit.setAttribute('d', path);
      }
      frame = progress < 1 ? requestAnimationFrame(tick) : 0;
    }
    tick(startTime);
    if (!manualCamera) fitCamera(false, compact);
  }
  function applyCamera(transition = false) {
    $('scene').style.transition = transition && !reducedMotion.matches ? 'transform 260ms var(--easing)' : 'none';
    $('scene').style.transform = 'translate(' + camera.x + 'px,' + camera.y + 'px) scale(' + camera.scale + ')';
    $('zoomLevel').textContent = Math.round(camera.scale * 100) + '%';
  }
  function fitCamera(transition = true, comfortable = false) {
    const width = $('viewport').clientWidth, height = $('viewport').clientHeight;
    if (!width || !height) return;
    const scale = comfortable ? Math.min(1, (width - 16) / world.width) : Math.min(1, (width - 32) / world.width, (height - 34) / world.height);
    camera = { scale, x: (width - world.width * scale) / 2, y: comfortable ? 6 : (height - world.height * scale) / 2 };
    applyCamera(transition);
  }
  function zoom(factor, x = $('viewport').clientWidth / 2, y = $('viewport').clientHeight / 2, transition = true) {
    const scale = Math.max(.22, Math.min(2, camera.scale * factor)), ratio = scale / camera.scale;
    camera.x = x - (x - camera.x) * ratio; camera.y = y - (y - camera.y) * ratio; camera.scale = scale;
    manualCamera = true; applyCamera(transition); $('tooltip').hidden = true;
  }
  $('zoomIn').addEventListener('click', () => zoom(1.2));
  $('zoomOut').addEventListener('click', () => zoom(1 / 1.2));
  $('fitGraph').addEventListener('click', () => { manualCamera = false; fitCamera(); });
  new ResizeObserver(() => { if (snapshot && !$('dashboard').hidden) { manualCamera = false; layoutGraph(); } }).observe($('viewport'));

  const viewport = $('viewport');
  viewport.addEventListener('pointerdown', event => {
    if (event.button !== 0 || event.target.closest('#zoomControls')) return;
    pointers.set(event.pointerId, { x: event.clientX, y: event.clientY, startX: event.clientX, startY: event.clientY });
    $('tooltip').hidden = true;
  });
  viewport.addEventListener('pointermove', event => {
    const point = pointers.get(event.pointerId);
    if (!point) { tooltip(event); return; }
    const before = [...pointers.values()].map(p => ({ ...p })), dx = event.clientX - point.x, dy = event.clientY - point.y;
    point.x = event.clientX; point.y = event.clientY;
    if (!dragging && pointers.size < 2 && Math.hypot(point.x - point.startX, point.y - point.startY) < 5) return;
    dragging = true; manualCamera = true; viewport.classList.add('dragging');
    if (!viewport.hasPointerCapture(event.pointerId)) viewport.setPointerCapture(event.pointerId);
    if (pointers.size === 2) {
      const after = [...pointers.values()], distance = Math.hypot(before[0].x - before[1].x, before[0].y - before[1].y);
      const nextDistance = Math.hypot(after[0].x - after[1].x, after[0].y - after[1].y), rect = viewport.getBoundingClientRect();
      const midX = (before[0].x + before[1].x) / 2 - rect.left, midY = (before[0].y + before[1].y) / 2 - rect.top;
      zoom(nextDistance / Math.max(1, distance), midX, midY, false);
      camera.x += dx / 2; camera.y += dy / 2;
    } else { camera.x += dx; camera.y += dy; }
    applyCamera();
  });
  function endPointer(event) {
    if (!pointers.delete(event.pointerId)) return;
    if (viewport.hasPointerCapture(event.pointerId)) viewport.releasePointerCapture(event.pointerId);
    if (dragging) suppressClickUntil = performance.now() + 260;
    if (!pointers.size) { dragging = false; viewport.classList.remove('dragging'); }
  }
  addEventListener('pointerup', endPointer);
  addEventListener('pointercancel', endPointer);
  viewport.addEventListener('lostpointercapture', event => {
    // A touch's implicit capture moves from a child node to the viewport when dragging starts.
    if (event.target === viewport) endPointer(event);
  });
  viewport.addEventListener('pointerleave', () => { $('tooltip').hidden = true; });
  viewport.addEventListener('wheel', event => {
    if (!event.ctrlKey && !event.metaKey) return;
    event.preventDefault();
    const rect = viewport.getBoundingClientRect();
    zoom(Math.exp(-event.deltaY * .008), event.clientX - rect.left, event.clientY - rect.top, false);
  }, { passive: false });
  viewport.addEventListener('keydown', event => {
    if (event.target !== viewport) return;
    if (event.key === '+' || event.key === '=') zoom(1.2);
    else if (event.key === '-') zoom(1 / 1.2);
    else if (event.key === '0' || event.key === 'Home') { manualCamera = false; fitCamera(); }
    else if (['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(event.key)) {
      camera.x += event.key === 'ArrowLeft' ? 40 : event.key === 'ArrowRight' ? -40 : 0;
      camera.y += event.key === 'ArrowUp' ? 40 : event.key === 'ArrowDown' ? -40 : 0;
      manualCamera = true; applyCamera(true);
    } else return;
    event.preventDefault();
  });
  function tooltip(event) {
    if (event.pointerType !== 'mouse' || dragging || selected && smallScreen.matches) return;
    const target = event.target.closest('[data-node-key],[data-edge-key]'), tip = $('tooltip');
    if (!target) { tip.hidden = true; return; }
    const node = nodes.get(target.dataset.nodeKey), edge = edges.get(target.dataset.edgeKey)?.data;
    if (node) tip.textContent = node.id === CLOUD ? 'Cloudflare · 信令服务\n点击查看直连设备与 Worker 版本' : node.data?.kind === 'relay' ? node.data.device_name + ' · ' + routeLabel({ kind: node.data.relayKind }) + '\n点击查看中继详情' : node.data.device_name + '\n' + signalLabel(node.data) + ' · ' + groupLabel(node.data) + '\n点击查看设备详情';
    else if (edge) tip.textContent = edge.a + ' ↔ ' + edge.b + '\n' + edgeLabel(edge) + '\n点击展开路径详情';
    else { tip.hidden = true; return; }
    tip.hidden = false;
    const rect = viewport.getBoundingClientRect();
    tip.style.left = Math.max(8, Math.min(event.clientX - rect.left + 14, rect.width - tip.offsetWidth - 8)) + 'px';
    tip.style.top = Math.max(8, Math.min(event.clientY - rect.top + 16, rect.height - tip.offsetHeight - 8)) + 'px';
  }

  function recordChanges(previous, data, nextEdges) {
    const changes = [];
    if (!previous) changes.push('已订阅房间状态');
    else {
      const old = new Map(previous.members.map(member => [member.device_name, member]));
      const next = new Set(data.members.map(member => member.device_name));
      for (const member of data.members) {
        const before = old.get(member.device_name);
        if (!before) changes.push(member.device_name + ' 加入房间');
        else if (signalLabel(before) !== signalLabel(member)) changes.push(member.device_name + ' · ' + signalLabel(member));
      }
      for (const member of previous.members) if (!next.has(member.device_name)) changes.push(member.device_name + ' 离开房间');
      const changed = new Set();
      for (const [id, edge] of nextEdges) if (edge.kind === 'transport') {
        const logicalId = edge.logicalId || id;
        if (changed.has(logicalId)) continue;
        changed.add(logicalId);
        const before = [...edges.values()].find(item => (item.data.logicalId || item.data.id) === logicalId)?.data;
        if (!before || edgeLabel(before) !== edgeLabel(edge)) changes.push(edge.a + ' ↔ ' + edge.b + ' · ' + edgeLabel(edge));
      }
      const nextLogicalIds = new Set([...nextEdges.values()].filter(edge => edge.kind === 'transport').map(edge => edge.logicalId || edge.id));
      const removed = new Set();
      for (const [id, edge] of edges) if (edge.data.kind === 'transport') {
        const logicalId = edge.data.logicalId || id;
        if (!nextLogicalIds.has(logicalId) && !removed.has(logicalId)) { changes.push(edge.data.a + ' ↔ ' + edge.data.b + ' · 连接移除'); removed.add(logicalId); }
      }
    }
    if (!changes.length) return;
    const time = new Date();
    feed = [...changes.reverse().map(text => ({ text, time })), ...feed].slice(0, 4);
    $('feedEvents').replaceChildren();
    for (const event of feed) {
      const row = element('div', 'feed-event'), when = element('time', '', event.time.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }));
      when.dateTime = event.time.toISOString();
      row.append(element('i', 'status-dot'), element('span', '', event.text), when);
      $('feedEvents').append(row);
    }
  }
  function render(data) {
    const previous = snapshot, nextEdges = buildEdges(data);
    recordChanges(previous, data, nextEdges);
    snapshot = data;
    $('roomTitle').textContent = roomName;
    const count = edgeCount([...nextEdges.values()].filter(edge => edge.kind === 'transport' && edge.confirmed));
    $('roomSummary').textContent = data.members.length + ' 台设备 · ' + count + ' 条已确认连接 · ' + data.groups.length + ' 个连通分组';
    $('footerCaption').textContent = 'Worker ' + (data.worker_version || '版本未上报') + ' · 只读面板';
    $('mapEmpty').hidden = data.members.length !== 0;
    $('mapCard').dataset.stale = 'false';
    reconcileGraph(data, nextEdges);
    if (selected && !(selected.type === 'node' ? nodes : edges).has(selected.id)) choose(null);
    layoutGraph(); renderDetails(); updateEmphasis();
  }
  function resetGraph() {
    if (frame) cancelAnimationFrame(frame);
    frame = 0; snapshot = null; selected = null; returnFocus = null; nodes.clear(); edges.clear(); feed = [];
    $('nodes').replaceChildren(); $('edges').replaceChildren(); $('feedEvents').replaceChildren(); $('detailContent').replaceChildren();
    $('tooltip').hidden = true; $('inspector').classList.remove('is-open');
    $('footerCaption').textContent = '房间连接面板';
    manualCamera = false; pointers.clear(); dragging = false; viewport.classList.remove('dragging');
    layer = 'all';
    for (const tab of $('layerTabs').children) tab.setAttribute('aria-pressed', String(tab.dataset.layer === 'all'));
    syncSheet();
  }

  function api(path, body = {}) {
    const next = requestChain.then(async () => {
      const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 12000);
      try {
        const response = await fetch(location.pathname + '/' + path, { method: 'POST', credentials: 'same-origin', cache: 'no-store', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: controller.signal });
        if (!response.ok) { const error = new Error('request failed'); error.status = response.status; throw error; }
        return await response.json();
      } finally { clearTimeout(timer); }
    });
    requestChain = next.catch(() => {});
    return next;
  }
  function sessionExpiry(expires) {
    clearTimeout(expiryTimer);
    expiryTimer = setTimeout(expired, Math.max(0, expires - Date.now()));
  }
  function stopWatching() {
    epoch++; watching = false; clearTimeout(retryTimer); clearTimeout(connectionTimer); retryTimer = null; connectionTimer = null;
    const previous = socket; socket = null;
    if (previous) previous.close();
    busy('roomSubmit', false);
  }
  function resetLogin(focus = true) {
    stopWatching(); clearTimeout(expiryTimer); roomName = ''; retryAttempt = 0;
    $('authForm').reset(); $('roomForm').reset(); $('authError').textContent = ''; $('roomError').textContent = '';
    $('password').removeAttribute('aria-invalid'); $('token').removeAttribute('aria-invalid');
    hidePasswords(); resetGraph(); screen('auth'); setState('尚未连接');
    busy('authSubmit', false);
    $('restoreRetry').hidden = true;
    if (focus) $('password').focus({ preventScroll: true });
  }
  async function logout() {
    stopWatching();
    const currentEpoch = epoch;
    $('logout').disabled = true; $('backToAuth').disabled = true;
    busy('authSubmit', true, '正在退出…'); busy('roomSubmit', true, '正在退出…');
    try {
      await api('logout');
      if (epoch === currentEpoch) resetLogin();
    } catch (error) {
      if (epoch !== currentEpoch) return;
      if (error.status === 401) resetLogin();
      else { screen('room'); $('roomError').textContent = '退出未完成，请检查网络后再次点击退出。'; setState('退出未完成', 'error'); }
    } finally {
      $('logout').disabled = false; $('backToAuth').disabled = false;
      busy('authSubmit', false); busy('roomSubmit', false);
    }
  }
  function expired() {
    resetLogin(); $('authError').textContent = '登录已过期或失效，请重新输入密码。';
  }
  function roomFailed(message, invalid = false) {
    stopWatching(); resetGraph(); screen('room'); setState('未连接', 'error');
    $('roomId').value = roomName;
    $('roomError').textContent = message;
    $('token').setAttribute('aria-invalid', String(invalid)); $('token').focus({ preventScroll: true });
  }
  function scheduleRetry() {
    if (!watching) return;
    setState('正在重新连接', 'busy');
    $('mapCard').dataset.stale = 'true';
    $('footerCaption').textContent = '连接已中断 · 保留最后收到的状态';
    clearTimeout(retryTimer);
    retryTimer = setTimeout(() => refreshAndConnect(), Math.min(15000, 1500 * 2 ** Math.min(retryAttempt++, 4)));
  }
  async function refreshAndConnect() {
    const currentEpoch = epoch;
    try {
      const session = await api('session');
      if (!watching || epoch !== currentEpoch) return;
      sessionExpiry(session.expires);
      if (!session.room) { roomFailed(session.roomExpired ? '房间验证已失效，请重新输入房间密码。' : '请选择房间并输入房间密码。'); return; }
      if (roomName !== session.room) resetGraph();
      roomName = session.room; connect();
    } catch (error) {
      if (!watching || epoch !== currentEpoch) return;
      if (error.status === 401 || error.status === 403) expired();
      else if (snapshot) scheduleRetry();
      else roomFailed('暂时无法连接信令服务，请稍后重试。');
    }
  }
  function connect() {
    if (!watching) return;
    setState(snapshot ? '正在重新连接' : '正在连接房间', 'busy');
    const url = new URL(location.pathname + '/live', location.href);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    let current;
    try { current = new WebSocket(url); }
    catch { if (snapshot) scheduleRetry(); else roomFailed('无法建立房间连接，请稍后重试。'); return; }
    socket = current;
    connectionTimer = setTimeout(() => {
      if (socket !== current) return;
      if (snapshot) current.close();
      else roomFailed('房间连接超时，请检查网络后重试。');
    }, 12000);
    current.onopen = () => { if (socket === current) current.send(JSON.stringify({ type: 'watch' })); };
    current.onmessage = event => {
      if (socket !== current) return;
      let data; try { data = JSON.parse(event.data); } catch { return; }
      if (data.type === 'error') { roomFailed(data.code === 'panel_auth_failed' ? '房间不存在或房间密码错误。' : '无法订阅房间，请稍后重试。', data.code === 'panel_auth_failed'); return; }
      if (data.type !== 'panel_snapshot') return;
      clearTimeout(connectionTimer); connectionTimer = null; retryAttempt = 0;
      busy('roomSubmit', false); screen('dashboard'); render(data); setState('已连接房间', 'live');
    };
    current.onclose = () => {
      if (socket !== current) return;
      socket = null; clearTimeout(connectionTimer); connectionTimer = null;
      if (!watching) return;
      if (snapshot) scheduleRetry();
      else roomFailed('无法连接房间，请检查网络或稍后重试。');
    };
    current.onerror = () => {};
  }
  $('authForm').addEventListener('submit', async event => {
    event.preventDefault();
    if ($('authSubmit').disabled) return;
    $('authError').textContent = ''; $('password').removeAttribute('aria-invalid');
    busy('authSubmit', true, '正在验证…');
    const currentEpoch = epoch;
    try {
      const pending = api('auth', { password: $('password').value, remember: $('remember').checked });
      $('password').value = ''; hidePasswords();
      const session = await pending;
      if (epoch !== currentEpoch) return;
      sessionExpiry(session.expires); $('restoreRetry').hidden = true;
      screen('room'); setState('信令已验证', 'live'); $('roomId').focus({ preventScroll: true });
    } catch (error) {
      if (epoch !== currentEpoch) return;
      const invalid = error.status === 401;
      $('authError').textContent = invalid ? '信令密码不正确，请重新输入。' : '验证失败，请检查网络或信令服务配置。';
      $('password').setAttribute('aria-invalid', String(invalid));
    } finally { if (epoch === currentEpoch) busy('authSubmit', false); }
  });
  $('roomForm').addEventListener('submit', async event => {
    event.preventDefault();
    if ($('roomSubmit').disabled) return;
    stopWatching(); resetGraph(); $('roomError').textContent = ''; $('token').removeAttribute('aria-invalid');
    roomName = $('roomId').value;
    const currentEpoch = epoch;
    busy('roomSubmit', true, '正在进入房间…'); setState('正在连接房间', 'busy');
    try {
      const pending = api('room', { room: $('roomId').value, token: $('token').value });
      $('token').value = ''; hidePasswords();
      const session = await pending;
      if (epoch !== currentEpoch) return;
      roomName = session.room; sessionExpiry(session.expires);
      watching = true; retryAttempt = 0; connect();
    } catch (error) {
      if (epoch !== currentEpoch) return;
      if (error.status === 401) expired();
      else roomFailed(error.status === 403 ? '房间不存在或房间密码错误。' : '暂时无法进入房间，请稍后重试。', error.status === 403);
    }
  });
  $('switchRoom').addEventListener('click', async () => {
    stopWatching(); resetGraph(); screen('room');
    const currentEpoch = epoch;
    busy('roomSubmit', true, '正在切换房间…'); $('roomError').textContent = ''; $('token').value = '';
    try {
      await api('room', { room: '' });
      if (epoch !== currentEpoch) return;
      roomName = ''; setState('信令已验证', 'live'); $('roomId').focus({ preventScroll: true });
    } catch (error) {
      if (epoch !== currentEpoch) return;
      if (error.status === 401) expired();
      else $('roomError').textContent = '切换未完成，请检查网络；重新打开时仍会恢复原房间。';
    } finally { if (epoch === currentEpoch) busy('roomSubmit', false); }
  });
  async function restore() {
    resetLogin(false);
    const currentEpoch = epoch;
    busy('authSubmit', true, '正在恢复登录…'); setState('正在恢复登录', 'busy');
    try {
      const session = await api('session');
      if (epoch !== currentEpoch) return;
      sessionExpiry(session.expires); roomName = session.room;
      if (roomName) { watching = true; connect(); }
      else {
        screen('room'); setState('信令已验证', 'live');
        if (session.roomExpired) $('roomError').textContent = '房间验证已失效，请重新输入房间信息。';
      }
    } catch (error) {
      if (epoch !== currentEpoch) return;
      setState('尚未连接');
      if (error.status !== 401) {
        $('authError').textContent = '暂时无法恢复登录，请检查网络后重试。'; $('restoreRetry').hidden = false;
      }
    } finally { if (epoch === currentEpoch) busy('authSubmit', false); }
  }
  $('backToAuth').addEventListener('click', () => logout());
  $('logout').addEventListener('click', () => logout());
  $('restoreRetry').addEventListener('click', restore);
  addEventListener('pagehide', () => resetLogin(false));
  addEventListener('pageshow', event => { if (event.persisted) restore(); });
  restore();
})();
</script>
</body>
</html>`;
