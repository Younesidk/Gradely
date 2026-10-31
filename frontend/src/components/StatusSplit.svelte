<script>
  import { plural } from '../lib/format.js';

  // Module states of a semester as one split bar: passed, failed, incomplete, and modules without a usable formula.
  let { summary, compact = false } = $props();

  const other = $derived(Math.max(0, summary.moduleCount - summary.passedCount - summary.failedCount - summary.incompleteCount));
  const segments = $derived([
    ['passed', summary.passedCount],
    ['failed', summary.failedCount],
    ['incomplete', summary.incompleteCount],
    ['other', other],
  ]);
</script>

{#if summary.moduleCount > 0}
  <div class="split {compact ? 'split-compact' : ''}">
    <div class="split-bar" role="img"
      aria-label="{summary.passedCount} passed, {summary.failedCount} failed, {summary.incompleteCount} incomplete of {summary.moduleCount} modules">
      {#each segments as [kind, count]}
        {#if count > 0}<span class="split-seg seg-{kind}" style="flex: {count}"></span>{/if}
      {/each}
    </div>
    <ul class="split-legend">
      <li><span class="dot dot-passed"></span><span class="count">{summary.passedCount}</span> passed</li>
      <li><span class="dot dot-failed"></span><span class="count">{summary.failedCount}</span> failed</li>
      <li><span class="dot dot-incomplete"></span><span class="count">{summary.incompleteCount}</span> incomplete</li>
      {#if !compact}
        <li class="total">{summary.completedCount} of {plural(summary.moduleCount, 'module')} completed</li>
      {/if}
    </ul>
  </div>
{/if}
