<script>
  import { groupScopes, GROUP_LABELS } from '../../lib/integrations.js';

  // catalog: the scope catalog from the API. allowed: optional subset that may be chosen (OAuth consent).
  let { catalog, selected = $bindable([]), allowed = null, idPrefix = 'scope', presets = true } = $props();

  const visible = $derived(allowed ? catalog.filter((s) => allowed.includes(s.scope)) : catalog);
  const groups = $derived(groupScopes(visible));
  const reads = $derived(visible.filter((s) => s.access === 'read').map((s) => s.scope));
  const all = $derived(visible.map((s) => s.scope));

  const isOn = (scope) => selected.includes(scope);

  function toggle(s, on) {
    let next = selected.filter((x) => x !== s.scope);
    if (on) {
      next.push(s.scope);
      // Changing data almost always means finding it first, so turning on "change" also turns on "view".
      if (s.access === 'write') {
        const read = visible.find((x) => x.group === s.group && x.access === 'read');
        if (read && !next.includes(read.scope)) next.push(read.scope);
      }
    }
    selected = all.filter((x) => next.includes(x));
  }

  const preset = (list) => (selected = [...list]);
  const same = (list) => list.length === selected.length && list.every((s) => selected.includes(s));
</script>

{#if presets}
  <div class="preset-row" role="group" aria-label="Quick choices">
    <button type="button" class="btn btn-ghost btn-sm {same(reads) ? 'is-active' : ''}" aria-pressed={same(reads)} onclick={() => preset(reads)}>Read-only</button>
    <button type="button" class="btn btn-ghost btn-sm {same(all) ? 'is-active' : ''}" aria-pressed={same(all)} onclick={() => preset(all)}>Full access</button>
    <button type="button" class="btn btn-ghost btn-sm" onclick={() => preset([])}>Clear</button>
  </div>
{/if}

<div class="scope-grid" role="group" aria-label="Permissions">
  <div class="scope-head" aria-hidden="true"><span></span><span>View</span><span>Change</span></div>
  {#each groups as g (g.group)}
    <div class="scope-row">
      <div class="scope-what">
        <strong>{GROUP_LABELS[g.group] ?? g.group}</strong>
        <span class="hint">{(g.read ?? g.write).description}</span>
      </div>
      <div class="scope-cell">
        {#if g.read}
          <label class="check" for="{idPrefix}-{g.read.scope}">
            <input id="{idPrefix}-{g.read.scope}" type="checkbox" checked={isOn(g.read.scope)} onchange={(e) => toggle(g.read, e.currentTarget.checked)} />
            <span class="scope-label">{g.read.label}</span>
          </label>
        {/if}
      </div>
      <div class="scope-cell">
        {#if g.write}
          <label class="check" for="{idPrefix}-{g.write.scope}">
            <input id="{idPrefix}-{g.write.scope}" type="checkbox" checked={isOn(g.write.scope)} onchange={(e) => toggle(g.write, e.currentTarget.checked)} />
            <span class="scope-label">{g.write.label}</span>
          </label>
        {:else}
          <span class="muted small" aria-hidden="true">—</span>
        {/if}
      </div>
    </div>
  {/each}
</div>
