import { RoomICE, TurnBroker, iceJoinFields, negotiateProfile, ICE_PROFILE, LEGACY_PROFILE } from "./worker_ice.mjs";
export { TurnBroker };
const ROOM_PATH = "/ws";
const MAX_MESSAGE_SIZE = 256 * 1024;

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

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
  const presented = new TextEncoder().encode(rest.join(" "));
  const wanted = new TextEncoder().encode(expected);
  if (presented.length !== wanted.length) return false;
  try {
    // workerd 提供的常数时间比较，避免逐字节比较泄露时序信息。
    return crypto.subtle.timingSafeEqual(presented, wanted);
  } catch {
    return false;
  }
}

// 房间状态全部落在两个地方：成员数据存 storage（member:<设备名>），
// 设备名存 WebSocket attachment，房间 token 与已通知映射存 storage。
// Hibernation 唤醒时构造函数会重新执行，内存里的 members/roomToken/activeMappings
// 都会丢失，每个事件入口先 loadState() 从持久层重建。
// attachment 只存设备名而不是整个成员数据：官方建议 attachment 只放小值和 key，
// 大对象走 Storage API。
export class Room {
  constructor(state, env = {}) {
    this.state = state;
    this.ice = new RoomICE(this, state, env);
    this.members = new Map();
    this.activeMappings = new Set();
    this.roomToken = null;
    this.loaded = false;
  }

  async fetch(request) {
    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return new Response("WebSocket upgrade required", { status: 426 });
    }

    await this.loadState();
    await this.ice.setRoomName(new URL(request.url).searchParams.get("room"));
    const pair = new WebSocketPair();
    const client = pair[0];
    const server = pair[1];
    // acceptWebSocket 而非 ws.accept()：socket 关联到 DO 并允许休眠，
    // 空闲时实例被逐出内存但连接不断。
    this.state.acceptWebSocket(server);

    return new Response(null, { status: 101, webSocket: client });
  }

  // loadState 从 storage 重建休眠期间丢失的内存状态。
  // DO 的事件由 input gate 串行化，加载期间不会有并发事件插进来改写状态。
  async loadState() {
    if (this.loaded) return;
    this.roomToken = (await this.state.storage.get("token")) ?? null;
    const active = await this.state.storage.get("activeMappings");
    this.activeMappings = new Set(active ?? []);
    this.members.clear();
    for (const ws of this.state.getWebSockets()) {
      const name = memberName(ws);
      if (name === null || this.members.has(name)) continue;
      const member = await this.state.storage.get(memberKey(name));
      if (member) this.members.set(name, { ws, member });
    }
    await this.ice.init();
    this.loaded = true;
    // Reconcile persisted topology against the sockets that actually survived
    // hibernation, including closes whose attachment is already gone.
    await this.reconcile();
  }

  async alarm() {
    await this.loadState();
    await this.ice.expireOffline();
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
    if (message.signal_version === 2 && new TextEncoder().encode(raw).length > 64 * 1024) { sendError(ws, "message_too_large", "ICE message exceeds 64 KiB"); return; }
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

      if (await this.ice.handle(ws, entry.member, message)) return;

      if (message.type === "candidate") {
        const candidates = validateCandidates(message.candidates);
        if (!candidates) {
          sendError(ws, "invalid_candidates", "invalid candidates");
          return;
        }
        entry.member = { ...entry.member, candidates };
        await this.state.storage.put(memberKey(name), entry.member);
        await this.reconcile();
        // reconcile 只对新建映射发 mapping_ready，已激活的映射不会重新通知；
        // 候选地址更新必须在这里主动推给活跃映射的对端，否则对端会一直
        // 打向已轮换失效的旧地址。
        await this.pushCandidateUpdates(name, candidates);
        return;
      }

      if (message.type === "leave") {
        ws.close(1000, "left room");
        return;
      }

      sendError(ws, "unknown_message", "unknown message type");
    } catch (error) {
      sendError(ws, "server_error", error instanceof Error ? error.message : "server error");
    }
  }

  async handleJoin(ws, message) {
    if (memberName(ws) !== null) {
      sendError(ws, "already_joined", "this socket already joined");
      return;
    }

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

    if (this.ice.staleJoin(member)) { sendError(ws, "stale_client_epoch", "旧网络代次已失效"); ws.close(4001, "stale client epoch"); return; }
    if (!this.members.has(member.device_name) && this.members.size >= 16) { sendError(ws, "member_limit", "房间同时在线设备达到上限"); return; }

    // Allow a reconnect to replace a stale WebSocket session.
    const existing = this.members.get(member.device_name);
    if (existing) {
      try {
        existing.ws.close(4001, "replaced by reconnect");
      } catch {
        // The old socket may already be closed.
      }
      // 服务端 close 后 socket 可能仍以 CLOSING 状态留在 getWebSockets 里，
      // 清空 attachment 让后续重建把它排除掉。
      detach(existing.ws);
      this.members.delete(member.device_name);
      // 重连往往快于运行时对旧连接断开的感知（休眠恢复后尤其如此），
      // 对端可能还挂着这条"僵尸"成员记录，配对 key 一直被视为 active。
      // 主动清掉与该设备相关的已通知映射，让 reconcile 重新下发 ready，
      // 否则重连的一端会一直等不到 mapping_ready，隧道无法重建。
      await this.dropActiveKeysFor(member.device_name);
    }

    if (this.roomToken === null) {
      this.roomToken = member.token;
      await this.state.storage.put("token", member.token);
    }

    await this.state.storage.put(memberKey(member.device_name), member);
    ws.serializeAttachment({ device_name: member.device_name });
    this.members.set(member.device_name, { ws, member });

    send(ws, {
      type: "joined",
      signal_version: 2,
      auth_mode: "shared-secret",
      lease_renewal: true,
      room_members: [...this.members.keys()],
    });
    await this.reconcile();
  }

  async webSocketClose(ws, code, reason, wasClean) {
    await this.loadState();
    await this.removeMember(ws);
  }

  async webSocketError(ws, error) {
    await this.loadState();
    await this.removeMember(ws);
  }

  // removeMember 清掉断开连接对应的成员。attachment 在连接关闭后可能已经
  // 丢失（官方文档：任一侧关闭连接 attachment 即失效），所以先读 attachment，
  // 读不到再按 ws 对象在内存 members 里反查；两者都拿不到（休眠中连接静默
  // 死亡）时只能留下一个无害的 storage 残键，重建后该键永远不会被引用，
  // 下次同名设备加入时会覆盖，房间清空时会被 deleteAll 清走。
  async removeMember(ws) {
    const name = memberName(ws) ?? findBySocket(this.members, ws);
    detach(ws);
    if (name === null) return;
    const entry = this.members.get(name);
    if (!entry || entry.ws !== ws) return;

    this.members.delete(name);
    await this.state.storage.delete(memberKey(name));
    // Online state may disappear; room authentication and transport generations remain.
    await this.reconcile();
  }

  // 删除 activeMappings 里包含指定设备的 key，变化时持久化。
  async dropActiveKeysFor(name) {
    const kept = new Set();
    for (const key of this.activeMappings) {
      const [first, second] = key.split("\0");
      if (first !== name && second !== name) kept.add(key);
    }
    if (kept.size === this.activeMappings.size) return;
    this.activeMappings = kept;
    if (kept.size === 0) {
      await this.state.storage.delete("activeMappings");
    } else {
      await this.state.storage.put("activeMappings", [...kept]);
    }
  }

  // pushCandidateUpdates 把某成员的最新候选地址推给所有涉及它的活跃映射
  // 的另一端，让对端把打洞目标热更新到当前有效地址。
  async pushCandidateUpdates(name, candidates) {
    for (const key of this.activeMappings) {
      const [first, second, mappingId] = key.split("\0");
      const peerName = first === name ? second : second === name ? first : null;
      if (peerName === null) continue;
      const peer = this.members.get(peerName);
      if (!peer) continue;
      send(peer.ws, {
        type: "peer_candidates",
        mapping_id: mappingId,
        peer_device: name,
        candidates,
      });
    }
  }

  async reconcile() {
    const nextActive = new Set();
    const icePairs = new Set();
    const entries = [...this.members.values()];

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
        const left = entries[i];
        const right = entries[j];
        const pairMappings = [...matchingPairs(left, right, providersById), ...matchingPairs(right, left, providersById)];

        if (pairMappings.length) {
          const profile = negotiateProfile(left.member, right.member);
          if (profile === ICE_PROFILE) { await this.ice.activate(left, right, pairMappings, icePairs); continue; }
          if (!profile) {
            for (const match of pairMappings) for (const entry of [left, right]) send(entry.ws,{type:"error",code:"protocol_mismatch",mapping_id:match.id,message:"双方没有共同连接方式，请更新对端或显式开启旧版兼容"});
            continue;
          }
        }
        for (const match of pairMappings) {
          // 名字排序后再拼 key：members 是插入序 Map，成员重连会调换 left/right，
          // 未排序的 key 会让同一个映射在重连后既被判为"新建"又被判为"消失"，
          // 于是 mapping_ready 之后紧跟一条错误的 mapping_closed。
          const [first, second] = [left.member.device_name, right.member.device_name].sort();
          const key = `${first}\0${second}\0${match.id}`;
          nextActive.add(key);

          if (!this.activeMappings.has(key)) {
            notifyMappingReady(left, right, match);
          }
        }
      }
    }

    for (const key of this.activeMappings) {
      if (!nextActive.has(key)) {
        const [leftName, rightName, mappingId] = key.split("\0");
        const left = this.members.get(leftName);
        const right = this.members.get(rightName);
        for (const entry of [left, right]) {
          if (entry) {
            send(entry.ws, {
              type: "mapping_closed",
              mapping_id: mappingId,
              peer_device: entry === left ? rightName : leftName,
              reason: "peer_or_configuration_changed",
            });
          }
        }
      }
    }

    await this.ice.finish(icePairs);

    if (!sameKeySet(this.activeMappings, nextActive)) {
      this.activeMappings = nextActive;
      if (nextActive.size === 0) {
        await this.state.storage.delete("activeMappings");
      } else {
        await this.state.storage.put("activeMappings", [...nextActive]);
      }
    }
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

function detach(ws) {
  try {
    ws.serializeAttachment(null);
  } catch {
    // The socket may already be closed.
  }
}

function memberKey(name) {
  return `member:${name}`;
}

// 按单一方向匹配：left 的 provide 与 right 的 consume，id 对上即配对。
// id 被多台设备同时提供时跳过（reconcile 已向相关成员发 error）；
// service 是否有效由 consumer 在收到 mapping_ready 后自行校验。
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
      provider_candidates: left.member.candidates,
      consumer_candidates: right.member.candidates,
      provider_fingerprint: left.member.cert_fingerprint ?? "",
      consumer_fingerprint: right.member.cert_fingerprint ?? "",
      consumer_expose: consume.expose,
    });
  }
  return matches;
}

function notifyMappingReady(left, right, match) {
  const common = {
    type: "mapping_ready",
    profile: LEGACY_PROFILE,
    mapping_id: match.id,
    provider: match.provider,
    service: match.service,
  };

  send(left.ws, {
    ...common,
    peer_device: right.member.device_name,
    role: left.member.device_name === match.provider ? "provider" : "consumer",
    local_expose: left.member.device_name === match.provider ? null : match.consumer_expose,
    peer_candidates: left.member.device_name === match.provider ? match.consumer_candidates : match.provider_candidates,
    peer_fingerprint: left.member.device_name === match.provider ? match.consumer_fingerprint : match.provider_fingerprint,
  });
  send(right.ws, {
    ...common,
    peer_device: left.member.device_name,
    role: right.member.device_name === match.provider ? "provider" : "consumer",
    local_expose: right.member.device_name === match.provider ? null : match.consumer_expose,
    peer_candidates: right.member.device_name === match.provider ? match.consumer_candidates : match.provider_candidates,
    peer_fingerprint: right.member.device_name === match.provider ? match.consumer_fingerprint : match.provider_fingerprint,
  });
}

function sameKeySet(left, right) {
  if (left.size !== right.size) return false;
  for (const key of left) {
    if (!right.has(key)) return false;
  }
  return true;
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
    token: String(message.token),
    device_name: message.device_name,
    device_id: message.device_name,
    provide,
    consume,
    candidates: validateCandidates(message.candidates) ?? [],
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

function validateCandidates(candidates) {
  if (!Array.isArray(candidates)) return null;
  if (candidates.length > 32) return null;
  const result = [];
  for (const candidate of candidates) {
    if (!candidate || typeof candidate.ip !== "string" || !validPort(candidate.port)) return null;
    result.push({ ip: candidate.ip, port: Number(candidate.port) });
  }
  return result;
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
