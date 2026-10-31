<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, confirmDialog } from '../../lib/state.svelte.js';
  import { date } from '../../lib/format.js';
  import { ago, scopeSummary } from '../../lib/integrations.js';
  import Icon from '../Icon.svelte';
  import Loading from '../Loading.svelte';
  import EmptyState from '../EmptyState.svelte';
  import KeyDialog from './KeyDialog.svelte';
  import SecretReveal from './SecretReveal.svelte';

  let { meta, onchange } = $props();

  let keys = $state(null);
  let loadError = $state(null);
  let dialog = $state(null); // { existing } while the create / edit dialog is open
  let rotated = $state(null); // { name, secret } right after a rotation
  let busyId = $state(null);

  async function load() {
    try {
      keys = (await api.get('/integrations/keys')).items;
      loadError = null;
    } catch (err) {
      loadError = err.message;
    }
  }
  onMount(load);

  async function act(k, action, success) {
    busyId = k.id;
    try {
      const r = await action();
      toast.success(success);
      await load();
      onchange?.();
      return r;
    } catch (err) {
      toast.error(err.message);
    } finally {
      busyId = null;
    }
  }

  async function rotate(k) {
    const yes = await confirmDialog(`Rotate “${k.name}”?`,
      'A new secret is issued and the current one stops working immediately. Anything using the old key must be updated.', 'Rotate key');
    if (!yes) return;
    const r = await act(k, () => api.post(`/integrations/keys/${k.id}/rotate`), 'Key rotated — copy the new secret');
    if (r) rotated = { name: k.name, secret: r.secret };
  }

  async function revoke(k) {
    const yes = await confirmDialog(`Revoke “${k.name}”?`, 'It stops working immediately and can’t be re-enabled.', 'Revoke key');
    if (yes) await act(k, () => api.post(`/integrations/keys/${k.id}/revoke`), 'Key revoked');
  }

  async function remove(k) {
    const yes = await confirmDialog(`Delete “${k.name}”?`, 'The key is removed from this list. Its past activity stays in the activity log.', 'Delete');
    if (yes) await act(k, () => api.del(`/integrations/keys/${k.id}`), 'Key deleted');
  }

  const tone = (s) => ({ active: 'success', expired: 'neutral', revoked: 'danger-soft' })[s];
  const statusText = (s) => ({ active: 'Active', expired: 'Expired', revoked: 'Revoked' })[s];
</script>

<div class="section-title section-title-first">
  <div>
    <h2>API keys</h2>
    <p class="muted small">Personal keys for scripts and AI assistants that can't sign in with OAuth. Send one as <code>Authorization: Bearer &lt;key&gt;</code> to the MCP endpoint.</p>
  </div>
  <button type="button" class="btn btn-primary" onclick={() => (dialog = { existing: null })}><Icon name="plus" size={16} />Create key</button>
</div>

{#if rotated}
  <SecretReveal secret={rotated.secret} title="New secret for “{rotated.name}”" ondone={() => (rotated = null)} />
{/if}

{#if loadError}
  <div class="alert alert-danger" role="alert">{loadError} <button type="button" class="link-btn" onclick={load}>Try again</button></div>
{:else if keys === null}
  <Loading />
{:else if keys.length === 0}
  <EmptyState icon="key" title="No API keys yet" text="Create a key to let a script or an assistant such as Hermes use your Gradly data.">
    <button type="button" class="btn btn-primary" onclick={() => (dialog = { existing: null })}><Icon name="plus" size={16} />Create your first key</button>
  </EmptyState>
{:else}
  <div class="table-wrap">
    <table class="table">
      <thead>
        <tr>
          <th scope="col">Name</th><th scope="col">Key</th><th scope="col">Permissions</th><th scope="col">Status</th>
          <th scope="col">Last used</th><th scope="col">Created</th><th scope="col"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each keys as k (k.id)}
          <tr>
            <td data-label="Name">
              <div class="cell-title"><strong>{k.name}</strong>{#if k.description}<span class="muted small">{k.description}</span>{/if}</div>
            </td>
            <td data-label="Key"><code>{k.prefix}</code></td>
            <td data-label="Permissions"><span class="tag" title={k.scopes.join(', ')}>{scopeSummary(k.scopes, meta.scopes)}</span></td>
            <td data-label="Status">
              <span class="badge badge-{tone(k.status)}">{statusText(k.status)}</span>
              {#if k.status === 'active' && k.expiresAt}<span class="muted small"> · until {date(k.expiresAt)}</span>{/if}
            </td>
            <td data-label="Last used" title={k.lastUsedAt ?? ''}>{ago(k.lastUsedAt)}</td>
            <td data-label="Created">{date(k.createdAt)}</td>
            <td class="row-actions">
              {#if k.status === 'active'}
                <button type="button" class="btn btn-ghost btn-sm" disabled={busyId === k.id} onclick={() => (dialog = { existing: k })}><Icon name="edit" size={15} />Edit</button>
                <button type="button" class="btn btn-ghost btn-sm" disabled={busyId === k.id} onclick={() => rotate(k)}>Rotate</button>
              {/if}
              {#if k.status !== 'revoked'}
                <button type="button" class="btn btn-ghost btn-sm text-danger" disabled={busyId === k.id} onclick={() => revoke(k)}>Revoke</button>
              {/if}
              {#if k.status !== 'active'}
                <button type="button" class="btn btn-ghost btn-sm text-danger" disabled={busyId === k.id} onclick={() => remove(k)} aria-label="Delete {k.name}"><Icon name="trash" size={15} />Delete</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

{#if dialog}
  <KeyDialog catalog={meta.scopes} existing={dialog.existing} onclose={() => (dialog = null)} onsaved={() => { load(); onchange?.(); }} />
{/if}
