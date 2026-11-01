<script>
  import { average, num, plural } from '../lib/format.js';
  import StatusSplit from './StatusSplit.svelte';

  let { summary, label = 'Semester average' } = $props();
</script>

<section class="summary" aria-label={label}>
  <div class="summary-avg">
    <div class="summary-label">{label}</div>
    <div class="summary-value">
      <span class="summary-figure">{average(summary)}</span>
      {#if summary.average !== null}<span class="summary-scale">/ {num(summary.scale)}</span>{/if}
    </div>
    {#if summary.average === null}
      <div class="summary-note">{summary.moduleCount === 0 ? 'No modules yet' : 'Waiting for completed modules'}</div>
    {:else if summary.isProvisional}
      <div class="summary-note"><span class="tag tag-warn">Provisional</span> {plural(summary.incompleteCount, 'incomplete module')} not counted</div>
    {:else}
      <div class="summary-note">Weighted by coefficient</div>
    {/if}
  </div>
  <StatusSplit {summary} />
</section>
