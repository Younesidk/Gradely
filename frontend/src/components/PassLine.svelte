<script>
  import { fixed2, num, percent } from '../lib/format.js';

  // A grade on its scale, with a tick at the passing threshold.
  let { value, max, threshold, passed = null, wide = false } = $props();
</script>

<span class="pass-line {wide ? 'pass-line-wide' : ''} {passed === true ? 'is-pass' : passed === false ? 'is-fail' : ''}"
  role="img" aria-label="{fixed2(value)} of {num(max)}, pass mark {num(threshold)}">
  <span class="pass-line-fill" style="width: {percent(value, max)}"></span>
  <span class="pass-line-tick" style="left: {percent(threshold, max)}"></span>
</span>
