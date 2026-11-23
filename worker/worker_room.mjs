import { modernMember, versionAtLeast, WORKER_VERSION } from './software_version.mjs';

const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const MAX_FRAME = 128 * 1024;
const MAX_MEMBERS = 128;
const MAX_HOPS = 16;
class RoomNoticeTooLarge extends Error {}
const encoded = value => encoder.encode(value);
const b64 = bytes => {
  let text = '';
  for (let i = 0; i < bytes.length; i += 8192) text += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(text);
};
const unb64 = (value, maximum = MAX_FRAME) => {
  if (typeof value !== 'string' || value.length > maximum * 4 / 3 + 4 || !/^[A-Za-z0-9+/]*={0,2}$/.test(value)) throw new Error('invalid base64');
  const text = atob(value);
  if (text.length > maximum) throw new Error('room message too large');
  return Uint8Array.from(text, c => c.charCodeAt(0));
};
const hex = bytes => [...bytes].map(n => n.toString(16).padStart(2, '0')).join('');
const decimal = value => typeof value === 'string' && /^(0|[1-9]\d*)$/.test(value) && BigInt(value) <= 0xffffffffffffffffn;

export function roomSigningBytes(frame) {
  const values = [frame.room, frame.id, frame.origin, frame.runtime, frame.target ?? '', frame.target_runtime ?? '', frame.kind, frame.body, frame.certificate ?? ''].map(encoded);
  const bytes = new Uint8Array(4 + values.reduce((n, value) => n + 4 + value.length, 0));
  bytes.set(encoded('HRC1'));
  const view = new DataView(bytes.buffer);
  let offset = 4;
  for (const value of values) {
    view.setUint32(offset, value.length); offset += 4;
    bytes.set(value, offset); offset += value.length;
  }
  return bytes;
}

function derElement(bytes, start) {
  if (start + 2 > bytes.length) throw new Error('invalid certificate');
  let length = bytes[start + 1], content = start + 2;
  if (length & 128) {
    const count = length & 127;
    if (!count || count > 4 || content + count > bytes.length) throw new Error('invalid certificate');
    length = 0;
    for (let i = 0; i < count; i++) length = length * 256 + bytes[content++];
  }
  if (content + length > bytes.length) throw new Error('invalid certificate');
  return { tag: bytes[start], start, content, end: content + length };
}

export async function roomCertificate(member) {
  const bytes = unb64(member.certificate, 3072);
  if (hex(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))) !== member.cert_fingerprint) throw new Error('certificate fingerprint mismatch');
  const certificate = derElement(bytes, 0);
  const tbs = derElement(bytes, certificate.content);
  if (certificate.tag !== 48 || tbs.tag !== 48) throw new Error('invalid certificate');
  let offset = tbs.content;
  if (bytes[offset] === 160) offset = derElement(bytes, offset).end;
  for (let i = 0; i < 5; i++) offset = derElement(bytes, offset).end;
  const spki = derElement(bytes, offset);
  if (spki.tag !== 48 || spki.end > tbs.end) throw new Error('invalid certificate public key');
  const publicBytes = bytes.slice(spki.start, spki.end);
  const verify = await crypto.subtle.importKey('spki', publicBytes, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['verify']);
  const encrypt = await crypto.subtle.importKey('spki', publicBytes, { name: 'ECDH', namedCurve: 'P-256' }, false, []);
  return { verify, encrypt };
}

export class RoomControl {
  constructor(room, state) {
    this.room = room; this.state = state;
    this.nodes = new Map(); this.keys = new Map(); this.notices = []; this.noticeBytes = 0;
    this.privateKey = null; this.publicKey = ''; this.revision = 0; this.lastSnapshot = '';
    this.keyInit = null; this.publishing = Promise.resolve(); this.graphUpdates = Promise.resolve();
    this.flushing = Promise.resolve();
  }

  async init() {
    const key = await this.state.storage.get('room_control_key');
    if (key) {
      this.publicKey = key.public;
      this.privateKey = await crypto.subtle.importKey('jwk', key.private, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign']);
    }
    this.revision = (await this.state.storage.get('room_revision')) ?? 0;
    for (const frame of (await this.state.storage.get('room_nodes')) ?? []) {
      const member = this.room.registered.get(frame.origin)?.member;
      if (member && modernMember(member)) {
        try { await this.acceptNode(frame, member); } catch {}
      }
    }
  }

  async ensureKey() {
    if (this.privateKey) return;
    if (!this.keyInit) this.keyInit = this.createKey().finally(() => { this.keyInit = null; });
    return this.keyInit;
  }

  async createKey() {
    const pair = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
    const publicKey = b64(new Uint8Array(await crypto.subtle.exportKey('spki', pair.publicKey)));
    await this.state.storage.put('room_control_key', { private: await crypto.subtle.exportKey('jwk', pair.privateKey), public: publicKey });
    this.privateKey = pair.privateKey;
    this.publicKey = publicKey;
  }

  async certificate(member) {
    const key = member.runtime_id + '/' + member.cert_fingerprint;
    if (!this.keys.has(key)) {
      if (this.keys.size >= MAX_MEMBERS * 2) this.keys.clear();
      this.keys.set(key, await roomCertificate(member));
    }
    return this.keys.get(key);
  }

  proxy(device) {
    let attachment = {};
    return { proxy: true, send: raw => this.enqueue(device, JSON.parse(raw)), deserializeAttachment: () => attachment, serializeAttachment: value => { attachment = value; } };
  }

  enqueue(target, message) {
    const size = encoded(JSON.stringify(message)).length;
    if (size > MAX_FRAME || this.notices.length >= 512 || this.noticeBytes + size > 512 * 1024) throw new Error('room notice queue is full');
    const member = this.room.registered.get(target)?.member;
    this.noticeBytes += size;
    this.notices.push({ target, message, size, runtime: member?.runtime_id, fingerprint: member?.cert_fingerprint });
  }

  noticeMember(notice) {
    const member = this.room.registered.get(notice.target)?.member;
    return member && modernMember(member) && member.runtime_id === notice.runtime && member.cert_fingerprint === notice.fingerprint ? member : null;
  }

  discardNotice(notice) {
    const index = this.notices.indexOf(notice);
    if (index < 0) return;
    this.notices.splice(index, 1);
    this.noticeBytes -= notice.size;
  }

  async acceptNode(frame, member, nodes = this.nodes) {
    if (!frame || frame.room !== this.room.ice.roomName || frame.origin !== member.device_name || frame.runtime !== member.runtime_id || frame.kind !== 'node' || !/^[a-f0-9]{32}$/.test(frame.id ?? '') || !Number.isInteger(frame.hops) || frame.hops < 0 || frame.hops > MAX_HOPS || JSON.stringify(frame).length > MAX_FRAME || frame.certificate !== member.certificate) throw new Error('invalid room node envelope');
    const key = await this.certificate(member);
    if (!await crypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, key.verify, unb64(frame.signature, 64), roomSigningBytes(frame))) throw new Error('invalid room node signature');
    const node = JSON.parse(decoder.decode(unb64(frame.body)));
    if (typeof node.worker_version !== 'string' || node.worker_version !== '' && !versionAtLeast(node.worker_version, '0.0.0') || node.signal_recovering !== undefined && typeof node.signal_recovering !== 'boolean') throw new Error('invalid room node version or state');
    if (node.device_name !== member.device_name || node.runtime_id !== member.runtime_id || node.core_version !== member.core_version || !decimal(node.revision) || node.revision === '0' || !decimal(node.transport_epoch) || typeof node.signal_online !== 'boolean' || typeof node.resident !== 'boolean' || !Array.isArray(node.neighbours) || node.neighbours.length > 16) throw new Error('invalid room node');
    const neighbours = new Set();
    for (const link of node.neighbours) {
      if (!/^[a-zA-Z0-9._-]{1,128}$/.test(link.peer_id ?? '') || link.peer_id === node.device_name || neighbours.has(link.peer_id) || !/^[a-f0-9]{32}$/.test(link.runtime_id ?? '') || typeof link.transport_id !== 'string' || !link.transport_id || link.transport_id.length > 256 || !decimal(link.generation)) throw new Error('invalid room link');
      neighbours.add(link.peer_id);
    }
    const current = this.room.registered.get(member.device_name)?.member;
    if (!current || current.runtime_id !== member.runtime_id || current.cert_fingerprint !== member.cert_fingerprint || current.core_version !== node.core_version || BigInt(node.transport_epoch) < BigInt(current.transport_epoch)) return false;
    const previous = nodes.get(member.device_name);
    if (previous?.node.runtime_id === node.runtime_id && BigInt(previous.node.revision) >= BigInt(node.revision)) return false;
    nodes.set(member.device_name, { node, frame });
    return true;
  }

  update(frames) {
    const next = this.graphUpdates.catch(() => {}).then(() => this.updateGraph(frames));
    this.graphUpdates = next;
    return next;
  }

  async updateGraph(frames) {
    if (!Array.isArray(frames) || frames.length > 16) throw new Error('invalid room graph');
    const nodes = new Map(this.nodes);
    const updates = new Map();
    for (const frame of frames) {
      const member = this.room.registered.get(frame?.origin)?.member;
      // A carrier can still have declarations queued for an earlier identity.
      if (!member || !modernMember(member) || frame.runtime !== member.runtime_id || frame.certificate !== member.certificate) continue;
      if (await this.acceptNode(frame, member, nodes)) updates.set(frame.origin, nodes.get(frame.origin));
    }
    let changed = false;
    for (const [device, value] of updates) {
      const current = this.room.registered.get(device)?.member;
      // Verification awaits crypto operations; a join may advance the epoch
      // before this batch commits. Keep the other current declarations.
      if (!current || current.runtime_id !== value.node.runtime_id || current.certificate !== value.frame.certificate || current.core_version !== value.node.core_version || BigInt(value.node.transport_epoch) < BigInt(current.transport_epoch)) continue;
      this.nodes.set(device, value);
      changed = true;
    }
    if (changed) await this.state.storage.put('room_nodes', [...this.nodes.values()].map(value => value.frame));
    return changed;
  }

  neighbours(device) {
    const entry = this.room.registered.get(device), record = this.nodes.get(device);
    if (!entry || record?.node.runtime_id !== entry.member.runtime_id || BigInt(record.node.transport_epoch) < BigInt(entry.member.transport_epoch)) return [];
    return record.node.neighbours.filter(link => {
      const other = this.room.registered.get(link.peer_id), reverse = this.nodes.get(link.peer_id)?.node;
      if (!other || other.member.runtime_id !== link.runtime_id || reverse?.runtime_id !== link.runtime_id || BigInt(reverse.transport_epoch) < BigInt(other.member.transport_epoch)) return false;
      return reverse.neighbours.some(back => back.peer_id === device && back.runtime_id === entry.member.runtime_id && back.transport_id === link.transport_id && back.generation === link.generation);
    }).map(link => link.peer_id);
  }

  component(device) {
    const members = new Set([device]), queue = [device];
    while (queue.length) for (const next of this.neighbours(queue.shift())) if (!members.has(next)) { members.add(next); queue.push(next); }
    return [...members].sort();
  }

  direct(device) {
    const ws = this.room.registered.get(device)?.ws;
    return ws && !ws.proxy && !ws.closed && (ws.readyState === undefined || ws.readyState === 1) ? ws : null;
  }

  rebuild() {
    const active = new Set();
    for (const [device] of this.room.registered) if (this.direct(device)) for (const peer of this.component(device)) active.add(peer);
    this.room.members.clear();
    for (const device of active) {
      const entry = this.room.registered.get(device);
      if (entry) this.room.members.set(device, { member: entry.member, ws: this.direct(device) ?? this.proxy(device) });
    }
  }

  snapshot() {
    const groups = [], seen = new Set();
    for (const device of [...this.room.members.keys()].sort()) {
      if (seen.has(device)) continue;
      const members = this.component(device).filter(peer => this.room.members.has(peer));
      members.forEach(peer => seen.add(peer));
      groups.push({ group_id: members[0], members, signaling: members.filter(peer => this.direct(peer)) });
    }
    const members = [...this.room.members.values()].map(({ member }) => ({
      device_name: member.device_name, voice: member.voice === true, runtime_id: member.runtime_id,
      core_version: member.core_version ?? '', worker_version: WORKER_VERSION,
      certificate: member.certificate ?? '', cert_fingerprint: member.cert_fingerprint,
      transport_epoch: member.transport_epoch, signal_online: !!this.direct(member.device_name),
      observed_worker_version: this.nodes.get(member.device_name)?.node.worker_version ?? member.worker_version ?? '',
      signal_recovering: !this.direct(member.device_name) && this.nodes.get(member.device_name)?.node.signal_recovering === true,
      group_id: groups.find(group => group.members.includes(member.device_name))?.group_id ?? member.device_name,
    })).sort((a, b) => a.device_name.localeCompare(b.device_name));
    const links = [];
    for (const [device, value] of this.nodes) if (this.room.members.has(device)) for (const link of value.node.neighbours) {
      if (device < link.peer_id && this.neighbours(device).includes(link.peer_id)) links.push({ from: device, to: link.peer_id, transport_id: link.transport_id, generation: link.generation });
    }
    return { worker_version: WORKER_VERSION, members, groups, links };
  }

  publishSnapshot() {
    const next = this.publishing.catch(() => {}).then(() => this.publishCurrentSnapshot());
    this.publishing = next;
    return next;
  }

  async publishCurrentSnapshot() {
    if (!this.privateKey) return;
    const snapshot = this.snapshot(), text = JSON.stringify(snapshot);
    if (text === this.lastSnapshot) return;
    const recipients = [...this.room.registered].filter(([device, entry]) => this.direct(device) && modernMember(entry.member)).map(([device]) => device);
    this.revision++;
    await this.state.storage.put('room_revision', this.revision);
    await this.state.storage.put('room_mirror', { ...snapshot, state_revision: String(this.revision) });
    const message = { type: 'room_state', version: 1, ...snapshot, state_revision: String(this.revision) };
    const frame = await this.signNotice('', message);
    const raw = JSON.stringify({ type: 'room_frame', room_frame: frame });
    let complete = true;
    for (const device of recipients) {
      const entry = this.room.registered.get(device);
      const ws = this.direct(device);
      if (!ws || !entry || !modernMember(entry.member)) { complete = false; continue; }
      try { ws.send(raw); } catch { complete = false; }
    }
    if (complete) this.lastSnapshot = text;
  }

  async signNotice(target, message, member = target ? this.room.registered.get(target)?.member : null) {
    await this.ensureKey();
    const id = hex(crypto.getRandomValues(new Uint8Array(16)));
    let body = encoded(JSON.stringify(message));
    if (member) {
      const recipient = await this.certificate(member);
      const pair = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
      const shared = await crypto.subtle.deriveBits({ name: 'ECDH', public: recipient.encrypt }, pair.privateKey, 256);
      const seed = await crypto.subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey']);
      const key = await crypto.subtle.deriveKey({ name: 'HKDF', hash: 'SHA-256', salt: encoded(this.room.ice.roomName), info: encoded('hole-room-notice:' + id) }, seed, { name: 'AES-GCM', length: 256 }, false, ['encrypt']);
      const nonce = crypto.getRandomValues(new Uint8Array(12));
      const ciphertext = await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce, additionalData: encoded(id) }, key, body);
      body = encoded(JSON.stringify({ key: b64(new Uint8Array(await crypto.subtle.exportKey('spki', pair.publicKey))), nonce: b64(nonce), ciphertext: b64(new Uint8Array(ciphertext)) }));
    }
    const frame = { id, room: this.room.ice.roomName, origin: '$worker', runtime: 'worker', kind: 'notice', body: b64(body), hops: MAX_HOPS };
    if (member) Object.assign(frame, { target, target_runtime: member.runtime_id });
    frame.signature = b64(new Uint8Array(await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, this.privateKey, roomSigningBytes(frame))));
    if (JSON.stringify(frame).length > MAX_FRAME) throw new RoomNoticeTooLarge('room notice too large');
    return frame;
  }

  flush() {
    const next = this.flushing.catch(() => {}).then(() => this.flushNotices());
    this.flushing = next;
    return next;
  }

  async flushNotices() {
    // Keep in-flight and retryable notices inside the bounded queue.
    for (const notice of [...this.notices]) {
      const member = this.noticeMember(notice);
      if (!member) { this.discardNotice(notice); continue; }
      if (!this.component(notice.target).some(device => this.direct(device))) continue;
      let frame;
      try { frame = await this.signNotice(notice.target, notice.message, member); }
      catch (error) {
        if (error instanceof RoomNoticeTooLarge || !this.noticeMember(notice)) this.discardNotice(notice);
        continue;
      }
      if (!this.noticeMember(notice)) { this.discardNotice(notice); continue; }
      const raw = JSON.stringify({ type: 'room_frame', room_frame: frame });
      let sent = false;
      // Signing and encryption yield; resolve the current routes afterwards.
      for (const device of this.component(notice.target)) {
        const ws = this.direct(device);
        if (!ws) continue;
        try { ws.send(raw); sent = true; } catch {}
      }
      if (sent) this.discardNotice(notice);
    }
  }
}
