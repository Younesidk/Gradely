// A tiny History-API router. Links are plain <a href> elements; same-origin clicks are handled client-side.

export const route = $state({
  path: location.pathname,
  query: new URLSearchParams(location.search),
});

function sync() {
  route.path = location.pathname;
  route.query = new URLSearchParams(location.search);
}

export function navigate(to, { replace = false } = {}) {
  if (replace) history.replaceState(null, '', to);
  else history.pushState(null, '', to);
  sync();
  window.scrollTo(0, 0);
}

window.addEventListener('popstate', sync);

document.addEventListener('click', (e) => {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  const a = e.target instanceof Element ? e.target.closest('a[href]') : null;
  if (!a || (a.target && a.target !== '_self') || a.hasAttribute('download')) return;
  const url = new URL(a.href, location.href);
  if (url.origin !== location.origin) return;
  e.preventDefault();
  navigate(url.pathname + url.search);
});

/** Matches "/semesters/:id" style patterns. Returns the params, or null. */
export function match(pattern, path) {
  const a = pattern.split('/').filter(Boolean);
  const b = path.split('/').filter(Boolean);
  if (a.length !== b.length) return null;
  const params = {};
  for (let i = 0; i < a.length; i++) {
    if (a[i].startsWith(':')) params[a[i].slice(1)] = decodeURIComponent(b[i]);
    else if (a[i] !== b[i]) return null;
  }
  return params;
}

/** True when the current path is href, or (unless exact) lives below it. */
export function isActive(href, exact = false) {
  if (exact || href === '/') return route.path === href;
  return route.path === href || route.path.startsWith(href + '/');
}
