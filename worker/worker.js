import { RoomICE, TurnBroker, iceJoinFields, negotiateProfile, recordPhase, ICE_PROFILE, FIXED_PROFILE, RELAY_ONLY_POLICY, VOICE_MEMBER_LIMIT, HOLE_VERSION } from "./worker_ice.mjs";
import { RoomControl } from "./worker_room.mjs";
import { WORKER_VERSION, modernMember } from "./software_version.mjs";
import { panelPath, panelPage, panelSession, validPanelSession, sameOrigin } from "./worker_panel.mjs";
export { TurnBroker };
const ROOM_PATH = "/ws";
const MAX_MESSAGE_SIZE = 256 * 1024;

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    const panel = panelPath(env.PANEL_PATH);
    if (panel && url.pathname === panel && request.method === 'GET') return panelPage();
    if (panel && url.pathname === panel + '/auth') {
      if (request.method !== 'POST' || !sameOrigin(request)) return new Response('Forbidden', { status: 403 });
      let password;
      try {
        const raw = await request.text();
        if (raw.length > 8192) return new Response('request too large', { status: 413 });
        password = JSON.parse(raw).password;
      } catch { return new Response('invalid request', { status: 400 }); }
      if (!await passwordMatches(password, env.PASSWORD)) return new Response('unauthorized', { status: 401 });
      const broker = env.ROOMS.get(env.ROOMS.idFromName('$hole-panel-sessions'));
      return broker.fetch(new Request('https://panel-internal/__panel_session', { method: 'POST', body: JSON.stringify({ issue: true, password: env.PASSWORD }) }));
    }
    if (panel && url.pathname === panel + '/live') {
      if (!sameOrigin(request) || request.headers.get('Upgrade')?.toLowerCase() !== 'websocket') return new Response('Forbidden', { status: 403 });
      const protocol = request.headers.get('Sec-WebSocket-Protocol') ?? '';
      if (!/^hole\.[0-9]{10}\.[a-f0-9]{32}\.[a-f0-9]{64}$/.test(protocol)) return new Response('unauthorized', { status: 401 });
      const broker = env.ROOMS.get(env.ROOMS.idFromName('$hole-panel-sessions'));
      const check = await broker.fetch(new Request('https://panel-internal/__panel_session', { method: 'POST', body: JSON.stringify({ session: protocol, password: env.PASSWORD }) }));
      if (!check.ok) return new Response('unauthorized', { status: 401 });
      const room = url.searchParams.get('room');
      if (!room || room.length > 128) return new Response('room is required', { status: 400 });
      return env.ROOMS.get(env.ROOMS.idFromName(room)).fetch(request);
    }

    if (url.pathname !== ROOM_PATH) {
      return new Response("Not found", { status: 404 });
    }

    // 密码门禁放在最前面：校验失败时绝不去解析 room、更不去触碰 DO，
    // 否则每个扫到 /ws 的请求都会实例化一个 Durable Object 白白消耗额度。
    if (!(await authorized(request, env))) {
      return new Response("unauthorized", { status: 401 });
    }

    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return new Response("WebSocket upgrade required", { status: 426 });
    }

    const room = url.searchParams.get("room");
    if (!room || room.length > 128) {
      return new Response("room is required", { status: 400 });
    }

    const id = env.ROOMS.idFromName(room);
    return env.ROOMS.get(id).fetch(request);
  },
};

// 用 wrangler secret put PASSWORD 设置。未配置时拒绝一切（fail closed）：
// 部署完忘了配密码就等于门禁不存在，宁可全员 401 也不能裸奔。
async function authorized(request, env) {
  const expected = env.PASSWORD;
  if (typeof expected !== "string" || expected.length === 0) {
    console.error("PASSWORD secret 未配置，拒绝所有信令连接");
    return false;
  }
  const header = request.headers.get("Authorization") ?? "";
  const [scheme, ...rest] = header.split(" ");
  if (scheme?.toLowerCase() !== "bearer" || rest.length === 0) return false;
  return passwordMatches(rest.join(" "), expected);
}

async function passwordMatches(password, expected) {
  if (typeof password !== 'string' || typeof expected !== 'string' || !expected) return false;
  const presented = new TextEncoder().encode(password);
  const wanted = new TextEncoder().encode(expected);
  if (presented.length !== wanted.length) return false;
  try {
    // workerd 提供的常数时间比较，避免逐字节比较泄露时序信息。
    return crypto.subtle.timingSafeEqual(presented, wanted);
  } catch {
    return false;
  }
}

// 成员声明、设备连接图和传输记录保存在 storage。
// WebSocket attachment 只保存设备名，断开 WS 不代表设备连接已失效。
// Hibernation 唤醒时构造函数会重新执行，内存里的 members/roomToken
// 都会丢失，每个事件入口先 loadState() 从持久层重建。
// attachment 只存设备名而不是整个成员数据：官方建议 attachment 只放小值和 key，
// 大对象走 Storage API。
export class Room {
  constructor(state, env = {}) {
    this.state = state;
    this.ice = new RoomICE(this, state, env);
    this.members = new Map();
    this.registered = new Map();
    this.control = new RoomControl(this, state);
    this.joining = new Map();
    this.roomToken = null;
    this.loaded = false;
    this.panelLast = '';
  }

  async fetch(request) {
    if (new URL(request.url).pathname === '/__panel_session' && request.method === 'POST') {
      const data = await request.json();
      if (!this.panelKeyPromise) this.panelKeyPromise = (async () => {
        let key = await this.state.storage.get('panel_session_key');
        if (!key) {
          key = [...crypto.getRandomValues(new Uint8Array(32))].map(n => n.toString(16).padStart(2, '0')).join('');
          await this.state.storage.put('panel_session_key', key);
        }
        return key;
      })();
      const key = await this.panelKeyPromise;
      if (data.issue === true) return Response.json({ session: await panelSession(key, data.password) }, { headers: { 'Cache-Control': 'no-store' } });
      return new Response(null, { status: await validPanelSession(data.session, key, data.password) ? 204 : 401 });
    }
    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return new Response("WebSocket upgrade required", { status: 426 });
    }

    await this.loadState();
    await this.ice.setRoomName(new URL(request.url).searchParams.get("room"));
    const isPanel = new URL(request.url).pathname !== ROOM_PATH;
    if (isPanel && this.state.getWebSockets().filter(ws => panelAttachment(ws)).length >= 32) return new Response('too many viewers', { status: 429 });
    const pair = new WebSocketPair();
    const client = pair[0];
    const server = pair[1];
    // acceptWebSocket 而非 ws.accept()：socket 关联到 DO 并允许休眠，
    // 空闲时实例被逐出内存但连接不断。
    this.state.acceptWebSocket(server);
    if (isPanel) server.serializeAttachment({ panel: 'pending' });
    return new Response(null, { status: 101, webSocket: client, ...(isPanel ? { headers: { 'Sec-WebSocket-Protocol': request.headers.get('Sec-WebSocket-Protocol') } } : {}) });
  }

  // loadState 从 storage 重建休眠期间丢失的内存状态。
  // Share one reconstruction across events, including asynchronous key imports.
  async loadState() {
    if (this.loaded) return;
    if (!this.loading) this.loading = this.restoreState().finally(() => { this.loading = null; });
    return this.loading;
  }

  async restoreState() {
    this.roomToken = (await this.state.storage.get("token")) ?? null;
    this.members.clear();
    this.registered.clear();
    for (const [, member] of await this.state.storage.list({ prefix: 'member:' })) {
      if (member?.device_name && member.signal_version === 2 && member.transport_profiles?.every(profile => [ICE_PROFILE, FIXED_PROFILE].includes(profile))) this.registered.set(member.device_name, { member, ws: null });
    }
    for (const ws of this.state.getWebSockets()) {
      const name = memberName(ws);
      if (name === null || this.members.has(name)) continue;
      const member = await this.state.storage.get(memberKey(name));
      if (member && this.registered.has(name)) {
        const entry = { ws, member };
        this.members.set(name, entry);
        this.registered.set(name, entry);
      } else {
        detach(ws);
        ws.close(4002, 'unsupported client protocol');
      }
    }
    await this.ice.init();
    await this.control.init();
    this.control.rebuild();
    // Reconcile persisted topology against the sockets that actually survived
    // hibernation, including closes whose attachment is already gone.
    const resumeNames = new Set();
    for (const record of this.ice.records.values()) {
      if (!this.members.has(record.a)) resumeNames.add(record.a);
      if (!this.members.has(record.b)) resumeNames.add(record.b);
    }
    await this.reconcile(resumeNames);
    this.loaded = true;
  }

  async alarm() {
    await this.loadState();
    await this.ice.expireOffline();
    await this.pruneMembers();
    this.broadcastPanel();
  }

  async webSocketMessage(ws, raw) {
    await this.loadState();
    if (typeof raw !== "string") return;
    if (raw.length > MAX_MESSAGE_SIZE) {
      sendError(ws, "message_too_large", "message is too large");
      return;
    }

    let message;
    try {
      message = JSON.parse(raw);
    } catch {
      sendError(ws, "invalid_json", "message must be valid JSON");
      return;
    }

    if (!message || typeof message !== "object" || typeof message.type !== "string") { sendError(ws, "invalid_json", "message must be an object with a type"); return; }
    const panel = panelAttachment(ws);
    if (panel) {
      if (panel === 'pending' && message.type === 'watch' && typeof message.token === 'string' && this.roomToken !== null && message.token === this.roomToken) {
        ws.serializeAttachment({ panel: 'watching' });
        send(ws, this.panelSnapshot());
      } else {
        sendError(ws, 'panel_auth_failed', '房间不存在或房间密码错误');
        ws.close(1008, 'panel authorization failed');
      }
      return;
    }
    if (message.signal_version === 2 && message.type !== 'room_graph' && new TextEncoder().encode(raw).length > 64 * 1024) { sendError(ws, "message_too_large", "ICE message exceeds 64 KiB"); return; }
    try {
      if (message.type === "join") {
        await this.handleJoin(ws, message);
        return;
      }

      const name = memberName(ws);
      const entry = name !== null ? this.members.get(name) : undefined;
      if (!entry || entry.ws !== ws) {
        sendError(ws, "not_joined", "join before sending other messages");
        return;
      }

      if (message.type === "member_update") {
        await this.handleMemberUpdate(ws, entry, message);
        return;
      }

      if (message.type === 'room_graph') {
        if (!modernMember(entry.member) || !this.ice.takeRate(ws)) { sendError(ws, 'room_graph_rejected', '房间图上报未通过版本或频率检查'); return; }
        if (await this.control.update(message.room_frames)) {
          this.control.rebuild();
          await this.broadcastRoomState();
          await this.reconcile();
        }
        return;
      }

      if (message.type === 'room_pause') {
        // Ignore legacy pause requests and keep their authenticated socket alive.
        return;
      }

      if (await this.ice.handle(ws, entry.member, message)) return;

      if (message.type === "leave") {
        await this.state.storage.delete(memberKey(name));
        this.registered.delete(name);
        this.control.nodes.delete(name);
        this.control.rebuild();
        await this.broadcastRoomState();
        await this.reconcile();
        ws.close(1000, "left room");
        return;
      }

      sendError(ws, "unknown_message", "unknown message type");
    } catch (error) {
      sendError(ws, "server_error", error instanceof Error ? error.message : "server error");
    } finally {
      await this.control.flush();
      this.broadcastPanel();
    }
  }

  async handleJoin(ws, message) {
    if (memberName(ws) !== null || this.joining.has(ws)) {
      sendError(ws, "already_joined", "this socket already joined");
      return;
    }

    this.joining.set(ws, message.device_name);
    try {
      await this.joinMember(ws, message);
    } finally {
      this.joining.delete(ws);
    }
  }

  async joinMember(ws, message) {
    const member = validateJoin(message);
    if (!member) {
      sendError(ws, "invalid_join", "invalid join message");
      return;
    }

    if (this.roomToken !== null && member.token !== this.roomToken) {
      sendError(ws, "invalid_token", "token does not match this room");
      ws.close(1008, "invalid token");
      return;
    }

    if (modernMember(member)) {
      try { await this.control.certificate(member); await this.control.ensureKey(); }
      catch { sendError(ws, 'invalid_certificate', '设备证书与声明的指纹不一致'); return; }
    }

    if (this.ice.staleJoin(member)) { sendError(ws, "stale_client_epoch", "旧网络代次已失效"); ws.close(4001, "stale client epoch"); return; }
    if ((!this.members.has(member.device_name) && this.members.size >= 16) || (!this.registered.has(member.device_name) && this.registered.size >= 128)) { sendError(ws, "member_limit", "房间成员记录达到上限"); return; }

    // Certificate and key initialization can let another join claim the room.
    // Check and claim its token before yielding or replacing any membership.
    if (this.roomToken !== null && member.token !== this.roomToken) {
      sendError(ws, "invalid_token", "token does not match this room");
      ws.close(1008, "invalid token");
      return;
    }
    if (this.roomToken === null) {
      this.roomToken = member.token;
      await this.state.storage.put("token", member.token);
    }

    // Allow a reconnect to replace a stale WebSocket session.
    const existing = this.registered.get(member.device_name);
    if (existing) {
      try {
        existing.ws?.close(4001, "replaced by reconnect");
      } catch {
        // The old socket may already be closed.
      }
      // 服务端 close 后 socket 可能仍以 CLOSING 状态留在 getWebSockets 里，
      // 清空 attachment 让后续重建把它排除掉。
      detach(existing.ws);
      this.members.delete(member.device_name);
      // A same-runtime signal reconnect can replace its WebSocket before the
      // close callback runs. Keep an unchanged authenticated ICE generation;
      // the new socket will replay it through transport_sync. A changed
      // declaration still takes the normal fresh-generation path.
      const sameMappings = JSON.stringify(existing.member.provide.map(item => item.id).sort()) === JSON.stringify(member.provide.map(item => item.id).sort()) &&
        JSON.stringify(existing.member.consume.map(item => item.id).sort()) === JSON.stringify(member.consume.map(item => item.id).sort());
      const sameDeclaration = sameResumeDeclaration(existing.member, member);
      const resumable = sameMappings && sameDeclaration && (modernMember(member) || this.ice.hasOnlineTransport(member.device_name, member));
      if (!modernMember(member) && !resumable && sameMappings) await this.ice.markOfflineForReconnect(member.device_name);
      if (existing.member.runtime_id !== member.runtime_id || existing.member.cert_fingerprint !== member.cert_fingerprint) this.control.nodes.delete(member.device_name);
    }

    await this.state.storage.put(memberKey(member.device_name), member);
    ws.serializeAttachment({ device_name: member.device_name });
    this.members.set(member.device_name, { ws, member });
    this.registered.set(member.device_name, { ws, member });
    this.control.rebuild();

    send(ws, {
      type: "joined",
      signal_version: 2,
      auth_mode: "shared-secret",
      lease_renewal: !modernMember(member),
      live_config: true,
      signal_resume: true,
      worker_version: WORKER_VERSION,
      state_revision: String(this.control.revision),
      ...(modernMember(member) ? { room_public_key: this.control.publicKey } : {}),
      hole_version: HOLE_VERSION,
      relay_only_policy: RELAY_ONLY_POLICY,
      room_members: [...this.members.keys()],
    });
    await this.broadcastRoomState();
    await this.reconcile();
    this.ice.resumePending();
  }

  async handleMemberUpdate(ws, entry, message) {
    const reject = (code, text) => send(ws, {type:"error", code, message:text, request_id:message.request_id});
    if (!this.ice.takeRate(ws)) { reject("rate_limited", "配置更新过于频繁"); return; }
    const previous = entry.member;
    const member = validateJoin(message);
    if (!member || member.signal_version !== 2 || typeof message.request_id !== "string" ||
        !/^[a-f0-9]{32}$/.test(message.request_id) || typeof message.member_revision !== "string" ||
        !/^[1-9][0-9]{0,18}$/.test(message.member_revision)) {
      reject("invalid_member_update", "在线配置声明无效"); return;
    }
    if (["device_name", "token", "runtime_id", "cert_fingerprint"].some(key => member[key] !== previous[key]) ||
        BigInt(member.transport_epoch) < BigInt(previous.transport_epoch)) {
      reject("member_identity_changed", "在线更新不能替换身份或回退网络代次"); return;
    }
    const revision = BigInt(message.member_revision), current = BigInt(previous.member_revision ?? "0");
    if (revision <= current) {
      if (revision === current && message.request_id === previous.update_request_id) {
        send(ws, {type:"member_updated", request_id:message.request_id, member_revision:message.member_revision});
      } else reject("stale_member_revision", "配置修订已失效");
      return;
    }
    member.member_revision = message.member_revision;
    member.update_request_id = message.request_id;
    await this.state.storage.put(memberKey(member.device_name), member);
    entry.member = member;
    this.registered.set(member.device_name, entry);
    this.control.rebuild();
    // ACK precedes all topology notifications for this accepted declaration.
    send(ws, {type:"member_updated", request_id:message.request_id, member_revision:message.member_revision});
    await this.broadcastRoomState();
    await this.reconcile();
  }

  async webSocketClose(ws, code, reason, wasClean) {
    await this.loadState();
    await this.removeMember(ws, !wasClean && code !== 1000);
    await this.control.flush();
    this.broadcastPanel();
  }

  async webSocketError(ws, error) {
    await this.loadState();
    await this.removeMember(ws, true);
    await this.control.flush();
    this.broadcastPanel();
  }

  panelSnapshot() {
    const graph = this.control.snapshot();
    const members = graph.members.map(m => ({ device_name: m.device_name, voice: m.voice, core_version: m.core_version,
      signal_online: m.signal_online, signal_recovering: m.signal_recovering, group_id: m.group_id }));
    const transports = [...this.ice.records.values()].filter(r => r.online).map(r => {
      const established = [r.a, r.b].map(name => r.established?.[name]?.generation === r.generation ? r.established[name].path_type : '');
      const punch = [r.a, r.b].map(name => {
        const events = r.ice?.[name]?.punch ?? [];
        const last = events.at(-1);
        return last ? { kind: last.kind, round: last.round, result: last.result } : null;
      });
      const phases = [0, 1].map(index => r.peer_relay ? 'peer_relay' : r.profile === FIXED_PROFILE ? 'fixed' : r.round_mode ? recordPhase(r, r.round ?? 0, index) : r.phase);
      return { a: r.a, b: r.b, generation: r.generation, profile: r.profile, phases, direct_stage: r.direct_stage, blocked: r.blocked ?? '',
        round: r.round, relay_required: r.relay_required, established, punch, voice: r.voice === true,
        mappings: (r.mappings ?? []).map(m => m.mapping_id), control: r.room_control === true };
    }).sort((a, b) => a.a.localeCompare(b.a) || a.b.localeCompare(b.b));
    return { type: 'panel_snapshot', worker_version: WORKER_VERSION, members, groups: graph.groups, links: graph.links.map(l => ({from:l.from,to:l.to})), transports };
  }

  broadcastPanel() {
    const watchers = this.state.getWebSockets().filter(ws => panelAttachment(ws) === 'watching');
    if (!watchers.length) { this.panelLast = ''; return; }
    const snapshot = this.panelSnapshot(), serialized = JSON.stringify(snapshot);
    if (serialized === this.panelLast) return;
    this.panelLast = serialized;
    for (const ws of watchers) send(ws, snapshot);
  }

  // attachment may already be gone when the close callback arrives. Keep
  // delegated members and identities needed by retained transports; prune
  // unreachable, unreferenced declarations after reconciling the graph.
  async removeMember(ws, resumable = false) {
    const name = memberName(ws) ?? findBySocket(this.registered, ws);
    detach(ws);
    if (name === null) return;
    const entry = this.registered.get(name);
    if (!entry || entry.ws !== ws) return;

    if (modernMember(entry.member)) entry.ws = null;
    else {
      this.registered.delete(name);
      await this.state.storage.delete(memberKey(name));
    }
    this.control.rebuild();
    await this.broadcastRoomState();
    // Online state may disappear; room authentication and transport generations remain.
    const canResume = resumable && this.ice.hasOnlineTransport(name, entry.member);
    await this.reconcile(canResume ? new Set([name]) : new Set());
  }

  async pruneMembers() {
    const retained = new Set([...this.members.keys(), ...this.joining.values()]);
    for (const record of this.ice.records.values()) {
      // Offline records still need their declarations for same-runtime resume,
      // including a selected third-party relay that may reconnect separately.
      for (const name of [record.a, record.b, record.hole?.device, record.peer_relay?.device]) retained.add(name);
    }
    const keys = [];
    let graphChanged = false;
    for (const [name, entry] of this.registered) {
      if (entry.ws || retained.has(name)) continue;
      this.registered.delete(name);
      graphChanged = this.control.nodes.delete(name) || graphChanged;
      keys.push(memberKey(name));
    }
    if (keys.length) await this.state.storage.delete(keys);
    if (graphChanged) await this.state.storage.put('room_nodes', [...this.control.nodes.values()].map(value => value.frame));
  }

  // room_members remains the legacy string list in joined messages. Voice
  // clients consume this versioned object snapshot instead.
  async broadcastRoomState() {
    const members = [...this.members.values()]
      .map(({ member }) => ({ device_name: member.device_name, voice: member.voice === true }))
      .sort((a, b) => a.device_name.localeCompare(b.device_name));
    for (const { ws, member } of this.members.values()) if (!modernMember(member)) send(ws, { type: "room_state", version: 1, members });
    await this.control.publishSnapshot();
  }

  async reconcile(resumeNames = new Set()) {
    const icePairs = new Set();
    const entries = [...this.members.values()];
    const voiceEntries = entries.filter(entry => entry.member.voice === true)
      .sort((a, b) => a.member.device_name.localeCompare(b.member.device_name));
    const allowedVoice = new Set(voiceEntries.slice(0, VOICE_MEMBER_LIMIT).map(entry => entry.member.device_name));
    for (const entry of voiceEntries.slice(VOICE_MEMBER_LIMIT)) {
      send(entry.ws, { type: "error", code: "voice_capacity", message: `语音房参与者上限为 ${VOICE_MEMBER_LIMIT}` });
    }

    // consume 只按 id 配对：id 在房间内被多台设备同时提供时无法确定对端，
    // 跳过这些映射并向相关成员发 error，避免静默无输出的配置错误。
    const providersById = new Map();
    for (const entry of entries) {
      for (const provide of entry.member.provide) {
        const list = providersById.get(provide.id) ?? [];
        list.push(entry);
        providersById.set(provide.id, list);
      }
    }
    for (const [id, list] of providersById) {
      if (list.length < 2) continue;
      const message = `映射 ${id} 被多台设备同时提供（${list.map((entry) => entry.member.device_name).join("、")}），已跳过配对`;
      for (const entry of list) {
        send(entry.ws, { type: "error", code: "duplicate_provide_id", mapping_id: id, message });
      }
      for (const entry of entries) {
        if (!list.includes(entry) && entry.member.consume.some((c) => c.id === id)) {
          send(entry.ws, { type: "error", code: "duplicate_provide_id", mapping_id: id, message });
        }
      }
    }

    for (let i = 0; i < entries.length; i += 1) {
      for (let j = i + 1; j < entries.length; j += 1) {
        const left = entries[i], right = entries[j];
        const pairMappings = [...matchingPairs(left, right, providersById), ...matchingPairs(right, left, providersById)];
        const voicePair = left.member.voice === true && right.member.voice === true &&
          allowedVoice.has(left.member.device_name) && allowedVoice.has(right.member.device_name);
        if (!pairMappings.length && !voicePair) continue;
        if ((left.member.fixed_endpoint || right.member.fixed_endpoint) && (left.member.relay_only || right.member.relay_only)) {
          for (const entry of [left, right]) send(entry.ws, {type:"error",code:"fixed_relay_conflict",message:"固定入口直连与对端仅中继要求冲突"});
          continue;
        }
        const profile = negotiateProfile(left.member, right.member);
        if (profile !== ICE_PROFILE && profile !== FIXED_PROFILE) {
          for (const entry of [left, right]) send(entry.ws, {type:"error",code:"protocol_mismatch",message:"设备软件版本或连接方式不兼容，请同步更新客户端"});
          continue;
        }
        await this.ice.activate(left, right, pairMappings, icePairs, voicePair);
      }
    }
    await this.ice.finish(icePairs, resumeNames);
    await this.pruneMembers();
  }
}

function findBySocket(members, ws) {
  for (const [name, entry] of members) {
    if (entry.ws === ws) return name;
  }
  return null;
}

// attachment 在连接关闭后可能为 null，deserializeAttachment 本身不会抛错。
function memberName(ws) {
  let attachment;
  try {
    attachment = ws.deserializeAttachment();
  } catch {
    return null;
  }
  if (attachment && typeof attachment.device_name === "string") {
    return attachment.device_name;
  }
  return null;
}

function panelAttachment(ws) {
  try { return ws.deserializeAttachment()?.panel ?? null; } catch { return null; }
}

function detach(ws) {
  try {
    ws.serializeAttachment(null);
  } catch {
    // The socket may already be closed.
  }
}

function sameResumeDeclaration(left, right) {
  const normalize = value => {
    const copy = { ...value };
    delete copy.candidates;
    delete copy.member_revision;
    delete copy.update_request_id;
    copy.provide = [...(copy.provide ?? [])].sort((a, b) => a.id.localeCompare(b.id));
    copy.consume = [...(copy.consume ?? [])].sort((a, b) => a.id.localeCompare(b.id));
    return copy;
  };
  return JSON.stringify(normalize(left)) === JSON.stringify(normalize(right));
}

function memberKey(name) {
  return `member:${name}`;
}

// 按单一方向匹配：left 的 provide 与 right 的 consume，id 对上即配对。
// id 被多台设备同时提供时跳过（reconcile 已向相关成员发 error）；
// service 是否有效由共享核心在收到 transport_ready 后自行校验。
function matchingPairs(left, right, providersById) {
  const matches = [];
  for (const consume of right.member.consume) {
    const providers = providersById.get(consume.id);
    if (!providers || providers.length !== 1 || providers[0] !== left) continue;
    const provide = left.member.provide.find((entry) => entry.id === consume.id);
    if (!provide) continue;
    matches.push({
      id: provide.id,
      provider: left.member.device_name,
      service: provide.service,
      provider_device: left.member.device_name,
      consumer_device: right.member.device_name,
      consumer_expose: consume.expose,
    });
  }
  return matches;
}

function validateJoin(message) {
  if (typeof message.token !== "string" && typeof message.token !== "number") return null;
  if (typeof message.device_name !== "string" || !/^[a-zA-Z0-9._-]{1,128}$/.test(message.device_name)) return null;
  if (!Array.isArray(message.provide) || !Array.isArray(message.consume) || message.provide.length + message.consume.length > 64) return null;
  const iceFields = iceJoinFields(message);
  if (!iceFields) return null;

  const provide = message.provide.map(normalizeProvide);
  if (provide.some((entry) => entry === null)) return null;
  if (new Set(provide.map((entry) => entry.id)).size !== provide.length) return null;

  const consume = message.consume.map(normalizeConsume);
  if (consume.some((entry) => entry === null)) return null;
  if (new Set(consume.map((entry) => entry.id)).size !== consume.length) return null;

  const certFingerprint = normalizeFingerprint(message.cert_fingerprint);
  if (certFingerprint === null) return null;

  return {
    ...iceFields,
    voice: message.voice === true,
    token: String(message.token),
    device_name: message.device_name,
    device_id: message.device_name,
    provide,
    consume,
    cert_fingerprint: certFingerprint,
  };
}

// provide 的 service 是 provider 设备上的监听地址，协议必须显式声明。
function normalizeProvide(entry) {
  if (!entry || typeof entry !== "object") return null;
  if (typeof entry.id !== "string" || !entry.id || entry.id.length > 64 || /[\s\u0000-\u001f]/.test(entry.id)) return null;
  const service = entry.service;
  if (!service || typeof service !== "object") return null;
  const protocol = service.protocol;
  if (protocol !== "tcp" && protocol !== "udp") return null;
  if (typeof service.addr !== "string" || !service.addr || service.addr.length > 255 || !validPort(service.port)) return null;
  return {
    id: entry.id,
    service: { protocol, addr: service.addr, port: Number(service.port) },
  };
}

// consume 只声明 id 与 expose：id 对应某台设备 provide 的映射（房间内唯一），
// 提供者由配对得出，expose 是本机监听地址。
function normalizeConsume(entry) {
  if (!entry || typeof entry !== "object") return null;
  if (typeof entry.id !== "string" || !entry.id || entry.id.length > 64 || /[\s\u0000-\u001f]/.test(entry.id)) return null;
  const expose = normalizeHostPort(entry.expose);
  if (!expose) return null;
  return { id: entry.id, expose };
}

function normalizeHostPort(endpoint) {
  if (!endpoint || typeof endpoint !== "object") return null;
  if (typeof endpoint.addr !== "string" || !endpoint.addr || endpoint.addr.length > 255 || !validPort(endpoint.port)) return null;
  return { addr: endpoint.addr, port: Number(endpoint.port) };
}

function validPort(port) {
  return Number.isInteger(Number(port)) && Number(port) >= 1 && Number(port) <= 65535;
}

// 证书指纹是数据面认证的根：两端只接受证书指纹与信令下发一致的 QUIC 对端。
// 必须是 64 位小写 hex；缺字段按空串处理，客户端会拒绝没有 peer_fingerprint
// 的 mapping_ready，保证旧记录不会退化成无认证的数据面。
function normalizeFingerprint(value) {
  if (value === undefined || value === null || value === "") return "";
  if (typeof value !== "string" || !/^[a-f0-9]{64}$/.test(value)) return null;
  return value;
}

function send(socket, message) {
  try {
    socket.send(JSON.stringify(message));
  } catch {
    // The close event removes disconnected members.
  }
}

function sendError(socket, code, message) {
  send(socket, { type: "error", code, message });
}
