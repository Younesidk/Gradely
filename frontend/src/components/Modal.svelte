<script>
  import { onMount } from 'svelte';

  let { title, onclose, children } = $props();
  let dialog;

  onMount(() => {
    // Focus the first field, like autofocus did in the old dialogs.
    dialog?.querySelector('input:not([type=checkbox]), select, textarea')?.focus();
    const onKey = (e) => e.key === 'Escape' && onclose?.();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="modal-backdrop" onclick={() => onclose?.()}>
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
  <div class="modal" role="dialog" aria-modal="true" aria-label={title} bind:this={dialog} onclick={(e) => e.stopPropagation()}>
    <div class="modal-head">
      <h2 class="modal-title">{title}</h2>
      <button type="button" class="icon-btn" aria-label="Close" onclick={() => onclose?.()}>×</button>
    </div>
    {@render children?.()}
  </div>
</div>
