<script>
  import { inputValue, num, parseGrade } from '../lib/format.js';

  // One editable grade. Saves on change (blur / Enter). An empty field means "not entered yet", never zero.
  let { value, max = 20, readonly = false, label, onchange, oninvalid } = $props();

  function changed(e) {
    const parsed = parseGrade(e.currentTarget.value);
    if (!parsed.ok) {
      oninvalid?.("That doesn't look like a number.");
      return;
    }
    onchange?.(parsed.value);
  }
</script>

{#if readonly}
  <span>{value === null ? '—' : num(value)}</span>
{:else}
  <input class="grade-input" type="number" inputmode="decimal" step="any" min="0" max={inputValue(max)}
    value={inputValue(value)} placeholder="—" aria-label={label} onchange={changed} />
{/if}
