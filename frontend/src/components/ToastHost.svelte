<script>
  import { toasts, dismissToast } from '../lib/state.svelte.js';
</script>

<div class="toast-region" aria-live="polite" aria-atomic="false">
  {#each toasts.items as t (t.id)}
    <div class="toast toast-{t.kind}" role={t.kind === 'error' ? 'alert' : 'status'}>
      <span class="toast-text">{t.text}</span>
      <button type="button" class="toast-close" aria-label="Dismiss" onclick={() => dismissToast(t.id)}>×</button>
    </div>
  {/each}
</div>
