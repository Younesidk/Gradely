<script>
  let { children } = $props();
</script>

<div class="auth-shell">
  <div class="auth-card">
    <div class="auth-brand">
      <svg class="brand-mark" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M2 13h16" /><path d="M12 7v10" /></svg>
      <span class="auth-brand-name">Gradly</span>
    </div>
    {@render children?.()}
  </div>
</div>
