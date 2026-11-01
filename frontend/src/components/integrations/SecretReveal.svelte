<script>
  import { toast, copyText } from '../../lib/state.svelte.js';
  import Icon from '../Icon.svelte';

  let { secret, title = 'Copy your new API key now', ondone } = $props();
  let copied = $state(false);

  async function copy() {
    if (await copyText(secret)) {
      copied = true;
      toast.success('API key copied to clipboard');
    } else {
      toast.info("Couldn't access the clipboard — select the key and copy it manually");
    }
  }
</script>

<div class="callout callout-success secret-reveal" role="status">
  <strong>{title}</strong>
  <p class="small">This is the only time the full key is shown. Gradly stores only a fingerprint of it, so it can't show it again. Keep it somewhere safe, like a password manager.</p>
  <div class="link-box">
    <input class="input mono" readonly value={secret} aria-label="New API key" onfocus={(e) => e.currentTarget.select()} data-testid="secret" />
    <button type="button" class="btn btn-primary btn-sm" onclick={copy}><Icon name="copy" size={15} />{copied ? 'Copied' : 'Copy'}</button>
  </div>
  {#if ondone}
    <div class="modal-actions">
      <button type="button" class="btn btn-ghost btn-sm" onclick={ondone}>I've stored it safely</button>
    </div>
  {/if}
</div>
