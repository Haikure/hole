import { PANEL_PAGE } from './worker_panel_page.mjs';

const encoder = new TextEncoder();
const hex = bytes => [...new Uint8Array(bytes)].map(n => n.toString(16).padStart(2, '0')).join('');

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

export async function panelSession(secret, password = '') {
  const expiry = Math.floor(Date.now() / 1000) + 600;
  const nonce = hex(crypto.getRandomValues(new Uint8Array(16)));
  const value = 'hole.' + expiry + '.' + nonce;
  const signature = hex(await crypto.subtle.sign('HMAC', await keyFor(secret), encoder.encode(value + '\0' + password)));
  return value + '.' + signature;
}

export async function validPanelSession(session, secret, password = '') {
  if (typeof secret !== 'string' || !secret || typeof password !== 'string' || !password) return false;
  const match = /^hole\.(\d{10})\.([a-f0-9]{32})\.([a-f0-9]{64})$/.exec(session);
  if (!match) return false;
  const now = Math.floor(Date.now() / 1000), expiry = Number(match[1]);
  if (expiry < now || expiry > now + 600) return false;
  const signature = Uint8Array.from(match[3].match(/../g), byte => parseInt(byte, 16));
  return crypto.subtle.verify('HMAC', await keyFor(secret), signature, encoder.encode('hole.' + match[1] + '.' + match[2] + '\0' + password));
}

export function panelPage() {
  return new Response(PANEL_PAGE, { headers: {
    'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store',
    'Content-Security-Policy': "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
    'X-Content-Type-Options': 'nosniff', 'Referrer-Policy': 'no-referrer',
  } });
}
