<script>
  import { api } from '../../lib/api.js';
  import { toast, confirmDialog } from '../../lib/state.svelte.js';
  import { EXPIRY_CHOICES, isoDateIn, hasWrite } from '../../lib/integrations.js';
  import Modal from '../Modal.svelte';
  import ScopePicker from './ScopePicker.svelte';
  import SecretReveal from './SecretReveal.svelte';

  // existing: the key being edited, or null to create one. onsaved runs after every successful save.
  let { catalog, existing = null, onclose, onsaved } = $props();

  let name = $state(existing?.name ?? '');
  let description = $state(existing?.description ?? '');
  let scopes = $state(existing ? [...existing.scopes] : catalog.filter((s) => s.access === 'read').map((s) => s.scope));
  let expiry = $state('');
  let customDate = $state(isoDateIn(30));
  let busy = $state(false);
  let error = $state(null);
  let secret = $state(null);

  const minDate = isoDateIn(1);

  async function submit(e) {
    e.preventDefault();
    error = null;
    if (!name.trim()) {
      error = 'Give the key a name, such as the app or script that will use it.';
      return;
    }
    if (scopes.length === 0) {
      error = 'Choose at least one permission.';
      return;
    }
    busy = true;
    try {
      const body = { name: name.trim(), description: description.trim() || null, scopes };
      if (existing) {
        await api.put(`/integrations/keys/${existing.id}`, body);
        toast.success('API key updated');
        onsaved?.();
        onclose();
      } else {
        if (expiry === 'custom') body.expiresAt = customDate;
        else if (expiry) body.expiresAt = isoDateIn(expiry);
        const r = await api.post('/integrations/keys', body);
        secret = r.secret;
        onsaved?.();
      }
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }

  // Closing while the secret is on screen asks first: it can't be shown again.
  async function close() {
    if (secret && !(await confirmDialog('Close without copying the key?', 'It will never be shown again. You would have to rotate it to get a new one.', 'Close anyway'))) return;
    onclose();
  }
</script>

<Modal title={secret ? 'API key created' : existing ? 'Edit API key' : 'Create an API key'} onclose={close}>
  {#if secret}
    <SecretReveal {secret} ondone={onclose} />
  {:else}
    <form class="form" onsubmit={submit} novalidate>
      {#if error}<div class="alert alert-danger" role="alert">{error}</div>{/if}
      <div class="field">
        <label for="key-name">Name</label>
        <input id="key-name" class="input" bind:value={name} maxlength="80" placeholder="e.g. Hermes, Personal scripts" autocomplete="off" required />
      </div>
      <div class="field">
        <label for="key-desc">Description <span class="opt">(optional)</span></label>
        <input id="key-desc" class="input" bind:value={description} maxlength="300" placeholder="What is this key for?" autocomplete="off" />
      </div>
      {#if !existing}
        <div class="field-row">
          <div class="field">
            <label for="key-expiry">Expires</label>
            <select id="key-expiry" class="input" bind:value={expiry}>
              {#each EXPIRY_CHOICES as c}<option value={c.value}>{c.label}</option>{/each}
            </select>
          </div>
          {#if expiry === 'custom'}
            <div class="field">
              <label for="key-date">Expiry date</label>
              <input id="key-date" class="input" type="date" min={minDate} bind:value={customDate} />
            </div>
          {/if}
        </div>
      {/if}
      <fieldset class="field">
        <legend class="fieldset-legend">Permissions</legend>
        <p class="hint">“View” lets the integration read your data. “Change” lets it create, edit and delete it. Give only what it needs.</p>
        <ScopePicker {catalog} bind:selected={scopes} idPrefix="key-scope" />
      </fieldset>
      {#if hasWrite(scopes)}
        <p class="hint hint-warn">This key can change your academic data. Anyone who has it can act as you within these permissions.</p>
      {/if}
      <div class="modal-actions">
        <button type="button" class="btn btn-ghost" onclick={onclose}>Cancel</button>
        <button type="submit" class="btn btn-primary" disabled={busy}>{existing ? 'Save changes' : 'Create key'}</button>
      </div>
    </form>
  {/if}
</Modal>
