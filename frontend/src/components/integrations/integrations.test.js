// Workflow tests for the Developer Integrations UI: the real components, mounted in jsdom, against a fake API.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ApiKeysPanel from './ApiKeysPanel.svelte';
import ConnectionsPanel from './ConnectionsPanel.svelte';
import ActivityPanel from './ActivityPanel.svelte';
import ConfirmHost from '../ConfirmHost.svelte';
import { operationLabel, scopeSummary, isoDateIn } from '../../lib/integrations.js';

const catalog = [
  { scope: 'semesters:read', group: 'semesters', access: 'read', label: 'See semesters', description: 'd' },
  { scope: 'grades:read', group: 'grades', access: 'read', label: 'See grades', description: 'd' },
  { scope: 'statistics:read', group: 'statistics', access: 'read', label: 'See averages', description: 'd' },
  { scope: 'semesters:write', group: 'semesters', access: 'write', label: 'Manage semesters', description: 'd' },
  { scope: 'grades:write', group: 'grades', access: 'write', label: 'Enter grades', description: 'd' },
];
const meta = { scopes: catalog, mcpUrl: 'https://gradly.example/mcp', limits: { perCredential: 100, perUser: 150, windowSeconds: 300 } };
const SECRET = 'gradly_sk_ONLY-SHOWN-ONCE-abcdefghijklmnopqrstuvwxyz0123456';

// ---------- fake API ----------

let calls;
let routes;

function json(status, body) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
}

beforeEach(() => {
  calls = [];
  routes = {};
  globalThis.fetch = (url, opts = {}) => {
    const method = opts.method ?? 'GET';
    const path = String(url).replace(/^\/api/, '');
    const body = opts.body ? JSON.parse(opts.body) : undefined;
    calls.push({ method, path, body, headers: opts.headers });
    const handler = routes[`${method} ${path.split('?')[0]}`];
    if (!handler) return json(404, { error: 'Not found.' });
    const [status, out] = typeof handler === 'function' ? handler(body, path) : [200, handler];
    return json(status, out);
  };
});

let mounted = [];
afterEach(() => {
  mounted.forEach((c) => unmount(c));
  mounted = [];
  document.body.innerHTML = '';
});

function render(Component, props = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  mounted.push(mount(Component, { target, props }));
  return target;
}

async function settle() {
  for (let i = 0; i < 6; i++) {
    await new Promise((r) => setTimeout(r, 0));
    flushSync();
  }
}

const byText = (root, selector, text) => [...root.querySelectorAll(selector)].find((el) => el.textContent.trim().includes(text));
const click = async (el) => {
  el.click();
  await settle();
};
async function confirmYes() {
  await settle();
  const dialog = document.querySelector('[role=alertdialog]');
  expect(dialog).not.toBeNull();
  await click(dialog.querySelectorAll('button')[1]);
}
function type(input, value) {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function key(overrides = {}) {
  return {
    id: 'k1', name: 'Hermes', description: null, prefix: 'gradly_sk_AbCdEf…', scopes: ['semesters:read', 'grades:read', 'statistics:read'],
    status: 'active', requestCount: 3, createdAt: '2026-10-01T10:00:00Z', lastUsedAt: null, expiresAt: null, rotatedAt: null, revokedAt: null,
    ...overrides,
  };
}

// ---------- API keys ----------

describe('API keys', () => {
  it('shows an empty state with a call to action', async () => {
    routes['GET /integrations/keys'] = { items: [] };
    const root = render(ApiKeysPanel, { meta });
    await settle();
    expect(root.textContent).toContain('No API keys yet');
    expect(byText(root, 'button', 'Create your first key')).toBeTruthy();
  });

  it('shows a loading state, then the keys with their permissions and status', async () => {
    let release;
    routes['GET /integrations/keys'] = { items: [key(), key({ id: 'k2', name: 'Old', status: 'revoked', scopes: catalog.map((s) => s.scope) })] };
    const slow = globalThis.fetch;
    globalThis.fetch = (...args) => new Promise((r) => (release = () => r(slow(...args))));
    const root = render(ApiKeysPanel, { meta });
    flushSync();
    expect(root.querySelector('.loading')).not.toBeNull();
    release();
    await settle();
    const rows = root.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain('Read-only');
    expect(rows[0].textContent).toContain('Active');
    expect(rows[0].textContent).toContain('Never');
    expect(rows[1].textContent).toContain('Full access');
    expect(rows[1].textContent).toContain('Revoked');
    // A revoked key can only be deleted.
    expect(byText(rows[1], 'button', 'Rotate')).toBeUndefined();
    expect(byText(rows[1], 'button', 'Delete')).toBeTruthy();
  });

  it('creates a key with read-only permissions by default and shows the secret exactly once', async () => {
    let listed = [];
    routes['GET /integrations/keys'] = () => [200, { items: listed }];
    routes['POST /integrations/keys'] = (body) => {
      listed = [key({ name: body.name, scopes: body.scopes })];
      return [201, { key: listed[0], secret: SECRET }];
    };
    const root = render(ApiKeysPanel, { meta });
    render(ConfirmHost);
    await settle();

    await click(byText(root, 'button', 'Create key'));
    const dialog = document.querySelector('[role=dialog]');
    type(dialog.querySelector('#key-name'), 'Hermes');
    await click(byText(dialog, 'button', 'Create key'));

    const post = calls.find((c) => c.method === 'POST');
    expect(post.body).toEqual({ name: 'Hermes', description: null, scopes: ['semesters:read', 'grades:read', 'statistics:read'] });
    expect(post.headers['X-Gradly']).toBe('1');
    const shown = document.querySelector('[data-testid=secret]');
    expect(shown.value).toBe(SECRET);
    expect(document.body.textContent).toContain('only time the full key is shown');

    await click(byText(document.body, 'button', "I've stored it safely"));
    expect(document.querySelector('[data-testid=secret]')).toBeNull();
    expect(document.body.innerHTML).not.toContain(SECRET);
    expect(root.querySelectorAll('tbody tr')).toHaveLength(1);
  });

  it('applies the Full access preset and sends an expiry date', async () => {
    routes['GET /integrations/keys'] = { items: [] };
    routes['POST /integrations/keys'] = () => [201, { key: key(), secret: SECRET }];
    const root = render(ApiKeysPanel, { meta });
    await settle();
    await click(byText(root, 'button', 'Create key'));
    const dialog = document.querySelector('[role=dialog]');
    type(dialog.querySelector('#key-name'), 'Script');
    await click(byText(dialog, 'button', 'Full access'));
    const select = dialog.querySelector('#key-expiry');
    select.value = '30';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(dialog.textContent).toContain('can change your academic data');
    await click(byText(dialog, 'button', 'Create key'));
    const post = calls.find((c) => c.method === 'POST');
    expect(post.body.scopes).toEqual(catalog.map((s) => s.scope));
    expect(post.body.expiresAt).toBe(isoDateIn(30));
  });

  it('refuses to submit without a name or permissions, and shows server errors', async () => {
    routes['GET /integrations/keys'] = { items: [] };
    routes['POST /integrations/keys'] = () => [400, { error: 'You already have 25 API keys. Revoke one you no longer use first.' }];
    const root = render(ApiKeysPanel, { meta });
    await settle();
    await click(byText(root, 'button', 'Create key'));
    const dialog = document.querySelector('[role=dialog]');
    await click(byText(dialog, 'button', 'Create key'));
    expect(dialog.querySelector('[role=alert]').textContent).toContain('Give the key a name');
    type(dialog.querySelector('#key-name'), 'x');
    await click(byText(dialog, 'button', 'Clear'));
    await click(byText(dialog, 'button', 'Create key'));
    expect(dialog.querySelector('[role=alert]').textContent).toContain('at least one permission');
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
    await click(byText(dialog, 'button', 'Read-only'));
    await click(byText(dialog, 'button', 'Create key'));
    expect(dialog.querySelector('[role=alert]').textContent).toContain('25 API keys');
  });

  it('turning on "change" also turns on "view" for the same data', async () => {
    routes['GET /integrations/keys'] = { items: [] };
    const root = render(ApiKeysPanel, { meta });
    await settle();
    await click(byText(root, 'button', 'Create key'));
    const dialog = document.querySelector('[role=dialog]');
    await click(byText(dialog, 'button', 'Clear'));
    const write = dialog.querySelector('#key-scope-grades\\:write');
    write.checked = true;
    write.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(dialog.querySelector('#key-scope-grades\\:read').checked).toBe(true);
    expect(dialog.querySelector('#key-scope-semesters\\:read').checked).toBe(false);
  });

  it('rotates a key after confirmation and shows the new secret', async () => {
    routes['GET /integrations/keys'] = { items: [key()] };
    routes['POST /integrations/keys/k1/rotate'] = () => [200, { key: key(), secret: SECRET }];
    const root = render(ApiKeysPanel, { meta });
    render(ConfirmHost);
    await settle();
    byText(root, 'button', 'Rotate').click();
    await confirmYes();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/integrations/keys/k1/rotate')).toBe(true);
    expect(root.querySelector('[data-testid=secret]').value).toBe(SECRET);
  });

  it('does nothing when a revoke is cancelled, and revokes after confirmation', async () => {
    routes['GET /integrations/keys'] = { items: [key()] };
    routes['POST /integrations/keys/k1/revoke'] = () => [200, key({ status: 'revoked' })];
    const root = render(ApiKeysPanel, { meta });
    render(ConfirmHost);
    await settle();
    byText(root, 'button', 'Revoke').click();
    await settle();
    await click(byText(document.querySelector('[role=alertdialog]'), 'button', 'Cancel'));
    expect(calls.some((c) => c.path.endsWith('/revoke'))).toBe(false);
    byText(root, 'button', 'Revoke').click();
    await confirmYes();
    expect(calls.filter((c) => c.path === '/integrations/keys/k1/revoke')).toHaveLength(1);
  });

  it('shows a retry when the list cannot be loaded', async () => {
    routes['GET /integrations/keys'] = () => [500, { error: 'An unexpected error occurred. Please try again.' }];
    const root = render(ApiKeysPanel, { meta });
    await settle();
    expect(root.querySelector('[role=alert]').textContent).toContain('unexpected error');
    routes['GET /integrations/keys'] = { items: [key()] };
    await click(byText(root, 'button', 'Try again'));
    expect(root.querySelectorAll('tbody tr')).toHaveLength(1);
  });
});

// ---------- OAuth connections ----------

describe('MCP connections', () => {
  const app = {
    id: 'g1', clientId: 'gradly_client_x', clientName: 'Hermes Agent', redirectHost: 'http://127.0.0.1:47999', scopes: ['grades:read'],
    status: 'connected', requestCount: 4, authorizedAt: '2026-10-30T10:00:00Z', updatedAt: '2026-10-30T10:00:00Z', lastUsedAt: null, revokedAt: null,
  };

  it('shows the endpoint, setup instructions and an empty state', async () => {
    routes['GET /integrations/apps'] = { items: [] };
    const root = render(ConnectionsPanel, { meta });
    await settle();
    expect(root.querySelector('input[aria-label="MCP endpoint"]').value).toBe('https://gradly.example/mcp');
    expect(root.textContent).toContain('auth: oauth');
    expect(root.textContent).toContain('No connected applications');
    await click(byText(root, 'button', 'Use an API key'));
    expect(root.textContent).toContain('Authorization: "Bearer YOUR_GRADLY_API_KEY"');
  });

  it('lists connected applications and disconnects one after confirmation', async () => {
    let items = [app];
    routes['GET /integrations/apps'] = () => [200, { items }];
    routes['POST /integrations/apps/g1/revoke'] = () => {
      items = [{ ...app, status: 'revoked' }];
      return [200, { ok: true }];
    };
    const root = render(ConnectionsPanel, { meta });
    render(ConfirmHost);
    await settle();
    expect(root.textContent).toContain('Hermes Agent');
    expect(root.textContent).toContain('See grades');
    byText(root, 'button', 'Disconnect').click();
    await confirmYes();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/integrations/apps/g1/revoke')).toBe(true);
    expect(root.textContent).toContain('Disconnected');
    expect(byText(root, 'button', 'Remove')).toBeTruthy();
  });
});

// ---------- activity ----------

describe('Activity', () => {
  const event = (id, over = {}) => ({
    id, time: '2026-10-31T10:30:00Z', credentialType: 'api_key', credentialId: 'k1', credentialName: 'Hermes',
    operation: 'add_grade', category: 'write', success: true, status: 200, errorCode: null, durationMs: 12, ...over,
  });

  it('shows events, filters them and pages through them', async () => {
    routes['GET /integrations/activity'] = (_, path) => {
      const q = new URLSearchParams(path.split('?')[1]);
      const items = q.get('status') === 'failure' ? [event(2, { success: false, status: 404, errorCode: 'not_found', operation: 'delete_module' })] : [event(1)];
      return [200, { items, page: Number(q.get('page')), pageSize: 25, total: q.get('status') ? 1 : 30,
        credentials: [{ id: 'k1', type: 'api_key', name: 'Hermes' }], operations: ['add_grade', 'delete_module'], retentionDays: 90 }];
    };
    const root = render(ActivityPanel);
    await settle();
    expect(root.textContent).toContain('Added a grade');
    expect(root.textContent).toContain('Success');
    expect(root.textContent).toContain('page 1 of 2');

    await click(byText(root, 'button', 'Next'));
    expect(calls.at(-1).path).toContain('page=2');

    const status = root.querySelector('#f-status');
    status.value = 'failure';
    status.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    const last = new URLSearchParams(calls.at(-1).path.split('?')[1]);
    expect(last.get('status')).toBe('failure');
    expect(last.get('page')).toBe('1');
    expect(root.textContent).toContain('Deleted a module');
    expect(root.textContent).toContain('Not found');

    const cred = root.querySelector('#f-cred');
    cred.value = 'k1';
    cred.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(new URLSearchParams(calls.at(-1).path.split('?')[1]).get('credential')).toBe('k1');
  });

  it('distinguishes "no activity" from "no matches"', async () => {
    routes['GET /integrations/activity'] = { items: [], page: 1, pageSize: 25, total: 0, credentials: [], operations: [], retentionDays: 90 };
    const root = render(ActivityPanel);
    await settle();
    expect(root.textContent).toContain('No activity yet');
    const status = root.querySelector('#f-status');
    status.value = 'success';
    status.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(root.textContent).toContain('Nothing matches these filters');
  });
});

describe('helpers', () => {
  it('summarises permissions in plain words', () => {
    expect(scopeSummary(catalog.map((s) => s.scope), catalog)).toBe('Full access');
    expect(scopeSummary(['semesters:read', 'grades:read', 'statistics:read'], catalog)).toBe('Read-only');
    expect(scopeSummary(['grades:read'], catalog)).toBe('See grades');
    expect(scopeSummary(['grades:read', 'grades:write'], catalog)).toBe('2 permissions');
  });

  it('names operations for people', () => {
    expect(operationLabel('update_formula')).toBe('Updated a formula');
    expect(operationLabel('something_new')).toBe('something_new');
  });
});
