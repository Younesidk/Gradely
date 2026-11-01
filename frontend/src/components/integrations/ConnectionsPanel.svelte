<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, confirmDialog, copyText } from '../../lib/state.svelte.js';
  import { date } from '../../lib/format.js';
  import { ago, scopeSummary } from '../../lib/integrations.js';
  import Icon from '../Icon.svelte';
  import Loading from '../Loading.svelte';
  import EmptyState from '../EmptyState.svelte';

  let { meta, onchange } = $props();

  let apps = $state(null);
  let loadError = $state(null);
  let busyId = $state(null);
  let guide = $state('oauth');

  async function load() {
    try {
      apps = (await api.get('/integrations/apps')).items;
      loadError = null;
    } catch (err) {
      loadError = err.message;
    }
  }
  onMount(load);

  async function copy(text, what) {
    if (await copyText(text)) toast.success(`${what} copied to clipboard`);
    else toast.info("Couldn't access the clipboard — select the text and copy it manually");
  }

  async function revoke(app) {
    const yes = await confirmDialog(`Disconnect ${app.clientName}?`,
      'Its access ends immediately. Your other connections and API keys keep working. You can connect it again later.', 'Disconnect');
    if (!yes) return;
    await run(app, () => api.post(`/integrations/apps/${app.id}/revoke`), `${app.clientName} disconnected`);
  }

  async function remove(app) {
    const yes = await confirmDialog(`Remove ${app.clientName} from the list?`, 'Its past activity stays in the activity log.', 'Remove');
    if (yes) await run(app, () => api.del(`/integrations/apps/${app.id}`), 'Removed');
  }

  async function run(app, action, success) {
    busyId = app.id;
    try {
      await action();
      toast.success(success);
      await load();
      onchange?.();
    } catch (err) {
      toast.error(err.message);
    } finally {
      busyId = null;
    }
  }

  const hermesOAuth = $derived(`mcp_servers:\n  gradly:\n    url: "${meta.mcpUrl}"\n    auth: oauth`);
  const hermesKey = $derived(`mcp_servers:\n  gradly:\n    url: "${meta.mcpUrl}"\n    headers:\n      Authorization: "Bearer YOUR_GRADLY_API_KEY"`);
  const genericKey = $derived(`{\n  "mcpServers": {\n    "gradly": {\n      "type": "http",\n      "url": "${meta.mcpUrl}",\n      "headers": { "Authorization": "Bearer YOUR_GRADLY_API_KEY" }\n    }\n  }\n}`);
</script>

<section class="card">
  <h2>Your MCP endpoint</h2>
  <p class="muted small">Add this address to any assistant that supports remote MCP servers (Streamable HTTP), such as Hermes or Claude.</p>
  <div class="link-box">
    <input class="input mono" readonly value={meta.mcpUrl} aria-label="MCP endpoint" onfocus={(e) => e.currentTarget.select()} />
    <button type="button" class="btn btn-primary btn-sm" onclick={() => copy(meta.mcpUrl, 'Endpoint')}><Icon name="copy" size={15} />Copy</button>
  </div>

  <div class="segmented" role="tablist" aria-label="Connection method">
    <button type="button" role="tab" aria-selected={guide === 'oauth'} class:active={guide === 'oauth'} onclick={() => (guide = 'oauth')}>Sign in with Gradly (OAuth)</button>
    <button type="button" role="tab" aria-selected={guide === 'key'} class:active={guide === 'key'} onclick={() => (guide = 'key')}>Use an API key</button>
  </div>

  {#if guide === 'oauth'}
    <ol class="steps steps-tight">
      <li><span class="step-num">1</span><div><strong>Add the endpoint in your assistant.</strong><p class="muted small">Paste the address above as a new remote MCP server. No key is needed.</p></div></li>
      <li><span class="step-num">2</span><div><strong>Sign in when asked.</strong><p class="muted small">Your assistant opens Gradly. Sign in with your usual account and choose what it may do.</p></div></li>
      <li><span class="step-num">3</span><div><strong>That's it.</strong><p class="muted small">The connection appears below, where you can disconnect it at any time.</p></div></li>
    </ol>
    <p class="subhead">Hermes (<code>~/.hermes/config.yaml</code>)</p>
    <pre class="code-block"><code>{hermesOAuth}</code></pre>
    <p class="muted small">Then run <code>hermes mcp login gradly</code> (or type <code>/reload-mcp</code> in a running session) and approve the request in your browser.</p>
    <button type="button" class="btn btn-ghost btn-sm" onclick={() => copy(hermesOAuth, 'Configuration')}><Icon name="copy" size={15} />Copy configuration</button>
  {:else}
    <ol class="steps steps-tight">
      <li><span class="step-num">1</span><div><strong>Create a key</strong> in the API keys tab with only the permissions the assistant needs.</div></li>
      <li><span class="step-num">2</span><div><strong>Configure your assistant</strong> to send it as <code>Authorization: Bearer &lt;key&gt;</code> to the endpoint.</div></li>
    </ol>
    <p class="subhead">Hermes (<code>~/.hermes/config.yaml</code> — replace the placeholder with your key, then <code>/reload-mcp</code>)</p>
    <pre class="code-block"><code>{hermesKey}</code></pre>
    <p class="subhead">Other clients that take a JSON configuration</p>
    <pre class="code-block"><code>{genericKey}</code></pre>
  {/if}
</section>

<div class="section-title"><div><h2>Connected applications</h2><p class="muted small">Apps you've authorized with OAuth. Disconnecting one ends its access at once.</p></div></div>

{#if loadError}
  <div class="alert alert-danger" role="alert">{loadError} <button type="button" class="link-btn" onclick={load}>Try again</button></div>
{:else if apps === null}
  <Loading />
{:else if apps.length === 0}
  <EmptyState icon="plug" title="No connected applications" text="When you authorize an assistant through “Sign in with Gradly”, it shows up here." />
{:else}
  <div class="table-wrap">
    <table class="table">
      <thead>
        <tr>
          <th scope="col">Application</th><th scope="col">Permissions</th><th scope="col">Status</th>
          <th scope="col">Authorized</th><th scope="col">Last used</th><th scope="col"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each apps as app (app.id)}
          <tr>
            <td data-label="Application">
              <div class="cell-title"><strong>{app.clientName || 'Unnamed application'}</strong><span class="muted small mono" title={app.clientId}>{app.redirectHost}</span></div>
            </td>
            <td data-label="Permissions"><span class="tag" title={app.scopes.join(', ')}>{scopeSummary(app.scopes, meta.scopes)}</span></td>
            <td data-label="Status">
              {#if app.status === 'connected'}<span class="badge badge-success">Connected</span>{:else}<span class="badge badge-danger-soft">Disconnected</span>{/if}
            </td>
            <td data-label="Authorized">{date(app.authorizedAt)}</td>
            <td data-label="Last used" title={app.lastUsedAt ?? ''}>{ago(app.lastUsedAt)}</td>
            <td class="row-actions">
              {#if app.status === 'connected'}
                <button type="button" class="btn btn-ghost btn-sm text-danger" disabled={busyId === app.id} onclick={() => revoke(app)}>Disconnect</button>
              {:else}
                <button type="button" class="btn btn-ghost btn-sm" disabled={busyId === app.id} onclick={() => remove(app)}><Icon name="trash" size={15} />Remove</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
