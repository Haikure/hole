// ICE signaling is scoped by an authenticated socket and a persisted device pair.
export const ICE_PROFILE = "ice-quic-mux-v1";
export const FIXED_PROFILE = "fixed-quic-mux-v1";
export const PUNCH_VERSION = "nat-punch-v1";
export const PUNCH_SCHEDULE = "ordered-v1";
export const PUNCH_CALIBRATION = "barrier-v1";
export const PUNCH_CONE_POOL = "retained-v1";
export const DIRECT_ORDER = "ice-first-v1";
export const PEER_RELAY_VERSION = "peer-relay-v1";
const ipv4 = value => typeof value==='string' && /^(?:\d{1,3}\.){3}\d{1,3}$/.test(value) && value.split('.').every(v=>Number(v)<=255) && value!=='0.0.0.0' && Number(value.split('.')[0])<224;
const integer = (v,lo,hi) => Number.isSafeInteger(v) && v>=lo && v<=hi;
export function validPunch(p) {
  if (!p || JSON.stringify(p).length>4096) return false;
  if (p.trial!==undefined && (!['calibrate','calibrated','ready','go'].includes(p.kind) || !integer(p.trial,1,3))) return false;
  if (p.kind==='offer') {
    const c=p.config;
    const duration=v=>typeof v==='string' && /^(?:\d+(?:\.\d+)?)(?:ms|s)$/.test(v) ? parseFloat(v)*(v.endsWith('ms')?1:1000) : NaN;
    return /^[a-f0-9]{64}$/.test(p.nonce??'') && (p.mode===undefined || p.mode==='disabled') && (p.pool_prediction===undefined || typeof p.pool_prediction==='boolean') && validNATPlan({snapshot:p.snapshot,addresses:[]}) && c && integer(c.sockets,1,64) && integer(c.scan_width,1,256) && integer(c.dual_sockets,1,64) && integer(c.dual_retry_sockets,c.dual_sockets,64) && duration(c.timeout)>=1000 && duration(c.timeout)<=30000 && duration(c.round_timeout)>=500 && duration(c.round_timeout)<=duration(c.timeout);
  }
  if (['abort','ice_start','reserve','reserved'].includes(p.kind)) return p.round===undefined;
  if (!integer(p.round,1,6)) return false;
  if (p.kind==='pool_select') return p.round>=4 && p.trial===undefined && validPunch({...p,kind:'ready'}) && p.plan.sockets===1 && p.plan.width===1 && p.plan.target===p.plan.port;
  if (p.kind==='calibrate') return integer(p.trial,1,3) && p.result===undefined;
  if (p.kind==='calibrated') return integer(p.trial,1,3) && ['ok','retry','refresh','exhausted'].includes(p.result) && ['single','fixed','next','pool'].includes(p.mode) && (p.result==='ok' ? validPunch({...p,kind:'ready'}) : p.plan===undefined);
  if (p.kind==='model') return validNATPlan({snapshot:p.snapshot,addresses:[]});
  if (p.kind==='go') return true;
  if (p.kind==='failed') return p.result===undefined || p.result==='unreachable';
  const v=p.plan;
  return p.kind==='ready' && ['single','fixed','next','pool'].includes(p.mode) && !!v && ipv4(v.ip) && integer(v.port,1,65535) && integer(v.target,1,65535) && integer(v.width,1,256) && v.target+v.width-1<=65535 && integer(v.sockets,1,64);
}
// Keep the cone's last actual mapping separate from its hypothetical exit IP.
const samePunchPlan=(a,b)=>['ip','port','target','width','sockets'].every(k=>a?.[k]===b?.[k]);
function conePoolState(both, round) {
  if (both.some(list=>list.some(v=>v.kind==='abort'))) return null;
  for(let r=1;r<round;r++) {
    if (!both.every(list=>list.some(v=>v.kind==='failed' && v.round===r && v.result==='unreachable')) ||
        !both.some(list=>list.some(v=>v.kind==='go' && v.round===r))) return null;
  }
  const models=both.map(list=>list.find(v=>v.kind==='model' && v.round===3)?.snapshot);
  const eligible=s=>s?.status==='complete' && s.mapping_behavior==='endpoint_independent' && s.no_pat==='suspected' && s.port_preservation==='preserved' &&
    s.ip_evidence?.length>=2 && s.ip_evidence.every(e=>e.ports===1 && ['stable','unknown'].includes(e.trend)) &&
    (!s.probed_at || Number.isFinite(Date.parse(s.probed_at)) && Date.now()-Date.parse(s.probed_at)<300_000);
  const cone=models.findIndex(eligible), symmetric=models[1-cone];
  if (cone<0 || symmetric?.status!=='complete' || symmetric.type!=='symmetric' || symmetric.mapping_behavior!=='endpoint_dependent') return null;
  const go=both.flat().find(v=>v.kind==='go' && v.round===3);
  const results=both.map(list=>list.find(v=>v.kind==='calibrated' && v.round===3 && v.trial===go.trial));
  if (!results.every(v=>v?.result==='ok' && ['single','pool'].includes(v.mode)) || results[0].mode!==results[1].mode) return null;
  const original=results[cone].plan, selection=both[cone].find(v=>v.kind==='pool_select' && v.round===round);
  if (original.sockets!==1 || original.width!==1 || original.port!==original.target) return null;
  return {cone,model:models[cone],original,selection,mode:results[cone].mode};
}
function validIPEvidence(entries) {
  if (!Array.isArray(entries) || entries.length<2 || entries.length>24) return false;
  const seen=new Set();
  return entries.every(e=>{
    if (!e || !ipv4(e.ip) || seen.has(e.ip)) return false;
    seen.add(e.ip);
    return integer(e.targets,1,16) && integer(e.ports,1,48) && ['unknown','stable','incremental','decremental','irregular'].includes(e.trend) &&
      (e.step===undefined || integer(e.step,1,1024)) &&
      (!['incremental','decremental'].includes(e.trend) || e.targets>=3 && e.ports>=3) &&
      (e.step===undefined || ['incremental','decremental'].includes(e.trend));
  });
}
export function validNATPlan(plan) {
  if (!plan || JSON.stringify(plan).length > 4096 || !plan.snapshot || !Array.isArray(plan.addresses) || plan.addresses.length > 8) return false;
  const s=plan.snapshot;
  return ['insufficient','probing','complete','expired','not_applicable'].includes(s.status) &&
    ['unknown','open','firewall','full_cone','restricted_cone','port_restricted_cone','symmetric','endpoint_independent','address_pool'].includes(s.type) &&
    ['unknown','stable','incremental','decremental','predictable_incremental','predictable_decremental','irregular','unverified'].includes(s.predictability) &&
    (s.ip_evidence===undefined || validIPEvidence(s.ip_evidence)) &&
    (s.type!=='address_pool' && s.predictability!=='unverified' || s.ip_evidence!==undefined) &&
    (s.mapping_behavior===undefined || ['unknown','endpoint_independent','endpoint_dependent'].includes(s.mapping_behavior)) &&
    (s.filtering_behavior===undefined || ['unknown','endpoint_independent','address_dependent','address_port_dependent'].includes(s.filtering_behavior)) &&
    (s.port_preservation===undefined || ['unknown','preserved','translated','mixed'].includes(s.port_preservation)) &&
    (s.no_pat===undefined || ['unknown','suspected','not_detected','not_applicable'].includes(s.no_pat)) &&
    Number.isSafeInteger(s.samples) && s.samples>=0 && s.samples<=64 &&
    (s.port_step===undefined || integer(s.port_step,0,65535)) &&
    (s.last_mapping_port===undefined || integer(s.last_mapping_port,1,65535)) &&
    (s.public_ip===undefined || ipv4(s.public_ip)) &&
    (s.allocation_scope===undefined || ['unknown','same_socket','cross_socket'].includes(s.allocation_scope)) &&
    (s.reason===undefined || typeof s.reason==='string' && s.reason.length<=256) &&
    plan.addresses.every(a=>typeof a==='string' && /^(?:\d{1,3}\.){3}\d{1,3}:[1-9]\d{0,4}$/.test(a) && a.split(':')[0].split('.').every(p=>Number(p)<=255) && Number(a.split(':')[1])<=65535);
}
export const LEGACY_PROFILE = "legacy-ipv6-quic-v2";
// Renewals arrive every five minutes; keep a full five-minute grace window.
const LEASE_MS = 10 * 60_000;
const OFFLINE_RETENTION_MS = 24 * 60 * 60 * 1000;
// Keep an authenticated transport generation resumable for one lease after a
// signaling socket disappears. The data plane is independent from the
// WebSocket, so a short-lived control-plane outage must not force ICE again.
const SIGNAL_RESUME_GRACE_MS = LEASE_MS;
const MAX_CANDIDATES = 64;
export const VOICE_MEMBER_LIMIT = 8;
const send = (ws, value) => { try { ws.send(JSON.stringify(value)); } catch {} };
const fault = (ws, code, message, request = {}) => send(ws, { type: "error", code, message, request_id: request.request_id, transport_id: request.transport_id });
const decimal = value => typeof value === "string" && /^(0|[1-9][0-9]{0,19})$/.test(value) && BigInt(value) <= 18446744073709551615n;
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const pairKey = (a, b) => JSON.stringify([a, b].sort());
export const RELAY_POLICY = "udp-tcp-tls-v1";
const orderedPhases = ["direct", "relay_udp", "relay_tcp_80", "relay_tcp", "relay_tls_443", "relay_tls"];
const compatiblePhases = ["direct", "relay_udp", "relay_tls", "relay_tls_443"];
const relayTypes = new Set(["udp", "tcp_80", "tcp", "tls_443", "tls"]);
const ALL_PAIRS = "all-pairs-v1";
export const RELAY_ONLY_POLICY = "relay-only-v1";
export const HOLE_VERSION = "hole-relay-v1";
export function deviceRelayPlan(a,b,aDevice,bDevice) {
  const out=[], seen=new Set();
  for (const [i,j] of relayPairs(a,b)) {
    if (a[i]==='hole' || b[j]==='hole') {
      for (const device of [a[i]==='hole'?aDevice:'',b[j]==='hole'?bDevice:''].filter(Boolean).sort())
        if (!seen.has(device)) {seen.add(device);out.push({device,a:'hole',b:'hole'});}
    } else out.push({a:i<0?'relay_wait':relayPhase(i+1,a),b:j<0?'relay_wait':relayPhase(j+1,b)});
  } return out;
}
const randomHex=n=>[...crypto.getRandomValues(new Uint8Array(n))].map(v=>v.toString(16).padStart(2,'0')).join('');

export function validFixedEndpoint(value) {
  if (typeof value !== "string" || value.length > 320 || /\s/.test(value)) return false;
  const match = /^(\[[0-9a-fA-F:.]+\]|[A-Za-z0-9.-]+):([0-9]+)$/.exec(value);
  if (!match || Number(match[2]) < 1 || Number(match[2]) > 65535) return false;
  const host = match[1];
  if (host.startsWith('[')) {
    try { const ip = new URL(`http://${host}/`).hostname; return ip !== '[::]' && ip !== '[::ffff:0:0]'; } catch { return false; }
  }
  if (/^[0-9.]+$/.test(host)) {
    const parts = host.split('.');
    return parts.length === 4 && parts.every(p => /^(0|[1-9][0-9]{0,2})$/.test(p) && Number(p) <= 255) && host !== '0.0.0.0';
  }
  return host.replace(/\.$/, '').split('.').every(p => p.length > 0 && p.length <= 63 && /^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$/.test(p)) && host.length <= 254;
}

function fixedDirection(record) {
  const listeners = [0, 1].filter(i => record.fixed_endpoints[i]);
  const index = listeners[(record.fixed_direction ?? 0) % listeners.length];
  return {listener_id: index === 0 ? record.a : record.b, dialer_id: index === 0 ? record.b : record.a, fixed_endpoint: record.fixed_endpoints[index]};
}

export function relayPairs(a, b) {
  const pairs = [];
  if (!a.length || !b.length) {
    for (let i = 0; i < Math.max(a.length,b.length); i++) pairs.push(a.length ? [i,-1] : [-1,i]);
    return pairs;
  }
  for (let depth = 0; depth < Math.max(a.length,b.length); depth++) {
    if (depth < a.length && depth < b.length) pairs.push([depth,depth]);
    for (let i = 0; i < depth; i++) {
      if (i < a.length && depth < b.length) pairs.push([i,depth]);
      if (depth < a.length && i < b.length) pairs.push([depth,i]);
    }
  }
  return pairs;
}
function recordPhase(record, round, index) {
  if (!round) return "direct";
  if (record.hole_version) return record.relay_plan[round-1]?.[index===0?'a':'b'] ?? '';
  if (record.relay_pairing !== ALL_PAIRS) return relayPhase(round,record.relay_orders[index]);
  const pair = relayPairs(...record.relay_orders)[round-1];
  if (!pair) return "";
  return pair[index] < 0 ? "relay_wait" : relayPhase(pair[index]+1,record.relay_orders[index]);
}

function maxRelayRound(record) {
  if (record.hole_version) return record.relay_plan.length;
  return record.relay_pairing === ALL_PAIRS ? relayPairs(...record.relay_orders).length : Math.max(...record.relay_orders.map(order => order.length));
}
// A relay-only endpoint cannot participate in relay_wait. Preserve the shared
// round numbering, but skip rounds where its own allocation cannot be gathered.
function usableRelayRound(record, start) {
  const max = maxRelayRound(record);
  for (let round = Math.max(1, start); round <= max; round++) {
    if (record.relay_only.every((only, index) => !only || recordPhase(record, round, index) !== "relay_wait")) return round;
  }
  return 0;
}

function validRelayOrder(value) {
  return Array.isArray(value) && value.length <= 5 && value.every((token, index) => relayTypes.has(token) && value.indexOf(token) === index);
}
function relayPhase(round, order) {
  if (round === 0) return "direct";
  if (round > order.length) return "relay_wait";
  return ({udp:"relay_udp",tcp_80:"relay_tcp_80",tcp:"relay_tcp",tls_443:"relay_tls_443",tls:"relay_tls"})[order[round - 1]];
}

export function iceJoinFields(message) {
  if (message.signal_version === undefined) return { signal_version: 1, transport_profiles: [LEGACY_PROFILE], session_versions: [2], runtime_id: message.cert_fingerprint || "legacy", transport_epoch: "0" };
  if (message.signal_version !== 2 || message.auth_mode !== "shared-secret") return null;
  if (!/^[a-f0-9]{32}$/.test(message.runtime_id ?? "") || !decimal(message.transport_epoch)) return null;
  if (!Array.isArray(message.transport_profiles) || message.transport_profiles.length > 3 || !message.transport_profiles.every(p => [ICE_PROFILE, LEGACY_PROFILE, FIXED_PROFILE].includes(p))) return null;
  if (message.fixed_endpoint !== undefined && (!validFixedEndpoint(message.fixed_endpoint) || message.relay_only === true || !message.transport_profiles.includes(FIXED_PROFILE))) return null;
  if (!Array.isArray(message.session_versions) || message.session_versions.length > 4 || !message.session_versions.includes(2)) return null;
  if (!/^[a-f0-9]{64}$/.test(message.cert_fingerprint ?? "")) return null;
  if (message.relay_policy !== undefined && message.relay_policy !== RELAY_POLICY) return null;
  if (message.relay_enabled !== undefined && typeof message.relay_enabled !== "boolean") return null;
  if (message.relay_only !== undefined && typeof message.relay_only !== "boolean") return null;
  const hole=message.hole_version===HOLE_VERSION;
  if (message.punch_version!==undefined && message.punch_version!==PUNCH_VERSION) return null;
  if (message.direct_order!==undefined && (message.punch_version!==PUNCH_VERSION || message.direct_order!==DIRECT_ORDER)) return null;
  if (message.punch_schedule!==undefined && (message.punch_version!==PUNCH_VERSION || message.punch_schedule!==PUNCH_SCHEDULE)) return null;
  if (message.punch_calibration!==undefined && (message.punch_calibration!==PUNCH_CALIBRATION || message.punch_schedule!==PUNCH_SCHEDULE || message.direct_order!==DIRECT_ORDER)) return null;
  if (message.punch_cone_pool!==undefined && (message.punch_cone_pool!==PUNCH_CONE_POOL || message.punch_calibration!==PUNCH_CALIBRATION)) return null;
  if (message.hole_version!==undefined && !hole) return null;
  if (message.hole_device!==undefined && (!hole || !/^[a-zA-Z0-9._-]{1,128}$/.test(message.hole_device) || message.hole_device===message.device_name)) return null;
  if (message.hole_serve!==undefined && (!hole || !message.fixed_endpoint || message.hole_serve.enabled!==true || !Number.isSafeInteger(message.hole_serve.max_pairs) || message.hole_serve.max_pairs<1 || message.hole_serve.max_pairs>128 || !Number.isSafeInteger(message.hole_serve.bandwidth_mbps) || message.hole_serve.bandwidth_mbps<0)) return null;
  if (message.relay_only===true && ((!hole && (message.relay_enabled===false || message.relay_order?.length===0)) || !message.transport_profiles.includes(ICE_PROFILE))) return null;
  if (message.relay_enabled === false && message.relay_order === undefined) return null;
  if (message.relay_order!==undefined && !(hole ? Array.isArray(message.relay_order) && message.relay_order.length<=6 && message.relay_order.every((t,i)=>(t==='hole'?!!message.hole_device:relayTypes.has(t)) && message.relay_order.indexOf(t)===i) : validRelayOrder(message.relay_order))) return null;
  if (message.relay_pairing !== undefined && (message.relay_pairing !== ALL_PAIRS || message.relay_order === undefined)) return null;
  const relayEnabled = message.relay_enabled ?? true;
  if (message.voice !== undefined && typeof message.voice !== "boolean") return null;
  if (message.live_config !== undefined && typeof message.live_config !== "boolean") return null;
  if (message.peer_relay_version !== undefined && message.peer_relay_version !== PEER_RELAY_VERSION) return null;
  return { peer_relay_version: message.peer_relay_version ?? "", live_config: message.live_config === true, ...(message.punch_cone_pool?{punch_cone_pool:PUNCH_CONE_POOL}:{}), ...(message.punch_calibration?{punch_calibration:PUNCH_CALIBRATION}:{}), ...(message.direct_order?{direct_order:DIRECT_ORDER}:{}), ...(message.punch_schedule?{punch_schedule:PUNCH_SCHEDULE}:{}), ...(message.punch_version?{punch_version:PUNCH_VERSION}:{}), ...(hole?{hole_version:HOLE_VERSION,hole_device:message.hole_device??"",hole_serve:message.hole_serve??null}:{}), signal_version: 2, auth_mode: "shared-secret", runtime_id: message.runtime_id, transport_epoch: message.transport_epoch, transport_profiles: [...new Set(message.transport_profiles)], session_versions: [2], voice: message.voice === true, relay_only: message.relay_only === true, fixed_endpoint: message.fixed_endpoint ?? "", relay_policy: message.relay_policy ?? "", relay_pairing: message.relay_pairing ?? "", ...(message.relay_order === undefined ? {} : {relay_order:relayEnabled ? [...message.relay_order] : hole ? message.relay_order.filter(t=>t==='hole') : []}) };
}
export function negotiateProfile(left, right) {
  const a = left.transport_profiles ?? [LEGACY_PROFILE];
  const b = right.transport_profiles ?? [LEGACY_PROFILE];
  if (left.fixed_endpoint || right.fixed_endpoint) {
    return !left.relay_only && !right.relay_only && a.includes(FIXED_PROFILE) && b.includes(FIXED_PROFILE) ? FIXED_PROFILE : null;
  }
  if (a.includes(ICE_PROFILE) && b.includes(ICE_PROFILE)) return ICE_PROFILE;
  if (left.relay_only || right.relay_only) return null;
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
  async persist(record) {
    await this.state.storage.put(`transport:${record.id}`, record);
  }
  async scheduleCleanup() {
    let next = null;
    for (const record of this.records.values()) {
      if (!record.online && Number.isFinite(record.expires_at)) next = next === null ? record.expires_at : Math.min(next, record.expires_at);
    }
    const current = await this.state.storage.getAlarm?.();
    if (next !== null && current !== next) await this.state.storage.setAlarm(next);
    else if (next === null && current != null) await this.state.storage.deleteAlarm?.();
  }
  async markOfflineForReconnect(name) {
    let changed = false;
    for (const record of this.records.values()) {
      if (!record.online || (record.a !== name && record.b !== name && record.hole?.device !== name)) continue;
      await this.revokeHole(record);
      this.revokePeerRelay(record);
      record.online = false;
      record.expires_at = Date.now() + OFFLINE_RETENTION_MS;
      await this.persist(record);
      changed = true;
    }
    if (changed) await this.scheduleCleanup();
  }
  async expireOffline() {
    const now = Date.now();
    for (const [key, record] of this.records) {
      if (!record.online && record.expires_at <= now) { this.records.delete(key); await this.state.storage.delete(`transport:${record.id}`); }
    }
    await this.scheduleCleanup();
  }
  member(name) { return this.room.members.get(name); }
  hasOnlineTransport(name, member) {
    return [...this.records.values()].some(record => {
      if (!record.online || record.blocked || (record.a !== name && record.b !== name)) return false;
      const index = record.a === name ? 0 : 1;
      return record.runtime?.[index] === member.runtime_id &&
        record.fingerprints?.[index] === member.cert_fingerprint &&
        record.epochs?.[index] === member.transport_epoch;
    });
  }
  async activate(left, right, matches, seen, voice = false) {
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
    const roundMode = Array.isArray(a.member.relay_order) && Array.isArray(b.member.relay_order);
    const relayOrders = roundMode ? [a.member.relay_order, b.member.relay_order] : null;
    const pairing = roundMode && a.member.relay_pairing === ALL_PAIRS && b.member.relay_pairing === ALL_PAIRS ? ALL_PAIRS : "";
    const relayOnly = [a.member.relay_only === true, b.member.relay_only === true];
    const relayRequired = relayOnly.some(Boolean);
    const profile = negotiateProfile(a.member, b.member);
    // Mixed ordering cannot share a punch handshake; use ordinary ICE instead.
    const sameDirectOrder=(a.member.direct_order??"")===(b.member.direct_order??"");
    const punchVersion=profile===ICE_PROFILE && !relayRequired && sameDirectOrder && a.member.punch_version===PUNCH_VERSION && b.member.punch_version===PUNCH_VERSION ? PUNCH_VERSION : "";
    const directOrder=punchVersion && a.member.direct_order===DIRECT_ORDER ? DIRECT_ORDER : "";
    const punchSchedule=punchVersion && a.member.punch_schedule===PUNCH_SCHEDULE && b.member.punch_schedule===PUNCH_SCHEDULE ? PUNCH_SCHEDULE : "";
    const punchCalibration=directOrder && punchSchedule && a.member.punch_calibration===PUNCH_CALIBRATION && b.member.punch_calibration===PUNCH_CALIBRATION ? PUNCH_CALIBRATION : "";
    const punchConePool=punchCalibration && a.member.punch_cone_pool===PUNCH_CONE_POOL && b.member.punch_cone_pool===PUNCH_CONE_POOL ? PUNCH_CONE_POOL : "";
    const wantsHole=profile!==FIXED_PROFILE && !!(a.member.hole_device || b.member.hole_device);
    if (wantsHole && (a.member.hole_version!==HOLE_VERSION || b.member.hole_version!==HOLE_VERSION || !roundMode || pairing!==ALL_PAIRS)) {
      for (const entry of [a,b]) fault(entry.ws,'peer_upgrade_required','Hole 中继需要双方更新客户端');
      if (record) {await this.revokeHole(record);record.online=false;await this.persist(record);} return;
    }
    const holeDevices=wantsHole?[a.member.hole_device||'',b.member.hole_device||'']:null;
    if (holeDevices?.some(n=>n===a.member.device_name || n===b.member.device_name)) {
      for (const entry of [a,b]) fault(entry.ws,'invalid_relay_device','中继不能是设备对的任一端');return;
    }
    const relayPlan=wantsHole?deviceRelayPlan(...relayOrders,...holeDevices):null;
    const identities=relayPlan?.filter(p=>p.device).map(p=> {
      const m=this.member(p.device)?.member;
      return [p.device,m?.runtime_id??'',m?.transport_epoch??'',m?.fixed_endpoint??'',m?.hole_serve??null];
    })??null;
    // Backup relay availability does not invalidate an authenticated direct
    // path (or a path through another relay). Only the selected Hole matters.
    const holeIdentity = values => values?.find(([device])=>device===record?.hole?.device) ?? null;
    const selectedHoleChanged = record?.hole && !same(holeIdentity(record.hole_identities),holeIdentity(identities));
    const fixedEndpoints = [a.member.fixed_endpoint || "", b.member.fixed_endpoint || ""];
    const liveConfig = a.member.live_config === true && b.member.live_config === true;
    const peerRelay = profile===ICE_PROFILE && !relayRequired && pairing===ALL_PAIRS &&
      [a,b].every(e=>e.member.peer_relay_version===PEER_RELAY_VERSION);
    const voiceChanged = record && record.voice !== voice;
    const voiceRevision = !record ? "1" : String(BigInt(record.voice_revision ?? "1") + (voiceChanged ? 1n : 0n));
    let changed = false;
    let resetPath = false;
    if (!record) {
      if (this.records.size >= 128) { fault(a.ws, "transport_limit", "房间传输记录达到上限"); fault(b.ws, "transport_limit", "房间传输记录达到上限"); return; }
      record = { id: crypto.randomUUID(), pair_key: key, a: a.member.device_name, b: b.member.device_name, generation: "1", phase: "direct", round: 0, round_mode: roundMode, relay_orders: relayOrders, runtime, fingerprints, epochs, mappings, voice, ice: {}, retry: 0 };
      changed = true;
      resetPath = true;
    } else if ((record.punch_cone_pool??"")!==punchConePool || (record.live_config ?? false) !== liveConfig || (record.punch_calibration??"")!==punchCalibration || (record.direct_order??"")!==directOrder || (record.punch_schedule??"")!==punchSchedule || (record.punch_version??"")!==punchVersion || (record.hole && record.lease_until<=Date.now()) || !same(record.hole_devices??null,holeDevices) || selectedHoleChanged || (record.profile ?? ICE_PROFILE) !== profile || !same(record.fixed_endpoints ?? ["", ""], fixedEndpoints) || (voiceChanged && !liveConfig) || !same(record.runtime, runtime) || !same(record.fingerprints, fingerprints) || !same(record.epochs, epochs) || !same(record.relay_only ?? [false,false], relayOnly) || (record.relay_policy ?? "") !== relayPolicy || (record.round_mode ?? false) !== roundMode || (record.relay_pairing ?? "") !== pairing || (roundMode && !same(record.relay_orders, relayOrders))) {
      record.generation = (BigInt(record.generation) + 1n).toString(); record.phase = "direct"; record.round = 0; record.ice = {}; record.retry = 0;
      record.runtime = runtime; record.fingerprints = fingerprints; record.epochs = epochs; record.blocked = null; record.voice = voice; changed = true;
      resetPath = true;
    } else if (!record.online) {
      // A short signaling outage leaves the authenticated data path usable.
      // Resume it only while the same runtime, certificate and network epoch
      // are still declared, and never revive a generation that was blocked by
      // an identity or protocol failure.
      const resumable = !record.blocked && record.lease_until > Date.now() && Number.isFinite(record.resume_until) && record.resume_until > Date.now();
      if (resumable) {
        record.resume_until = 0;
        changed = true;
      } else {
        record.generation = (BigInt(record.generation) + 1n).toString();
        record.phase = "direct"; record.round = 0; record.ice = {}; record.retry = 0;
        record.blocked = null;
        changed = true;
        resetPath = true;
      }
    }
    if (!same(record.mappings, mappings)) changed = true;
    if (!same(record.hole_identities??null,identities)) changed = true;
    if (record.peer_relay && !peerRelay) {
      this.revokePeerRelay(record);
      record.generation=(BigInt(record.generation)+1n).toString();
      record.round=0;record.phase='direct';record.ice={};resetPath=true;changed=true;
    }
    if (voiceChanged) changed = true;
    record.live_config = liveConfig;
    record.voice_revision = voiceRevision;
    if (!record.online) changed = true;
    if (resetPath) await this.revokeHole(record);
    if (resetPath) this.revokePeerRelay(record);
    record.peer_relay_enabled = peerRelay;
    record.hole_version=wantsHole?HOLE_VERSION:'';
    record.hole_devices=holeDevices;record.hole_identities=identities;record.relay_plan=relayPlan;
    record.relay_policy = relayPolicy;
    record.round_mode = roundMode;
    record.round = roundMode ? (record.round ?? 0) : 0;
    record.relay_orders = relayOrders;
    record.relay_pairing = pairing;
    record.relay_only = relayOnly;
    record.relay_required = relayRequired;
    record.profile = profile;
    record.punch_version = punchVersion;
    record.direct_order = directOrder;
    record.punch_schedule = punchSchedule;
    record.punch_calibration = punchCalibration;
    record.punch_cone_pool = punchConePool;
    record.fixed_endpoints = fixedEndpoints;
    if (resetPath) {
      record.manual_probe = false;
      record.direct_stage = 'ice';
      record.direct_failures = 0;
      record.fixed_direction = 0;
      record.round = roundMode && relayRequired ? usableRelayRound(record, 1) : 0;
      record.phase = relayRequired ? "relay_udp" : "direct";
      record.retry_after_ms = 0;
    }
    record.mappings = mappings; record.voice = voice; record.online = true; record.resume_until = 0; record.lease_until = Date.now() + LEASE_MS;
    this.records.set(key, record);
    // Reconcile also runs after hibernation and before live mapping updates.
    // Refresh the route before persisting or replaying it to either endpoint.
    this.grantPeerRelay(record);
    if (wantsHole && !record.peer_relay && (resetPath || !record.hole)) await this.prepareHole(record);
    if (changed) { record.expires_at = Date.now() + OFFLINE_RETENTION_MS; await this.persist(record); }
    if (changed) { this.notify(record, a.member.device_name, true); this.notify(record, b.member.device_name, true); }
  }
  async revokeHole(record) {
    if (!record.hole) return;
    const relay=this.member(record.hole.device);
    if (relay) send(relay.ws,{type:'hole_revoke',transport_id:record.id,transport_generation:record.hole.generation});
    record.hole=null;
  }
  revokePeerRelay(record) {
    const grant=record.peer_relay;
    if (grant) {
      const relay=this.member(grant.device);
      if (relay) send(relay.ws,{type:'peer_relay_revoke',peer_relay:grant});
    }
    record.peer_relay=null;
  }
  directLink(a,b) {
    const r=this.records.get(pairKey(a,b));
    if (!r?.online || r.lease_until<=Date.now() || r.peer_relay || r.hole) return null;
    for (const [i,name] of [r.a,r.b].entries()) {
      const m=this.member(name)?.member, proof=r.established?.[name];
      if (!m || m.peer_relay_version!==PEER_RELAY_VERSION || m.runtime_id!==r.runtime[i] || m.transport_epoch!==r.epochs[i] ||
          proof?.generation!==r.generation || !['direct','fixed'].includes(proof.path_type)) return null;
    }
    return {id:r.id,generation:r.generation};
  }
  preparePeerRelay(record) {
    if (!record.peer_relay_enabled) return false;
    for (const device of [...this.room.members.keys()].sort()) {
      if (device===record.a || device===record.b) continue;
      const relay=this.member(device)?.member;
      if (relay?.peer_relay_version!==PEER_RELAY_VERSION || relay.relay_only) continue;
      if ([...this.records.values()].filter(r=>r.online && r.lease_until>Date.now() && r.peer_relay?.device===device).length>=8) continue;
      const links=[this.directLink(record.a,device),this.directLink(record.b,device)];
      if (links.some(link=>!link)) continue;
      record.peer_relay={route:randomHex(16),device,runtime:relay.runtime_id,devices:[record.a,record.b],runtimes:record.runtime,
        links,transport_id:record.id,generation:record.generation,lease_until:String(record.lease_until)};
      this.grantPeerRelay(record);
      return true;
    }
    return false;
  }
  grantPeerRelay(record) {
    const g=record.peer_relay, relay=g&&this.member(g.device);
    if (!g || !record.online || record.lease_until<=Date.now()) return;
    g.lease_until=String(record.lease_until);
    if (relay?.member.runtime_id!==g.runtime) return;
    send(relay.ws,{type:'peer_relay_grant',peer_relay_version:PEER_RELAY_VERSION,transport_id:record.id,
      transport_generation:record.generation,peer_relay:g});
  }
  async prepareHole(record) {
    await this.revokeHole(record);
    const device=record.relay_plan?.[record.round-1]?.device;
    if (!device || !record.online) return;
    const relay=this.member(device)?.member;
    const unavailable=(reason,code='hole_unavailable')=>{record.hole={device,error:reason,error_code:code,generation:record.generation};};
    if (relay && relay.hole_version!==HOLE_VERSION) {unavailable('中继设备需要升级客户端','peer_upgrade_required');return;}
    if (!relay?.hole_serve?.enabled || !relay.fixed_endpoint) {unavailable('中继设备暂未在线或未开启服务');return;}
    const count=[...this.records.values()].filter(r=>r!==record && r.online && r.lease_until>Date.now() && r.hole?.device===device && !r.hole.error).length;
    if (count>=relay.hole_serve.max_pairs) {unavailable('中继设备容量已满');return;}
    record.hole={device,endpoint:relay.fixed_endpoint,relay_runtime:relay.runtime_id,relay_epoch:relay.transport_epoch,
      route:randomHex(16),keys:[randomHex(32),randomHex(32)],generation:record.generation,
      devices:[record.a,record.b],runtimes:record.runtime,epochs:record.epochs};
    this.grantHole(record);
  }
  grantHole(record) {
    const h=record.hole,relay=h&&this.member(h.device);
    if (!record.online || record.lease_until<=Date.now() || !h || h.error || !relay || !relay.member.hole_serve?.enabled || relay.member.runtime_id!==h.relay_runtime || relay.member.transport_epoch!==h.relay_epoch || relay.member.fixed_endpoint!==h.endpoint) return;
    send(relay.ws,{type:'hole_grant',hole_version:HOLE_VERSION,transport_id:record.id,transport_generation:record.generation,
      runtime_id:h.relay_runtime,transport_epoch:h.relay_epoch,lease_until:String(record.lease_until),hole:h});
  }
  notify(record, name, replay = false) {
    const own = this.member(name); if (!own) return;
    const index = name === record.a ? 0 : 1;
    const peerName = index === 0 ? record.b : record.a;
    const fixed = record.profile === FIXED_PROFILE;
    const direction = fixed ? fixedDirection(record) : {};
    const phase = record.peer_relay ? 'peer_relay' : fixed ? "fixed" : record.round_mode ? recordPhase(record, record.round ?? 0, index) : record.phase;
    const ready = { type: "transport_ready", transport_id: record.id, transport_generation: record.generation, profile: record.profile ?? ICE_PROFILE, session_version: 2, phase, relay_policy: fixed ? "" : record.relay_policy ?? "", peer_device: peerName, peer_runtime_id: record.runtime[1-index], peer_fingerprint: record.fingerprints[1-index], runtime_id: record.runtime[index], initiator_id: fixed ? direction.dialer_id : record.a, ...direction, mappings: record.mappings, voice: record.voice === true, live_config: record.live_config === true, voice_revision: record.voice_revision ?? "1", lease_until: String(record.lease_until), retry_after_ms: record.retry_after_ms ?? 0 };
    if (record.punch_version) ready.punch_version = record.punch_version;
    if (record.manual_probe) ready.manual_probe = true;
    if (record.peer_relay) Object.assign(ready,{peer_relay_version:PEER_RELAY_VERSION,peer_relay:record.peer_relay});
    if (record.direct_order) Object.assign(ready,{direct_order:record.direct_order,direct_stage:record.direct_stage??'ice'});
    if (record.punch_schedule) ready.punch_schedule = record.punch_schedule;
    if (record.punch_calibration) ready.punch_calibration = record.punch_calibration;
    if (record.punch_cone_pool) ready.punch_cone_pool = record.punch_cone_pool;
    if (record.hole_version) Object.assign(ready,{hole_version:HOLE_VERSION,hole_device:record.hole_devices[index],peer_hole_device:record.hole_devices[1-index],relay_plan:record.relay_plan,hole:record.hole});
    if (!fixed && record.round_mode) Object.assign(ready, {relay_round:record.round ?? 0,relay_order:record.relay_orders[index],peer_relay_order:record.relay_orders[1-index]});
    if (!fixed && record.relay_pairing) ready.relay_pairing = record.relay_pairing;
    Object.assign(ready, {relay_only_policy:RELAY_ONLY_POLICY, relay_required:record.relay_required === true,
      relay_only:record.relay_only?.[index] === true, peer_relay_only:record.relay_only?.[1-index] === true});
    send(own.ws, ready);
    // Capability and credentials are scoped to this authorized generation.
    if (replay && !fixed) {
      const cached = record.ice[peerName]; if (!cached) return;
      const common = { transport_id: record.id, transport_generation: record.generation, peer_device: peerName };
      if (cached.description) send(own.ws, { ...common, type: "ice_description", ...cached.description });
      for (const candidate of cached.candidates ?? []) send(own.ws, { ...common, type: "ice_candidate", candidate });
      if (cached.end) send(own.ws, { ...common, type: "ice_candidate", end_of_candidates: true });
      for (const punch of cached.punch ?? []) send(own.ws, {...common,type:'nat_punch',punch});
      if (cached.nat_status) send(own.ws, {...common,type:'nat_status',nat:cached.nat_status});
    }
  }
  async finish(seen, resumeNames = new Set()) {
    for (const [key, record] of this.records) {
      if (seen.has(key)) continue;
      const a = this.member(record.a), b = this.member(record.b);
      const removed = !!(a && b);
      const wasOnline = record.online;
      // If one signaling member vanished while the pair still has a valid
      // transport identity, retain the generation briefly. A reconnect can
      // then replay the ready message and keep the existing QUIC path.
      const resumable = !removed && !record.blocked && record.lease_until > Date.now() &&
        (resumeNames.has(record.a) || resumeNames.has(record.b)) &&
        (wasOnline || record.resume_until > Date.now());
      if (record.online || removed) for (const [own, peer] of [[a, record.b], [b, record.a]]) if (own && !resumable) send(own.ws, { type: "peer_state", peer_device: peer, transport_id: record.id, reason: removed ? "mapping_removed" : "peer_offline" });
      if (!resumable) {
        await this.revokeHole(record);
        this.revokePeerRelay(record);
      }
      record.online = false;
      // Retention starts when the peer goes offline, not when it first joined.
      if (wasOnline) {
        record.expires_at = Date.now() + OFFLINE_RETENTION_MS;
        if (resumable) record.resume_until = Date.now() + SIGNAL_RESUME_GRACE_MS;
      }
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
      for (const [device,runtime,epoch] of record.hole_identities??[]) {
        if (device===member.device_name && runtime===member.runtime_id && epoch && BigInt(member.transport_epoch)<BigInt(epoch)) return true;
      }
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
    if (!['peer_relay_failed','nat_punch','nat_status','hole_failed','transport_sync','transport_renew','transport_restart','ice_description','ice_candidate','transport_established','transport_failed','turn_request'].includes(message.type)) return false;
    if (member.signal_version !== 2) { fault(ws, "protocol_mismatch", "此消息需要新版协调协议", message); return true; }
    if (!this.takeRate(ws) || new TextEncoder().encode(JSON.stringify(message)).length > 64*1024) { fault(ws, "rate_limited", "协调消息过于频繁", message); return true; }
    const name = member.device_name;
    if (message.type==='peer_relay_failed') {
      const r=[...this.records.values()].find(r=>r.online && r.id===message.transport_id && r.generation===message.transport_generation && r.peer_relay?.device===name && r.peer_relay.runtime===member.runtime_id);
      if (r) {
        const own=this.member(r.a);
        if (own) await this.handle(own.ws,own.member,{type:'transport_restart',transport_id:r.id,expected_generation:r.generation,
          relay_round:r.round,reason:'peer_relay_failed'});
      }
      return true;
    }
    if (message.type==='hole_failed') {
      const record=[...this.records.values()].find(r=>r.id===message.transport_id && r.hole?.device===name && r.hole.relay_runtime===member.runtime_id && r.hole.generation===message.transport_generation);
      if (record) {
        await this.revokeHole(record);record.hole={device:name,error:'中继路由不可用',generation:record.generation};
        await this.persist(record);
        this.notify(record,record.a);this.notify(record,record.b);
      } return true;
    }
    if (message.type === "transport_sync" || message.type === "transport_renew") {
      const leases = [];
      for (const r of this.records.values()) if (r.online && r.hole?.device===name) this.grantHole(r);
      for (const r of this.records.values()) if (r.online && r.peer_relay?.device===name) this.grantPeerRelay(r);
      for (const record of this.records.values()) if ((record.a === name || record.b === name) && record.online) {
        // A lease is a fresh authorization reply, not persistent topology.
        // Reconstructing this value after hibernation requires no storage write.
        if (record.lease_until<=Date.now() && record.hole && !record.hole.error) {
          record.generation=(BigInt(record.generation)+1n).toString();record.ice={};
          record.lease_until=Date.now()+LEASE_MS;await this.prepareHole(record);await this.persist(record);
          this.notify(record,record.a);this.notify(record,record.b);
        }
        record.lease_until = Date.now() + LEASE_MS;
        this.grantHole(record);
        this.grantPeerRelay(record);
        if (message.type === "transport_sync") this.notify(record, name, true);
        else leases.push({ transport_id: record.id, transport_generation: record.generation, lease_until: String(record.lease_until) });
      }
      if (message.type === "transport_renew") send(ws, { type: "transport_lease", request_id: message.request_id, leases });
      return true;
    }
    if (message.type === "turn_request") { await this.turn(ws, member, message); return true; }
    const record = [...this.records.values()].find(r => r.id === message.transport_id && (r.a === name || r.b === name));
    if (!record || !record.online || (record.mappings.length === 0 && record.voice !== true)) { fault(ws, "transport_not_authorized", "当前设备没有该对端传输关系", message); return true; }
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
      // A successful endpoint can only lose its path explicitly. Delayed
      // discovery failures must not restart an already-established generation.
      if (record.established?.[name]?.generation===record.generation &&
          ['ice_failed','path_failed','direct_exhausted'].includes(message.reason)) {
        this.notify(record,name,true); return true;
      }
      const manual = message.reason==='manual_renomination';
      if (record.profile === FIXED_PROFILE) {
        if (message.phase !== 'fixed') { fault(ws, 'invalid_phase', '固定入口连接只能重试固定路径', message); return true; }
        record.generation = (BigInt(record.generation) + 1n).toString();
        record.fixed_direction = (record.fixed_direction ?? 0) + 1;
        record.retry = manual ? 1 : (record.retry ?? 0) + 1;
        record.retry_after_ms = manual ? 0 : Math.min(15_000, 1000 * 2 ** Math.min(record.retry - 1, 4));
        record.ice = {}; record.lease_until = Date.now() + LEASE_MS;
        await this.persist(record); this.notify(record, record.a); this.notify(record, record.b); return true;
      }
      let wrapped = false;
      const direct = !record.peer_relay && (record.round_mode ? record.round===0 : record.phase==='direct');
      if (record.direct_order===DIRECT_ORDER && direct && record.direct_stage==='punch' && message.reason==='ice_failed') {
        this.notify(record,name,true); return true;
      }
      const startPunch = record.direct_order===DIRECT_ORDER && direct && record.direct_stage!=='punch' && message.reason==='ice_failed';
      const retryDirect = record.punch_version && direct && message.reason==='path_failed' && (record.direct_failures??0)<2;
      if (record.round_mode) {
        const ownOrder = record.relay_orders[record.a === name ? 0 : 1];
        const peerOrder = record.relay_orders[record.a === name ? 1 : 0];
        const allPairs = record.relay_pairing === ALL_PAIRS;
        const maxRound = maxRelayRound(record);
        const requested = manual ? 0 : message.relay_round;
        const expectedPhase = allPairs ? recordPhase(record, requested, record.a === name ? 0 : 1) : relayPhase(requested, ownOrder);
        if (!Number.isSafeInteger(requested) || requested < 0 || requested > maxRound + 1 ||
            (!manual && (allPairs ? (message.phase !== undefined && message.phase !== expectedPhase) : message.phase !== expectedPhase))) { fault(ws, "invalid_phase", "路径轮次或本机阶段无效", message); return true; }
        const current = record.round ?? 0;
        if (requested !== 0 && requested !== current && requested !== current + 1) { fault(ws, "invalid_phase", "路径轮次应按顺序推进", message); return true; }
        let next = requested > maxRound ? 0 : requested;
        wrapped = next === 0;
        if (record.relay_required) {
          next = usableRelayRound(record, next || 1);
          if (!next) { next = usableRelayRound(record, 1); wrapped = true; }
        }
        record.round = next;
      } else {
        const phaseOrder = record.relay_policy === RELAY_POLICY ? orderedPhases : compatiblePhases;
        const phase = manual ? 'direct' : message.phase;
        if (!phaseOrder.includes(phase)) { fault(ws, "invalid_phase", "路径阶段无效", message); return true; }
        const current = phaseOrder.indexOf(record.phase), requested = phaseOrder.indexOf(phase);
        if (requested !== 0 && requested !== current && requested !== current + 1) { fault(ws, "invalid_phase", "路径阶段应按顺序推进", message); return true; }
        wrapped = phase === "direct";
        record.phase = wrapped && record.relay_required ? phaseOrder[1] : phase;
      }
      if (record.punch_version && direct && message.reason==='path_failed') record.direct_failures=(record.direct_failures??0)+1;
      if (startPunch || retryDirect) { record.round=0; record.phase='direct'; wrapped=false; }
      else if (!direct) record.direct_failures=0;
      record.direct_stage = startPunch || (retryDirect && record.direct_stage==='punch') ? 'punch' : 'ice';
      if (startPunch) record.direct_failures=0;
      if (manual) { record.direct_failures=0; record.retry=0; }
      record.manual_probe = manual || (record.manual_probe===true && !wrapped &&
        ['ice_failed','path_failed','direct_exhausted','peer_relay_failed'].includes(message.reason));
      record.generation = (BigInt(record.generation) + 1n).toString(); record.ice = {};
      record.retry = (record.retry ?? 0) + 1;
      record.relay_only ??= [false,false];
      record.retry_after_ms = wrapped && !manual && message.reason !== "turn_refresh" ? Math.min(15_000, 1000 * 2 ** Math.min(record.retry, 4)) : 0;
      record.lease_until = Date.now() + LEASE_MS;
      this.revokePeerRelay(record);
      const tryPeer=direct && !startPunch && !retryDirect && ['ice_failed','path_failed','direct_exhausted'].includes(message.reason);
      if (tryPeer) this.preparePeerRelay(record);
      if (record.hole_version && !record.peer_relay) await this.prepareHole(record);
      await this.persist(record); this.notify(record, record.a); this.notify(record, record.b); return true;
    }
    if (!decimal(message.transport_generation) || message.transport_generation !== record.generation) return true;
    if (message.type === "transport_established") {
      if (record.manual_probe) { record.manual_probe=false; await this.persist(record); }
      if (['direct','fixed'].includes(message.path_type)) {
        record.established??={};
        record.established[name]={generation:record.generation,path_type:message.path_type};
        await this.persist(record);
      }
      record.direct_failures=0;
      if (record.retry || record.retry_after_ms) { record.retry = 0; record.retry_after_ms = 0; await this.persist(record); }
      return true;
    }
    if (record.profile === FIXED_PROFILE) { fault(ws, 'invalid_phase', '固定入口连接不接受 ICE 消息', message); return true; }
    if (message.to_peer_id !== peerName) { fault(ws, "transport_not_authorized", "ICE 消息目标不属于该传输", message); return true; }
    const cached = record.ice[name] ?? { candidates: [], candidate_bytes: 0 };
    let outgoing;
    if(message.type==='nat_punch') {
      const index=record.a===name?0:1, p=message.punch;
      const direct=record.round_mode?record.round===0:record.phase==='direct';
      if(!record.punch_version || !direct || record.relay_required || record.lease_until<=Date.now() || record.runtime[index]!==member.runtime_id || record.epochs[index]!==member.transport_epoch) return true;
      const rejectCalibration=reason=>{
        if(p?.kind==='calibrated') send(ws,{type:'error',code:'punch_calibration_rejected',
          message:`第 ${p.round} 轮校准 ${p.trial} 结果未转发：${reason}`,request_id:message.request_id,
          transport_id:record.id,transport_generation:record.generation,punch:{kind:p.kind,round:p.round,trial:p.trial}});
        return true;
      };
      if(!validPunch(p)) return rejectCalibration('结果格式或端口计划无效');
      if(record.direct_order===DIRECT_ORDER && record.direct_stage!=='punch') return true;
      cached.punch??=[];
      const previous=cached.punch.find(v=>v.kind===p.kind && v.round===p.round && (!['calibrate','calibrated'].includes(p.kind) || v.trial===p.trial));
      if(previous) return true;
      if(cached.punch.length>=(record.punch_cone_pool?80:record.punch_calibration?40:record.punch_schedule?20:12)) return rejectCalibration('本代次协调消息达到上限');
      const other=record.ice[peerName]?.punch??[];
      const both=[cached.punch,other];
      let pool;
      if (p.round>3) {
        if (record.punch_cone_pool!==PUNCH_CONE_POOL || !['pool_select','calibrate','calibrated','go','failed'].includes(p.kind)) return true;
        pool=conePoolState(both,p.round);
        if (!pool || p.mode!==undefined && p.mode!==pool.mode) return rejectCalibration('换 IP 条件或双方模式不一致');
        if (p.kind==='pool_select') {
          if (pool.cone!==0 || p.plan.ip===pool.original.ip || !pool.model.ip_evidence.some(e=>e.ip===p.plan.ip) ||
              !samePunchPlan(p.plan,{...pool.original,ip:p.plan.ip}) || both[0].some(v=>v.kind==='pool_select' && v.plan.ip===p.plan.ip)) return true;
        } else if (!pool.selection) return rejectCalibration('锥形端尚未选择备用 IP');
        if (p.kind==='calibrated' && p.result==='ok' && pool.cone===0 && !samePunchPlan(p.plan,pool.selection.plan)) return rejectCalibration('锥形端未沿用保留的端口计划');
      }
      if (p.kind==='pool_select' && !pool) return true;
      if(['ready','calibrated'].includes(p.kind) && p.mode==='pool' && (!cached.punch.some(v=>v.kind==='offer' && v.pool_prediction===true) || !other.some(v=>v.kind==='offer' && v.pool_prediction===true))) return rejectCalibration('双方未声明地址池扫描能力');
      if(['reserve','reserved','model'].includes(p.kind) && record.punch_schedule!==PUNCH_SCHEDULE) return true;
      if(p.kind==='reserve' && name!==record.a) return true;
      if(p.kind==='reserved' && (name!==record.b || !other.some(v=>v.kind==='reserve'))) return true;
      if(record.punch_schedule && ['model','calibrate','calibrated','ready','go'].includes(p.kind)) {
        const first=name===record.a?cached.punch:other, second=name===record.b?cached.punch:other;
        if(!first.some(v=>v.kind==='reserve') || !second.some(v=>v.kind==='reserved')) return rejectCalibration('双方打洞时段预约尚未完成');
      }
      if (['calibrate','calibrated'].includes(p.kind) || p.trial!==undefined) {
        if (record.punch_calibration!==PUNCH_CALIBRATION) return rejectCalibration('双方未协商同步校准能力');
      }
      if (record.punch_calibration && ['calibrate','calibrated','ready','go'].includes(p.kind)) {
        if (p.kind==='ready') return true; // Successful calibrated messages carry the plans.
        if (!integer(p.trial,1,3)) return true;
        const at=(list,kind,trial=p.trial)=>list.find(v=>v.kind===kind && v.round===p.round && (kind==='model' || v.trial===trial));
        if (both.some(list=>list.some(v=>v.kind==='abort' || v.kind==='failed' && v.round===p.round))) return true; // Late results after a round ended are harmless.
        if (p.kind==='calibrate') {
          if (!pool && !both.every(list=>at(list,'model')) || both.some(list=>list.some(v=>v.kind==='go' && v.round===p.round))) return true;
          if (p.trial>1) {
            const results=both.map(list=>at(list,'calibrated',p.trial-1)?.result);
            if (!results.every(v=>v==='ok' || v==='retry') || !results.includes('retry')) return true;
          }
        } else if (p.kind==='calibrated') {
          if (!both.every(list=>at(list,'calibrate'))) return rejectCalibration('未收到双方同轮同序号的准备确认');
        } else {
          if (!both.every(list=>at(list,'calibrated')?.result==='ok')) return true;
          if (both.some(list=>list.some(v=>v.round===p.round && v.trial>p.trial))) return true;
        }
      }
      if(p.kind==='go' && (name!==record.a || !record.punch_calibration && (!cached.punch.some(v=>v.kind==='ready' && v.round===p.round) || !other.some(v=>v.kind==='ready' && v.round===p.round)))) return true;
      if(p.kind==='ice_start' && name!==record.a) return true;
      cached.punch.push(p); outgoing={type:'nat_punch',punch:p};
    } else if(message.type==='nat_status') {
      const index=record.a===name?0:1;
      if(record.relay_required || record.lease_until<=Date.now() || record.runtime[index]!==member.runtime_id || record.epochs[index]!==member.transport_epoch || !validNATPlan(message.nat) || (cached.nat_status_at && Date.now()-cached.nat_status_at<30_000))return true;
      cached.nat_status=message.nat;cached.nat_status_at=Date.now();outgoing={type:'nat_status',nat:message.nat};
    } else if (message.type === "ice_description") {
      if (typeof message.ufrag !== "string" || !/^[A-Za-z0-9+/_=-]{4,256}$/.test(message.ufrag) || typeof message.pwd !== "string" || message.pwd.length < 22 || message.pwd.length > 256) { fault(ws,"invalid_ice_description","ICE 描述格式无效",message); return true; }
      const description = { ufrag: message.ufrag, pwd: message.pwd };
      if (cached.description && !same(cached.description, description)) { fault(ws,"invalid_ice_description","同一代次的 ICE 凭据已固定",message); return true; }
      cached.description = description; outgoing = { type: message.type, ...description };
    } else if (message.end_of_candidates === true) { cached.end = true; outgoing = { type: message.type, end_of_candidates: true }; }
    else {
      const candidate = message.candidate;
      if (typeof candidate !== "string" || candidate.length > 2048 || candidate.length < 10 || /[\r\n]/.test(candidate)) { fault(ws,"invalid_ice_candidate","ICE 候选格式无效",message); return true; }
      if (record.relay_only?.[record.a === name ? 0 : 1] && !/\styp relay(?:\s|$)/.test(candidate)) { fault(ws,"invalid_ice_candidate","仅中继设备不能发布直连候选",message); return true; }
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
    if (![...this.records.values()].some(r => r.online && r.profile !== FIXED_PROFILE && (r.a === member.device_name || r.b === member.device_name))) { fault(ws,"transport_not_authorized","先建立有效的非固定入口设备关系再请求中继",message); return; }
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
  if (scheme === "turns" && protocol === "tcp") return port === "5349" ? 3 : port === "443" ? 4 : Infinity;
  if (scheme === "turn" && protocol === "tcp") return port === "3478" ? 1 : port === "80" ? 2 : Infinity;
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
    const key=JSON.stringify([identityKey,ttl,RELAY_POLICY,"standard-ports-first"]);
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
