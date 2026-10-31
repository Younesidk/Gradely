// App-wide reactive state: the signed-in user, toasts, the confirm dialog and the theme.
import { api, onUnauthorized } from './api.js';
import { navigate, route } from './router.svelte.js';

// ---------- auth ----------

export const auth = $state({ user: null, ready: false });

export async function loadUser() {
  try {
    auth.user = await api.get('/auth/me');
  } catch {
    auth.user = null;
  }
  auth.ready = true;
}

export async function signOut(notice = 'signed-out') {
  try {
    await api.post('/auth/logout');
  } catch {
    /* the cookie is cleared server-side when reachable; either way we leave */
  }
  auth.user = null;
  navigate('/account/login?notice=' + notice);
}

// A session that disappears mid-use (expired, account disabled) sends the user to sign in again.
onUnauthorized(() => {
  if (!auth.user) return;
  auth.user = null;
  navigate('/account/login?returnUrl=' + encodeURIComponent(route.path + location.search), { replace: true });
});

// ---------- toasts ----------

export const toasts = $state({ items: [] });
let toastId = 0;

function pushToast(kind, text) {
  const id = ++toastId;
  toasts.items.push({ id, kind, text });
  setTimeout(() => dismissToast(id), kind === 'error' ? 7000 : 4000);
}

export function dismissToast(id) {
  const i = toasts.items.findIndex((t) => t.id === id);
  if (i >= 0) toasts.items.splice(i, 1);
}

export const toast = {
  success: (text) => pushToast('success', text),
  error: (text) => pushToast('error', text),
  info: (text) => pushToast('info', text),
};

// ---------- confirm dialog ----------

export const confirmState = $state({ request: null });

/** Asks the user to confirm. Resolves true on confirm, false on cancel. */
export function confirmDialog(title, message, confirmText = 'Confirm', danger = true) {
  return new Promise((resolve) => {
    confirmState.request?.resolve(false);
    confirmState.request = { title, message, confirmText, danger, resolve };
  });
}

export function closeConfirm(result) {
  const r = confirmState.request;
  confirmState.request = null;
  r?.resolve(result);
}

// ---------- theme ----------

export function toggleTheme() {
  const next = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', next);
  try {
    localStorage.setItem('gradely-theme', next);
  } catch {
    /* storage unavailable */
  }
}

export async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through */
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
