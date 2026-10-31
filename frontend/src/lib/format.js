// Formatting helpers. Everything is culture-invariant so the same figure looks the same everywhere.

/** Up to two decimals, no trailing zeros ("8", "8.5", "9.67"). */
export function num(v) {
  if (v === null || v === undefined) return '—';
  const s = (Math.round(v * 100) / 100).toFixed(2).replace(/\.?0+$/, '');
  return s === '-0' || s === '' ? '0' : s;
}

/** Always two decimals ("12.50"). */
export function fixed2(v) {
  return v === null || v === undefined ? '—' : v.toFixed(2);
}

const DATE = { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' };

export function date(value) {
  if (!value) return '—';
  return new Date(value).toLocaleDateString('en-US', DATE);
}

export function stamp(value) {
  const d = new Date(value);
  const hh = String(d.getUTCHours()).padStart(2, '0');
  const mm = String(d.getUTCMinutes()).padStart(2, '0');
  return `${date(value)} ${hh}:${mm} UTC`;
}

export function bytes(n) {
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return i === 0 ? `${n} B` : `${num(v)} ${units[i]}`;
}

/** The semester average, or an em dash when there isn't one yet. */
export function average(summary) {
  return summary.average === null ? '—' : fixed2(summary.average);
}

/** A value's position on its scale as a CSS percentage, clamped to 0-100. */
export function percent(value, scale) {
  const p = scale <= 0 ? 0 : Math.min(100, Math.max(0, (value / scale) * 100));
  return `${Math.round(p * 100) / 100}%`;
}

export function plural(count, noun) {
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

/** Grade input value: empty when not entered. */
export function inputValue(v) {
  return v === null || v === undefined ? '' : num(v);
}

/** Parses a grade field. Returns {ok, value}; an empty field is a valid "not entered yet". */
export function parseGrade(text) {
  const t = String(text ?? '').trim();
  if (t === '') return { ok: true, value: null };
  const n = Number(t);
  return Number.isFinite(n) ? { ok: true, value: n } : { ok: false, value: null };
}

/** Only same-site relative paths are acceptable redirect targets (prevents open redirects). */
export function safeLocal(url, fallback = '/') {
  if (!url || url[0] !== '/') return fallback;
  if (url.length > 1 && (url[1] === '/' || url[1] === '\\')) return fallback;
  if (/[\r\n]/.test(url)) return fallback;
  return url;
}
