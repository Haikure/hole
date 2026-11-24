import { PANEL_PAGE } from './worker_panel_page.mjs';

const encoder = new TextEncoder();
const hex = bytes => [...new Uint8Array(bytes)].map(n => n.toString(16).padStart(2, '0')).join('');
const COOKIE = '__Host-hole-panel';
const SESSION_PREFIX = 'panel_browser:';
const SESSION_LIMIT = 1024;
const SESSION_TTL = 12 * 60 * 60;
const REMEMBER_TTL = 7 * 24 * 60 * 60;
const REVOKE_RETRY_MS = 30_000;
export const PANEL_SESSION_NAME = '$hole-panel-sessions';

export function panelPath(value) {
  if (value === 'off') return null;
  const path = value === undefined ? '/panel' : value;
  return typeof path === 'string' && path !== '/ws' && /^\/(?:[a-zA-Z0-9_-]+)(?:\/[a-zA-Z0-9_-]+)*$/.test(path) ? path : null;
}

export function sameOrigin(request) {
  return request.headers.get('Origin') === new URL(request.url).origin;
}

async function keyFor(password) {
  return crypto.subtle.importKey('raw', encoder.encode(password), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign', 'verify']);
}

async function sign(key, value) {
  return hex(await crypto.subtle.sign('HMAC', await keyFor(key), encoder.encode(value)));
}

async function verify(key, value, signature) {
  if (typeof signature !== 'string' || !/^[a-f0-9]{64}$/.test(signature)) return false;
  return crypto.subtle.verify('HMAC', await keyFor(key), Uint8Array.from(signature.match(/../g), byte => parseInt(byte, 16)), encoder.encode(value));
}

export async function storedPanelKey(storage, name) {
  return storage.transaction(async txn => {
    let key = await txn.get(name);
    if (!key) {
      key = hex(crypto.getRandomValues(new Uint8Array(32)));
      await txn.put(name, key);
    }
    return key;
  });
}

export function panelRoomProof(key, room, token) {
  return sign(key, JSON.stringify(['panel-room', room, token]));
}

export function validPanelRoomProof(key, room, token, proof) {
  return token !== null && verify(key, JSON.stringify(['panel-room', room, token]), proof);
}

function cookieToken(request) {
  const values = (request.headers.get('Cookie') || '').split(';').map(v => v.trim()).filter(v => v.startsWith(COOKIE + '='));
  if (values.length !== 1) return '';
  const value = values[0].slice(COOKIE.length + 1);
  return /^[a-f0-9]{64}$/.test(value) ? value : '';
}

function cookie(value, maxAge) {
  return COOKIE + '=' + value + '; Path=/; Secure; HttpOnly; SameSite=Strict' + (maxAge === undefined ? '' : '; Max-Age=' + maxAge);
}

function result(data, status = 200, setCookie) {
  return Response.json(data, { status, headers: { 'Cache-Control': 'no-store', ...(setCookie ? { 'Set-Cookie': setCookie } : {}) } });
}

// Only opaque random IDs reach the browser. Stored records contain keyed password
// bindings and room grants, never a copy of either submitted password.
export class PanelSessions {
  constructor(state, env) { this.storage = state.storage; this.env = env; }

  async prune(txn, now) {
    const records = await txn.list({ prefix: SESSION_PREFIX });
    for (const [id, value] of records) if (value.expires <= now) { await txn.delete(id); records.delete(id); }
    const current = await txn.getAlarm();
    const retryAt = Math.min(current > now ? current : Infinity, now + REVOKE_RETRY_MS);
    if (records.size) await txn.setAlarm(Math.min(...[...records.values()].map(v => v.revoked ? Math.min(v.expires, retryAt) : v.expires)));
    else await txn.deleteAlarm();
    return records;
  }

  async finishRevoke(storageKey, record) {
    try {
      if (record.room) {
        const room = this.env.ROOMS.get(this.env.ROOMS.idFromName(record.room));
        const response = await room.fetch(new Request('https://panel-internal/__panel_room', { method: 'POST', body: JSON.stringify({ revoke: storageKey.slice(SESSION_PREFIX.length) }) }));
        if (!response.ok) return false;
      }
      await this.storage.delete(storageKey);
      return true;
    } catch { return false; }
  }

  async alarm() {
    for (const [id, record] of await this.storage.list({ prefix: SESSION_PREFIX })) {
      if (record.revoked && record.expires > Date.now()) await this.finishRevoke(id, record);
    }
    await this.storage.transaction(txn => this.prune(txn, Date.now()));
  }

  async fetch(request) {
    const data = await request.json();
    if (typeof data.password !== 'string' || !data.password || typeof data.audience !== 'string') return result({}, 401);
    this.keyPromise ||= storedPanelKey(this.storage, 'panel_session_key').catch(error => { this.keyPromise = null; throw error; });
    const key = await this.keyPromise;
    const binding = JSON.stringify(['panel-login', data.audience, data.password]);
    const token = data.action === 'issue' ? hex(crypto.getRandomValues(new Uint8Array(32))) : data.token;
    if (typeof token !== 'string' || !/^[a-f0-9]{64}$/.test(token)) return result({}, 401);
    const id = hex(await crypto.subtle.digest('SHA-256', encoder.encode(token)));
    const response = await this.storage.transaction(async txn => {
      const now = Date.now(), storageKey = SESSION_PREFIX + id;
      if (data.action === 'issue') {
        const records = await this.prune(txn, now);
        if (records.size >= SESSION_LIMIT) return result({}, 429);
        const persistent = data.remember === true;
        const expires = now + (persistent ? REMEMBER_TTL : SESSION_TTL) * 1000;
        await txn.put(storageKey, { auth: await sign(key, binding), expires, room: '', proof: '' });
        await txn.setAlarm(Math.min(expires, (await txn.getAlarm()) ?? Infinity));
        return result({ token, expires, persistent });
      }
      const record = await txn.get(storageKey);
      if (!record || record.expires <= now || (record.revoked && data.action !== 'revoke') || !await verify(key, binding, record.auth)) return result({}, 401);
      const previousRoom = record.room;
      if (data.action === 'revoke') {
        // Keep the room until its sockets are closed, even if the caller loses
        // its cookie or the cross-object notification fails.
        record.revoked = true;
        await txn.put(storageKey, record);
        const alarm = await txn.getAlarm();
        await txn.setAlarm(Math.min(alarm ?? Infinity, record.expires, now + REVOKE_RETRY_MS));
      }
      if (data.action === 'select') {
        record.room = data.room;
        record.proof = data.proof;
        await txn.put(storageKey, record);
      }
      return result({ id, room: record.room, proof: record.proof, expires: record.expires, previousRoom });
    });
    if (data.action === 'revoke' && response.ok && !await this.finishRevoke(SESSION_PREFIX + id, await response.clone().json())) return result({}, 503);
    return response;
  }
}

export async function handlePanel(request, env, passwordMatches) {
  const url = new URL(request.url), path = panelPath(env.PANEL_PATH);
  if (!path) return null;
  if (url.pathname === path && request.method === 'GET') return panelPage();
  const action = url.pathname.slice(path.length + 1);
  if (!url.pathname.startsWith(path + '/') || !['auth', 'session', 'room', 'logout', 'live'].includes(action)) return null;
  if (url.protocol !== 'https:' || !sameOrigin(request) || (action === 'live' ? request.headers.get('Upgrade')?.toLowerCase() !== 'websocket' : request.method !== 'POST')) return result({}, 403);
  const broker = () => env.ROOMS.get(env.ROOMS.idFromName(PANEL_SESSION_NAME));
  const call = (operation, extra = {}) => broker().fetch(new Request('https://panel-internal/__panel_session', { method: 'POST', body: JSON.stringify({ action: operation, token: cookieToken(request), password: env.PASSWORD, audience: url.origin + path, ...extra }) }));
  const roomCall = (room, body) => env.ROOMS.get(env.ROOMS.idFromName(room)).fetch(new Request('https://panel-internal/__panel_room?room=' + encodeURIComponent(room), { method: 'POST', body: JSON.stringify(body) }));
  let body;
  if (action === 'auth' || action === 'room') {
    try {
      const raw = await request.text();
      if (raw.length > 8192) return result({}, 413);
      body = JSON.parse(raw);
      if (!body || typeof body !== 'object' || Array.isArray(body)) return result({}, 400);
    } catch { return result({}, 400); }
  }
  if (action === 'auth') {
    if (!await passwordMatches(body.password, env.PASSWORD)) return result({}, 401);
    // Reauthentication replaces the previous browser session as well.
    const previous = await call('revoke');
    if (!previous.ok && previous.status !== 401) return result({}, previous.status);
    const issued = await call('issue', { remember: body.remember === true });
    if (!issued.ok) return result({}, issued.status);
    const session = await issued.json();
    return result({ room: '', expires: session.expires }, 200, cookie(session.token, session.persistent ? REMEMBER_TTL : undefined));
  }
  const checked = await call(action === 'logout' ? 'revoke' : 'check');
  if (!checked.ok) return result({}, checked.status, checked.status === 401 ? cookie('', 0) : undefined);
  const session = await checked.json();
  if (action === 'logout') {
    return result({}, 200, cookie('', 0));
  }
  if (action === 'session') {
    if (session.room) {
      const room = await roomCall(session.room, { proof: session.proof });
      if (!room.ok) return result({ room: '', roomExpired: true, expires: session.expires });
    }
    return result({ room: session.room, expires: session.expires });
  }
  if (action === 'room') {
    if (typeof body.room !== 'string' || body.room.length > 128 || body.room === PANEL_SESSION_NAME || (body.room && typeof body.token !== 'string')) return result({}, 400);
    let proof = '';
    if (body.room) {
      const verified = await roomCall(body.room, { token: body.token });
      if (!verified.ok) return result({}, 403);
      proof = (await verified.json()).proof;
    }
    const selected = await call('select', { room: body.room, proof });
    if (!selected.ok) return result({}, 401, cookie('', 0));
    const updated = await selected.json();
    if (updated.previousRoom) await roomCall(updated.previousRoom, { revoke: session.id });
    return result({ room: body.room, expires: session.expires });
  }
  if (!session.room) return result({}, 409);
  if (url.searchParams.has('room') && url.searchParams.get('room') !== session.room) return result({}, 403);
  const headers = new Headers(request.headers);
  headers.delete('Cookie');
  headers.delete('Sec-WebSocket-Protocol');
  headers.set('X-Hole-Panel-Proof', session.proof);
  headers.set('X-Hole-Panel-Session', session.id);
  headers.set('X-Hole-Panel-Expires', String(session.expires));
  url.searchParams.set('room', session.room);
  const opened = await env.ROOMS.get(env.ROOMS.idFromName(session.room)).fetch(new Request(url, { headers }));
  if (opened.status !== 101) return opened;
  // A logout/switch can race the cross-object upgrade. Once the socket exists,
  // recheck the record: a later revocation can now find and close that socket.
  const latest = await call('check');
  const current = latest.ok ? await latest.json() : null;
  if (!current || current.room !== session.room || current.proof !== session.proof) {
    await roomCall(session.room, { revoke: session.id });
    return result({}, current ? 409 : 401);
  }
  return opened;
}

export function panelPage() {
  return new Response(PANEL_PAGE, { headers: {
    'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store',
    'Content-Security-Policy': "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
    'X-Content-Type-Options': 'nosniff', 'Referrer-Policy': 'no-referrer',
  } });
}
