<script>
  import { average, date, num, plural } from '../lib/format.js';
  import StatusSplit from './StatusSplit.svelte';

  let { view, href, actions } = $props();

  const subtitle = $derived.by(() => {
    const s = view.semester;
    const parts = [plural(view.summary.moduleCount, 'module')];
    if (s.academicYear?.trim()) parts.push(s.academicYear);
    if (s.startDate && s.endDate) parts.push(`${date(s.startDate)} – ${date(s.endDate)}`);
    else if (s.startDate) parts.push(`from ${date(s.startDate)}`);
    else if (s.endDate) parts.push(`until ${date(s.endDate)}`);
    if (view.summary.isProvisional && view.summary.average !== null) parts.push('provisional');
    return parts.join(' · ');
  });
</script>

<article class="card sem-card">
  <div class="sem-card-head">
    <div class="sem-card-id">
      <h3 class="sem-card-title"><a {href}>{view.semester.name}</a></h3>
      <div class="muted small">{subtitle}</div>
    </div>
    <div class="avg" title="Semester average">
      <span class="avg-value">{average(view.summary)}</span>
      {#if view.summary.average !== null}<span class="avg-scale">/ {num(view.summary.scale)}</span>{/if}
    </div>
  </div>
  {#if view.summary.moduleCount === 0}
    <div class="split muted small">No modules yet</div>
  {:else}
    <StatusSplit summary={view.summary} compact />
  {/if}
  {@render actions?.()}
</article>
