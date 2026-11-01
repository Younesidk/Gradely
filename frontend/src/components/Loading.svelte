<script>
  let { text = 'Loading…' } = $props();
</script>

<div class="loading" role="status" aria-live="polite">
  <span class="spinner" aria-hidden="true"></span>
  <span>{text}</span>
</div>
