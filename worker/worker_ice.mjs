// ICE signaling is scoped by an authenticated socket and a persisted device pair.
export const ICE_PROFILE = "ice-quic-mux-v1";
export const LEGACY_PROFILE = "legacy-ipv6-quic-v2";
const LEASE_MS = 90_000;
const OFFLINE_RETENTION_MS = 24 * 60 * 60 * 1000;
const MAX_CANDIDATES = 64;
const send = (ws, value) => { try { ws.send(JSON.stringify(value)); } catch {} };
const fault = (ws, code, message, request = {}) => send(ws, { type: "error", code, message, request_id: request.request_id, transport_id: request.transport_id });
const decimal = value => typeof value === "string" && /^(0|[1-9][0-9]{0,19})$/.test(value) && BigInt(value) <= 18446744073709551615n;
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const pairKey = (a, b) => JSON.stringify([a, b].sort());
export const RELAY_POLICY = "udp-tcp-tls-v1";
const orderedPhases = ["direct", "relay_udp", "relay_tcp_80", "relay_tcp", "relay_tls_443", "relay_tls"];
const compatiblePhases = ["direct", "relay_udp", "relay_tls", "relay_tls_443"];

export function iceJoinFields(message) {
  if (message.signal_version === undefined) return { signal_version: 1, transport_profiles: [LEGACY_PROFILE], session_versions: [2], runtime_id: message.cert_fingerprint || "legacy", transport_epoch: "0" };
  if (message.signal_version !== 2 || message.auth_mode !== "shared-secret") return null;
  if (!/^[a-f0-9]{32}$/.test(message.runtime_id ?? "") || !decimal(message.transport_epoch)) return null;
  if (!Array.isArray(message.transport_profiles) || message.transport_profiles.length > 2 || !message.transport_profiles.every(p => [ICE_PROFILE, LEGACY_PROFILE].includes(p))) return null;
  if (!Array.isArray(message.session_versions) || message.session_versions.length > 4 || !message.session_versions.includes(2)) return null;
  if (!/^[a-f0-9]{64}$/.test(message.cert_fingerprint ?? "")) return null;
  if (message.relay_policy !== undefined && message.relay_policy !== RELAY_POLICY) return null;
  return { signal_version: 2, auth_mode: "shared-secret", runtime_id: message.runtime_id, transport_epoch: message.transport_epoch, transport_profiles: [...new Set(message.transport_profiles)], session_versions: [2], relay_policy: message.relay_policy ?? "" };
}
export function negotiateProfile(left, right) {
  const a = left.transport_profiles ?? [LEGACY_PROFILE];
  const b = right.transport_profiles ?? [LEGACY_PROFILE];
  if (a.includes(ICE_PROFILE) && b.includes(ICE_PROFILE)) return ICE_PROFILE;
  if (a.includes(LEGACY_PROFILE) && b.includes(LEGACY_PROFILE)) return LEGACY_PROFILE;
  return null;
}

export class RoomICE {
  constructor(room, state, env = {}) {
    this.room = room; this.state = state; this.env = env;
    this.records = new Map(); this.loaded = false; this.roomName = "";
  }
  async init() {
    if (this.loaded) return;
    this.roomName = (await this.state.storage.get("room_name")) ?? "";
    const stored = await this.state.storage.list({ prefix: "transport:" });
    const now = Date.now();
    for (const [key, value] of stored) {
      // An online record is not a TTL cache. Its last topology update may be
      // days old while both hibernating sockets are still healthy.
      if (value.online || value.expires_at > now) this.records.set(value.pair_key, value);
      else await this.state.storage.delete(key);
    }
    this.loaded = true;
  }
  async setRoomName(name) {
    if (!this.roomName && name) { this.roomName = name; await this.state.storage.put("room_name", name); }
  }
  async persist(record) { await this.state.storage.put(`transport:${record.id}`, record); }
  async scheduleCleanup() {
    let next = null;
    for (const record of this.records.values()) {
      if (!record.online && Number.isFinite(record.expires_at)) next = next === null ? record.expires_at : Math.min(next, record.expires_at);
    }
    const current = await this.state.storage.getAlarm?.();
    if (next !== null && current !== next) await this.state.storage.setAlarm(next);
    else if (next === null && current != null) await this.state.storage.deleteAlarm?.();
  }
  async expireOffline() {
    const now = Date.now();
    for (const [key, record] of this.records) {
      if (!record.online && record.expires_at <= now) { this.records.delete(key); await this.state.storage.delete(`transport:${record.id}`); }
    }
    await this.scheduleCleanup();
  }
  member(name) { return this.room.members.get(name); }
  async activate(left, right, matches, seen) {
    const a = left.member.device_name < right.member.device_name ? left : right;
    const b = a === left ? right : left;
    const key = pairKey(a.member.device_name, b.member.device_name);
    seen.add(key);
    const mappings = matches.map(m => ({ map_key: JSON.stringify([this.roomName, m.provider_device, m.consumer_device, m.id]), mapping_id: m.id, provider_id: m.provider_device, consumer_id: m.consumer_device, service: m.service, expose: m.consumer_expose })).sort((x, y) => x.map_key.localeCompare(y.map_key));
    let record = this.records.get(key);
    const runtime = [a.member.runtime_id, b.member.runtime_id];
    const fingerprints = [a.member.cert_fingerprint, b.member.cert_fingerprint];
    const epochs = [a.member.transport_epoch, b.member.transport_epoch];
    const relayPolicy = a.member.relay_policy === RELAY_POLICY && b.member.relay_policy === RELAY_POLICY ? RELAY_POLICY : "";
    let changed = false;
    if (!record) {
      if (this.records.size >= 128) { fault(a.ws, "transport_limit", "房间传输记录达到上限"); fault(b.ws, "transport_limit", "房间传输记录达到上限"); return; }
      record = { id: crypto.randomUUID(), pair_key: key, a: a.member.device_name, b: b.member.device_name, generation: "1", phase: "direct", runtime, fingerprints, epochs, mappings, ice: {}, retry: 0 };
      changed = true;
    } else if (!same(record.runtime, runtime) || !same(record.fingerprints, fingerprints) || !same(record.epochs, epochs) || (record.relay_policy ?? "") !== relayPolicy) {
      record.generation = (BigInt(record.generation) + 1n).toString(); record.phase = "direct"; record.ice = {}; record.retry = 0;
      record.runtime = runtime; record.fingerprints = fingerprints; record.epochs = epochs; record.blocked = null; changed = true;
    }
    if (!same(record.mappings, mappings)) changed = true;
    if (!record.online) changed = true;
    record.relay_policy = relayPolicy;
    record.mappings = mappings; record.online = true; record.lease_until = Date.now() + LEASE_MS;
    this.records.set(key, record);
    if (changed) { record.expires_at = Date.now() + OFFLINE_RETENTION_MS; await this.persist(record); }
    if (changed) { this.notify(record, a.member.device_name, true); this.notify(record, b.member.device_name, true); }
  }
  notify(record, name, replay = false) {
    const own = this.member(name); if (!own) return;
    const index = name === record.a ? 0 : 1;
    const peerName = index === 0 ? record.b : record.a;
    send(own.ws, { type: "transport_ready", transport_id: record.id, transport_generation: record.generation, profile: ICE_PROFILE, session_version: 2, phase: record.phase, relay_policy: record.relay_policy ?? "", peer_device: peerName, peer_runtime_id: record.runtime[1-index], peer_fingerprint: record.fingerprints[1-index], runtime_id: record.runtime[index], initiator_id: record.a, mappings: record.mappings, lease_until: String(record.lease_until), retry_after_ms: record.retry_after_ms ?? 0 });
    if (replay) {
      const cached = record.ice[peerName]; if (!cached) return;
      const common = { transport_id: record.id, transport_generation: record.generation, peer_device: peerName };
      if (cached.description) send(own.ws, { ...common, type: "ice_description", ...cached.description });
      for (const candidate of cached.candidates ?? []) send(own.ws, { ...common, type: "ice_candidate", candidate });
      if (cached.end) send(own.ws, { ...common, type: "ice_candidate", end_of_candidates: true });
    }
  }
  async finish(seen) {
    for (const [key, record] of this.records) {
      if (seen.has(key)) continue;
      const a = this.member(record.a), b = this.member(record.b);
      const removed = !!(a && b);
      if (record.online || removed) for (const [own, peer] of [[a, record.b], [b, record.a]]) if (own) send(own.ws, { type: "peer_state", peer_device: peer, transport_id: record.id, reason: removed ? "mapping_removed" : "peer_offline" });
      const wasOnline = record.online;
      record.online = false;
      // Retention starts when the peer goes offline, not when it first joined.
      if (wasOnline) record.expires_at = Date.now() + OFFLINE_RETENTION_MS;
      if (removed || record.expires_at <= Date.now()) { this.records.delete(key); await this.state.storage.delete(`transport:${record.id}`); }
      else if (wasOnline) await this.persist(record);
    }
    await this.scheduleCleanup();
  }
  staleJoin(member) {
    if (member.signal_version !== 2) return false;
    for (const record of this.records.values()) {
      const i = member.device_name === record.a ? 0 : member.device_name === record.b ? 1 : -1;
      if (i >= 0 && record.runtime[i] === member.runtime_id && BigInt(member.transport_epoch) < BigInt(record.epochs[i])) return true;
    }
    return false;
  }
  takeRate(ws) {
    const attachment = ws.deserializeAttachment() ?? {};
    const window = Math.floor(Date.now()/10_000);
    attachment.ice_rate_count = attachment.ice_rate_window === window ? (attachment.ice_rate_count ?? 0) + 1 : 1;
    attachment.ice_rate_window = window; ws.serializeAttachment(attachment);
    return attachment.ice_rate_count <= 512;
  }
  async handle(ws, member, message) {
    if (!['transport_sync','transport_renew','transport_restart','ice_description','ice_candidate','transport_established','transport_failed','turn_request'].includes(message.type)) return false;
    if (member.signal_version !== 2) { fault(ws, "protocol_mismatch", "此消息需要新版协调协议", message); return true; }
    if (!this.takeRate(ws) || new TextEncoder().encode(JSON.stringify(message)).length > 64*1024) { fault(ws, "rate_limited", "协调消息过于频繁", message); return true; }
    const name = member.device_name;
    if (message.type === "transport_sync" || message.type === "transport_renew") {
      const leases = [];
      for (const record of this.records.values()) if ((record.a === name || record.b === name) && record.online) {
        // A lease is a fresh authorization reply, not persistent topology.
        // Reconstructing this value after hibernation requires no storage write.
        record.lease_until = Date.now() + LEASE_MS;
        if (message.type === "transport_sync") this.notify(record, name, true);
        else leases.push({ transport_id: record.id, transport_generation: record.generation, lease_until: String(record.lease_until) });
      }
      if (message.type === "transport_renew") send(ws, { type: "transport_lease", request_id: message.request_id, leases });
      return true;
    }
    if (message.type === "turn_request") { await this.turn(ws, member, message); return true; }
    const record = [...this.records.values()].find(r => r.id === message.transport_id && (r.a === name || r.b === name));
    if (!record || !record.online || record.mappings.length === 0) { fault(ws, "transport_not_authorized", "当前设备没有该对端传输关系", message); return true; }
    const peerName = record.a === name ? record.b : record.a;
    if (message.type === "transport_failed") {
      if (message.transport_generation === record.generation && ["peer_identity_mismatch", "protocol_mismatch"].includes(message.code)) {
        record.blocked = message.code; await this.persist(record);
        for (const target of [record.a,record.b]) { const peer=this.member(target); if(peer) fault(peer.ws,message.code,"对端身份或协议未通过检查，请核对客户端与协调服务配置",message); }
      }
      return true;
    }
    if (message.type === "transport_restart") {
      if (record.blocked) { fault(ws,record.blocked,"传输校验失败，等待配置或运行实例更新",message); return true; }
      if (!decimal(message.expected_generation)) { fault(ws, "invalid_generation", "传输代次格式无效", message); return true; }
      if (message.expected_generation !== record.generation) { this.notify(record, name, true); return true; }
      const phaseOrder = record.relay_policy === RELAY_POLICY ? orderedPhases : compatiblePhases;
      if (!phaseOrder.includes(message.phase)) { fault(ws, "invalid_phase", "路径阶段无效", message); return true; }
      const current = phaseOrder.indexOf(record.phase), requested = phaseOrder.indexOf(message.phase);
      if (requested !== 0 && requested !== current && requested !== current + 1) { fault(ws, "invalid_phase", "路径阶段应按顺序推进", message); return true; }
      record.generation = (BigInt(record.generation) + 1n).toString(); record.phase = message.phase; record.ice = {};
      record.retry = (record.retry ?? 0) + 1;
      record.retry_after_ms = message.phase === "direct" && message.reason !== "turn_refresh" ? Math.min(15_000, 1000 * 2 ** Math.min(record.retry, 4)) : 0;
      record.lease_until = Date.now() + LEASE_MS;
      await this.persist(record); this.notify(record, record.a); this.notify(record, record.b); return true;
    }
    if (!decimal(message.transport_generation) || message.transport_generation !== record.generation) return true;
    if (message.type === "transport_established") {
      if (record.retry || record.retry_after_ms) { record.retry = 0; record.retry_after_ms = 0; await this.persist(record); }
      return true;
    }
    if (message.to_peer_id !== peerName) { fault(ws, "transport_not_authorized", "ICE 消息目标不属于该传输", message); return true; }
    const cached = record.ice[name] ?? { candidates: [], candidate_bytes: 0 };
    let outgoing;
    if (message.type === "ice_description") {
      if (typeof message.ufrag !== "string" || !/^[A-Za-z0-9+/_=-]{4,256}$/.test(message.ufrag) || typeof message.pwd !== "string" || message.pwd.length < 22 || message.pwd.length > 256) { fault(ws,"invalid_ice_description","ICE 描述格式无效",message); return true; }
      const description = { ufrag: message.ufrag, pwd: message.pwd };
      if (cached.description && !same(cached.description, description)) { fault(ws,"invalid_ice_description","同一代次的 ICE 凭据已固定",message); return true; }
      cached.description = description; outgoing = { type: message.type, ...description };
    } else if (message.end_of_candidates === true) { cached.end = true; outgoing = { type: message.type, end_of_candidates: true }; }
    else {
      const candidate = message.candidate;
      if (typeof candidate !== "string" || candidate.length > 2048 || candidate.length < 10 || /[\r\n]/.test(candidate)) { fault(ws,"invalid_ice_candidate","ICE 候选格式无效",message); return true; }
      if (cached.candidates.includes(candidate)) return true;
      if (cached.end || cached.candidates.length >= MAX_CANDIDATES || cached.candidate_bytes + candidate.length > 16*1024) { fault(ws,"candidate_limit","ICE 候选达到本代次上限",message); return true; }
      cached.candidates.push(candidate); cached.candidate_bytes += candidate.length; outgoing = { type: message.type, candidate };
    }
    record.ice[name] = cached; await this.persist(record);
    const peer = this.member(peerName); if (peer) send(peer.ws, { ...outgoing, transport_id: record.id, transport_generation: record.generation, peer_device: name });
    return true;
  }
  async turn(ws, member, message) {
    if (!/^[A-Za-z0-9-]{1,64}$/.test(message.request_id ?? "")) { fault(ws,"invalid_request","中继请求缺少标识",message); return; }
    if (![...this.records.values()].some(r => r.online && (r.a === member.device_name || r.b === member.device_name))) { fault(ws,"transport_not_authorized","先建立有效设备关系再请求中继",message); return; }
    if (!this.env.TURN_BROKER) { fault(ws,"relay_unavailable","中继服务未配置，直连仍可使用",message); return; }
    const runtime = member.runtime_id;
    try {
      const broker = this.env.TURN_BROKER.get(this.env.TURN_BROKER.idFromName("global"));
      const response = await broker.fetch(new Request("https://turn-broker/credentials", { method:"POST", body:JSON.stringify({room:this.state.id.toString(),device:member.device_name,ttl:message.ttl}) }));
      const result = await response.json();
      const current = this.member(member.device_name);
      if (!current || current.ws !== ws || current.member.runtime_id !== runtime) return;
      if (!response.ok) { fault(ws,result.code ?? "relay_unavailable","中继服务暂不可用，直连仍在尝试",message); return; }
      send(ws,{type:"turn_servers",request_id:message.request_id,ice_servers:result.ice_servers,expire_at:String(result.expire_at),refresh_at:String(result.refresh_at)});
    } catch { fault(ws,"relay_unavailable","中继服务暂不可用，直连仍在尝试",message); }
  }
}

function turnURLPriority(url) {
  if (typeof url !== "string" || url.length > 512) return Infinity;
  const match = /^(turns?):(?:[A-Za-z0-9._-]+|\[[0-9A-Fa-f:]+\]):(\d+)(?:\?transport=(udp|tcp))?$/.exec(url);
  if (!match) return Infinity;
  const [, scheme, port, queryProtocol] = match;
  const protocol = queryProtocol ?? (scheme === "turns" ? "tcp" : "udp");
  if (scheme === "turn" && protocol === "udp" && port === "3478") return 0;
  if (scheme === "turns" && protocol === "tcp") return port === "443" ? 3 : port === "5349" ? 4 : Infinity;
  if (scheme === "turn" && protocol === "tcp") return port === "80" ? 1 : port === "3478" ? 2 : Infinity;
  return Infinity;
}

export function validateTurnResponse(data) {
  const raw = data?.iceServers ?? data?.ice_servers;
  const list = Array.isArray(raw) ? raw : raw && typeof raw === "object" ? [raw] : null;
  if (!Array.isArray(list) || list.length > 8) throw new Error("invalid TURN response");
  const result = [];
  for (const server of list) {
    const urls = typeof server.urls === "string" ? [server.urls] : server.urls;
    if (!Array.isArray(urls) || urls.length > 8) throw new Error("invalid TURN URLs");
    const accepted = [...new Set(urls.filter(url => Number.isFinite(turnURLPriority(url))))].sort((a,b) => turnURLPriority(a)-turnURLPriority(b));
    if (!accepted.length) continue;
    if (typeof server.username !== "string" || !server.username || server.username.length > 512 || typeof server.credential !== "string" || !server.credential || server.credential.length > 1024) throw new Error("invalid TURN credentials");
    result.push({urls:accepted,username:server.username,credential:server.credential});
  }
  if (!result.length) throw new Error("no supported TURN endpoints");
  return result;
}

export class TurnBroker {
  constructor(state, env) { this.state = state; this.env = env; this.inflight = new Map(); }
  async scheduleAt(at) {
    const current = await this.state.storage.getAlarm?.();
    if (current == null || current > at) await this.state.storage.setAlarm(at);
  }
  async fetch(request) {
    if (request.method !== "POST" || new URL(request.url).pathname !== "/credentials") return Response.json({code:"not_found"},{status:404});
    if (!this.env.TURN_KEY_ID || !this.env.TURN_KEY_API_TOKEN) return Response.json({code:"relay_unavailable"},{status:503});
    let input;
    try { const body=await request.text(); if(body.length>4096)throw new Error(); input=JSON.parse(body); } catch {return Response.json({code:"invalid_request"},{status:400});}
    if (typeof input.room!=="string"||input.room.length>128||typeof input.device!=="string"||!/^[A-Za-z0-9._-]{1,128}$/.test(input.device)) return Response.json({code:"invalid_request"},{status:400});
    const ttl=Math.max(600,Math.min(21600,Number.isFinite(input.ttl)?Math.floor(input.ttl):21600));
    const identityKey=JSON.stringify([input.room,input.device,this.env.TURN_KEY_ID,this.env.TURN_POLICY_VERSION??"1"]);
    // Invalidate filtered endpoint caches from before plain TURN/TCP support,
    // while retaining the existing per-device issuance rate-limit identity.
    const key=JSON.stringify([identityKey,ttl,RELAY_POLICY]);
    let job=this.inflight.get(key);
    if (!job) {
      if(this.inflight.size>=8)return Response.json({code:"turn_rate_limited"},{status:429});
      job=this.issue(key,ttl,identityKey,input.room);this.inflight.set(key,job);
    }
    try{return Response.json(await job);}catch(error){return Response.json({code:error?.message==="rate_limited"?"turn_rate_limited":"relay_unavailable"},{status:error?.message==="rate_limited"?429:503});}
    finally{if(this.inflight.get(key)===job)this.inflight.delete(key);}
  }
  async issue(key,ttl,identityKey,room) {
    const keyID=this.env.TURN_KEY_ID, policy=this.env.TURN_POLICY_VERSION??"1";
    const digest=async text=>[...new Uint8Array(await crypto.subtle.digest("SHA-256",new TextEncoder().encode(text)))].map(n=>n.toString(16).padStart(2,"0")).join("");
    const deviceHash=await digest(identityKey), roomHash=await digest(room);
    const encoded=new TextEncoder().encode(key);
    const hash=[...new Uint8Array(await crypto.subtle.digest("SHA-256",encoded))].map(n=>n.toString(16).padStart(2,"0")).join("");
    const storageKey=`turn_cache:${hash}`, now=Date.now();
    const cached=await this.state.storage.get(storageKey);
    if(cached&&cached.refresh_at>now+30_000)return cached;
    await this.state.storage.transaction(async tx=>{
      const take=async(k,window,limit)=>{let r=await tx.get(k);const w=Math.floor(now/window);if(!r||r.window!==w)r={window:w,count:0};if(r.count>=limit)throw new Error("rate_limited");r.count++;r.expire_at=now+window*2;await tx.put(k,r);};
      await take("turn_rate:global",60_000,60);await take(`turn_rate:room:${roomHash}`,60_000,30);await take(`turn_rate:device:${deviceHash}`,300_000,3);
    });
    // Global/room rate rows expire first. The alarm subsequently schedules the
    // next actual expiry instead of scanning a six-hour cache every five minutes.
    await this.scheduleAt(now+120_000);
    const response=await fetch(`https://rtc.live.cloudflare.com/v1/turn/keys/${encodeURIComponent(keyID)}/credentials/generate-ice-servers`,{
      method:"POST",headers:{Authorization:`Bearer ${this.env.TURN_KEY_API_TOKEN}`,"Content-Type":"application/json"},body:JSON.stringify({ttl}),signal:AbortSignal.timeout(6000),
    });
    if(!response.ok)throw new Error("TURN API failed");
    const data=await response.text();if(data.length>64*1024)throw new Error("TURN API response limit");
    const ice_servers=validateTurnResponse(JSON.parse(data));
    if(this.env.TURN_KEY_ID!==keyID||(this.env.TURN_POLICY_VERSION??"1")!==policy)throw new Error("key changed");
    const result={ice_servers,expire_at:now+ttl*1000,refresh_at:now+Math.floor(ttl*1000*0.8)};
    await this.state.storage.put(storageKey,result);
    await this.scheduleAt(result.expire_at);
    return result;
  }
  async alarm(){
    const entries=await this.state.storage.list({prefix:"turn_cache:"});const now=Date.now();
    const rates=await this.state.storage.list({prefix:"turn_rate:"});
    let next=null;
    for(const [key,value] of [...entries,...rates]) {
      if(!Number.isFinite(value.expire_at)||value.expire_at<=now)await this.state.storage.delete(key);
      else next=next===null?value.expire_at:Math.min(next,value.expire_at);
    }
    if(next!==null)await this.state.storage.setAlarm(next);
    else await this.state.storage.deleteAlarm?.();
  }
}
