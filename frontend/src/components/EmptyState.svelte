<script>
  import Icon from './Icon.svelte';

  let { icon = null, title, text = null, children } = $props();
</script>

<div class="empty">
  {#if icon}
    <div class="empty-icon" aria-hidden="true"><Icon name={icon} size={20} /></div>
  {/if}
  <h3 class="empty-title">{title}</h3>
  {#if text}<p class="muted">{text}</p>{/if}
  {@render children?.()}
</div>
