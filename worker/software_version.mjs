import versions from '../versions.json' with { type: 'json' };

export const WORKER_VERSION = versions.worker.version;
export const ROOM_MINIMUM_VERSION = '1.1.0';

export function versionAtLeast(value, minimum) {
  const parse = text => {
    const match = typeof text === 'string' && text.length <= 128 && /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:[-+][0-9A-Za-z.+-]+)?$/.exec(text);
    if (!match) return null;
    const parts = match.slice(1, 4).map(Number);
    return parts.every(n => Number.isSafeInteger(n) && n <= 0xffffffff) ? parts : null;
  };
  const a = parse(value), b = parse(minimum);
  if (!a || !b) return false;
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] > b[i];
  return true;
}

export const modernMember = member => versionAtLeast(member?.core_version, ROOM_MINIMUM_VERSION);
