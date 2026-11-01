<script>
  // Module state: green = passed, red = failed, amber = incomplete, neutral = waiting.
  let { result = null } = $props();

  const tone = $derived.by(() => {
    if (!result) return 'neutral';
    if (result.status === 'complete') return result.passed ? 'success' : 'danger';
    if (result.status === 'incomplete') return 'warn';
    if (result.status === 'formulaError' || result.status === 'noFormula') return 'danger-soft';
    return 'neutral';
  });
  const text = $derived(!result ? '—' : result.status === 'incomplete' ? 'Incomplete' : result.statusText);
  const title = $derived(result?.status === 'incomplete' ? result.statusText : (result?.formulaError ?? undefined));
</script>

<span class="badge badge-{tone}" {title}>{text}</span>
