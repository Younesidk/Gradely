<script>
  import { confirmState, closeConfirm } from '../lib/state.svelte.js';
</script>

{#if confirmState.request}
  {@const r = confirmState.request}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="modal-backdrop" onclick={() => closeConfirm(false)}>
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
    <div class="modal modal-sm" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title" onclick={(e) => e.stopPropagation()}>
      <h2 id="confirm-title" class="modal-title">{r.title}</h2>
      <p class="muted">{r.message}</p>
      <div class="modal-actions">
        <button type="button" class="btn btn-ghost" onclick={() => closeConfirm(false)}>Cancel</button>
        <button type="button" class="btn {r.danger ? 'btn-danger' : 'btn-primary'}" onclick={() => closeConfirm(true)}>{r.confirmText}</button>
      </div>
    </div>
  </div>
{/if}
