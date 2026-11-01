<script>
  import { onMount, tick } from 'svelte';
  import { api } from '../lib/api.js';
  import { toast, confirmDialog } from '../lib/state.svelte.js';
  import { fixed2 } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';

  const OPERATORS = [['+', ' + '], ['−', ' - '], ['×', ' * '], ['÷', ' / '], ['(', '('], [')', ')'], [',', ', ']];

  let data = $state(null);
  let form = $state(null); // { name, expression, forLabExam, isDefault }
  let editingId = $state(null);
  let kindLocked = $state(false);
  let busy = $state(false);
  let preview = $state(null);
  let sample = $state({ cc1: 12, cc2: 14, lab: 15, exam: 13 });
  let textarea = $state();

  const formulas = $derived(data?.formulas ?? []);
  const variables = $derived(form?.forLabExam ? data?.variablesWithLab : data?.variablesNoLab);

  async function load() {
    data = await api.get('/formulas');
  }
  onMount(load);

  // ---------- live parse / preview (debounced; the server owns the formula engine) ----------
  let timer;
  let seq = 0;
  function schedulePreview() {
    clearTimeout(timer);
    timer = setTimeout(runPreview, 150);
  }
  async function runPreview() {
    if (!form) return;
    const mine = ++seq;
    try {
      const r = await api.post('/formulas/preview', {
        expression: form.expression, forLabExam: form.forLabExam,
        cc1: num(sample.cc1), cc2: num(sample.cc2), lab: num(sample.lab), exam: num(sample.exam),
      });
      if (mine === seq) preview = r;
    } catch {
      /* the editor keeps working; the next keystroke retries */
    }
  }
  const num = (v) => (v === '' || v === null || v === undefined || Number.isNaN(Number(v)) ? null : Number(v));

  function startNew(lab) {
    editingId = null;
    kindLocked = false;
    form = {
      name: '', forLabExam: lab,
      expression: lab ? (data.platformLab ?? '') : data.defaultNoLab,
      isDefault: !formulas.some((f) => f.forLabExam === lab),
    };
    preview = null;
    runPreview();
  }

  function startEdit(f) {
    editingId = f.id;
    kindLocked = f.modulesUsing > 0;
    form = { name: f.name, expression: f.expression, forLabExam: f.forLabExam, isDefault: f.isDefault };
    preview = null;
    runPreview();
  }

  function cancel() {
    form = null;
    editingId = null;
    preview = null;
  }

  function setKind(lab) {
    form.forLabExam = lab;
    runPreview();
  }

  async function insert(text) {
    const el = textarea;
    if (!el) return;
    const start = el.selectionStart ?? form.expression.length;
    const end = el.selectionEnd ?? form.expression.length;
    form.expression = form.expression.slice(0, start) + text + form.expression.slice(end);
    await tick();
    el.focus();
    el.setSelectionRange(start + text.length, start + text.length);
    runPreview();
  }

  async function save(e) {
    e.preventDefault();
    busy = true;
    try {
      if (editingId) await api.put(`/formulas/${editingId}`, form);
      else await api.post('/formulas', form);
      toast.success(editingId ? 'Formula saved' : 'Formula created');
      cancel();
      await load();
    } catch (err) {
      toast.error(err.message);
    } finally {
      busy = false;
    }
  }

  async function makeDefault(f) {
    try {
      await api.post(`/formulas/${f.id}/default`);
      toast.success(`“${f.name}” is now your default`);
    } catch (err) {
      toast.error(err.message);
    }
    await load();
  }

  async function remove(f) {
    if (f.modulesUsing > 0) {
      toast.error(`“${f.name}” is used by ${f.modulesUsing} module${f.modulesUsing === 1 ? '' : 's'}. Reassign them to another formula before deleting it.`);
      return;
    }
    if (!(await confirmDialog(`Delete “${f.name}”?`, 'This formula will be permanently deleted.', 'Delete formula'))) return;
    try {
      await api.del(`/formulas/${f.id}`);
      toast.success('Formula deleted');
    } catch (err) {
      toast.error(err.message);
    }
    await load();
  }

  async function addPlatformLab() {
    try {
      await api.post('/formulas/platform-lab');
      toast.success('Lab formula added');
    } catch (err) {
      toast.error(err.message);
    }
    await load();
  }

  const groups = [
    { title: 'Modules without a lab exam', lab: false },
    { title: 'Modules with a lab exam', lab: true },
  ];
  const parseOk = $derived(preview?.success === true);
  const firstError = $derived(preview?.errors?.[0]);
</script>

<svelte:head><title>Formula settings · Gradly</title></svelte:head>

<div class="page-head">
  <div>
    <h1>Formula settings</h1>
    <p class="muted">How a module grade is computed from CC1, CC2, lab and exam</p>
  </div>
  <div class="page-actions">
    {#if !form}
      <button type="button" class="btn btn-primary" onclick={() => startNew(false)}><Icon name="plus" size={16} />New formula</button>
    {/if}
  </div>
</div>

{#if data === null}
  <Loading />
{:else}
  {#if data.platformLab && !formulas.some((f) => f.forLabExam) && !form}
    <div class="callout callout-info">
      <strong>A lab formula is available.</strong> Your administrator has configured a default for modules with a lab exam.
      <button type="button" class="btn btn-ghost btn-sm" onclick={addPlatformLab}>Add it to my formulas</button>
    </div>
  {/if}

  {#if form}
    <section class="card editor" aria-label="Formula editor">
      <h2>{editingId ? 'Edit formula' : 'New formula'}</h2>
      <form class="form" onsubmit={save}>
        <div class="field-row field-row-wide">
          <div class="field">
            <label for="f-name">Name</label>
            <input id="f-name" class="input" bind:value={form.name} maxlength="80" placeholder="e.g. Heavy exam weighting" required />
          </div>
          <fieldset class="field fieldset">
            <legend>Applies to</legend>
            <label class="radio"><input type="radio" name="kind" checked={!form.forLabExam} onchange={() => setKind(false)} disabled={kindLocked} /> Modules without lab exam</label>
            <label class="radio"><input type="radio" name="kind" checked={form.forLabExam} onchange={() => setKind(true)} disabled={kindLocked} /> Modules with lab exam</label>
            {#if kindLocked}<span class="hint">Used by modules, so the type can't change.</span>{/if}
          </fieldset>
        </div>

        <div class="field">
          <label for="expr">Expression</label>
          <textarea id="expr" class="input mono" rows="3" spellcheck="false" autocomplete="off" maxlength={data.maxLength}
            bind:this={textarea} bind:value={form.expression} oninput={schedulePreview} aria-describedby="expr-status"></textarea>

          <div class="palette" role="group" aria-label="Formula palette">
            <div class="palette-group" role="group" aria-label="Variables">
              <span class="palette-label">Variables</span>
              {#each variables as v}<button type="button" class="chip-btn" onclick={() => insert(v)}>{v}</button>{/each}
            </div>
            <div class="palette-group" role="group" aria-label="Operators">
              <span class="palette-label">Operators</span>
              {#each OPERATORS as [shown, text]}
                <button type="button" class="chip-btn chip-btn-op" aria-label="Insert {text.trim() || text}" onclick={() => insert(text)}>{shown}</button>
              {/each}
            </div>
            <div class="palette-group" role="group" aria-label="Functions">
              <span class="palette-label">Functions</span>
              {#each data.functions as fn}
                <button type="button" class="chip-btn" title={fn.description} onclick={() => insert(fn.name + '(')}>{fn.name}()</button>
              {/each}
            </div>
          </div>

          <div id="expr-status" aria-live="polite">
            {#if parseOk}
              <div class="validation ok">✓ Valid formula</div>
            {:else if firstError}
              <div class="validation bad" role="alert">
                ✕ {firstError.message}
                {#if firstError.position >= 0 && form.expression.length > 0}
                  <pre class="caret-pre">{form.expression}{'\n'}{' '.repeat(Math.min(firstError.position, form.expression.length))}^</pre>
                {/if}
              </div>
            {/if}
          </div>
        </div>

        <div class="preview">
          <h3>Live example</h3>
          <p class="muted small">Try some grades (out of 20, pass mark 10) to see what this formula gives.</p>
          <div class="field-row field-row-4">
            <div class="field"><label for="s-cc1">CC1</label><input id="s-cc1" class="input input-sm" type="number" step="any" bind:value={sample.cc1} oninput={schedulePreview} /></div>
            <div class="field"><label for="s-cc2">CC2</label><input id="s-cc2" class="input input-sm" type="number" step="any" bind:value={sample.cc2} oninput={schedulePreview} /></div>
            {#if form.forLabExam}
              <div class="field"><label for="s-lab">Lab</label><input id="s-lab" class="input input-sm" type="number" step="any" bind:value={sample.lab} oninput={schedulePreview} /></div>
            {/if}
            <div class="field"><label for="s-exam">Exam</label><input id="s-exam" class="input input-sm" type="number" step="any" bind:value={sample.exam} oninput={schedulePreview} /></div>
          </div>
          {#if parseOk}
            <div class="preview-result">
              {#if preview.full.grade !== null}
                <div class="preview-grade">
                  <span class="preview-label">Module grade</span>
                  <strong class={preview.full.passed ? 'final-pass' : 'final-fail'}>{fixed2(preview.full.grade)}</strong>
                  <span class="muted">/ 20</span>
                  <StatusBadge result={preview.full} />
                </div>
              {:else}
                <div class="preview-grade"><span class="preview-label">Module grade</span> <span class="muted">{preview.full.formulaError ?? preview.full.statusText}</span></div>
              {/if}
              {#if preview.required.requiredExam.kind !== 'notApplicable'}
                <div class="preview-required muted small"><span class="preview-label">Before the exam</span> {preview.required.requiredExam.message}</div>
              {/if}
            </div>
          {/if}
        </div>

        <label class="check">
          <input type="checkbox" bind:checked={form.isDefault} />
          <span>Use as my default for new modules {form.forLabExam ? 'with' : 'without'} a lab exam</span>
        </label>

        <div class="modal-actions inline-actions">
          <button type="button" class="btn btn-ghost" onclick={cancel}>Cancel</button>
          <button type="submit" class="btn btn-primary" disabled={!parseOk || busy}>{editingId ? 'Save changes' : 'Create formula'}</button>
        </div>
      </form>
    </section>
  {/if}

  {#if formulas.length === 0}
    <EmptyState icon="formula" title="No formulas yet" text="Create a formula to compute module grades.">
      <button type="button" class="btn btn-primary" onclick={() => startNew(false)}><Icon name="plus" size={16} />New formula</button>
    </EmptyState>
  {/if}

  {#each groups as group (group.lab)}
    {@const items = formulas.filter((f) => f.forLabExam === group.lab)}
    <div class="section-title"><h2>{group.title}</h2></div>
    {#if items.length === 0}
      <p class="muted small group-empty">
        No formula yet.
        {#if group.lab}<span>Modules with a lab exam stay incomplete until you add one.</span>{/if}
        <button type="button" class="link-btn" onclick={() => startNew(group.lab)}>Create one</button>
      </p>
    {:else}
      <div class="formula-list">
        {#each items as f (f.id)}
          <article class="card formula-card">
            <div class="formula-head">
              <h3>{f.name}</h3>
              {#if f.isDefault}<span class="tag tag-primary">Default</span>{/if}
            </div>
            <code class="formula-expr">{f.expression}</code>
            <div class="muted small">Used by {f.modulesUsing} module{f.modulesUsing === 1 ? '' : 's'}</div>
            <div class="card-actions">
              <button type="button" class="btn btn-ghost btn-sm" onclick={() => startEdit(f)}><Icon name="edit" size={15} />Edit</button>
              {#if !f.isDefault}
                <button type="button" class="btn btn-ghost btn-sm" onclick={() => makeDefault(f)}>Make default</button>
              {/if}
              <button type="button" class="btn btn-ghost btn-sm text-danger" onclick={() => remove(f)}><Icon name="trash" size={15} />Delete</button>
            </div>
          </article>
        {/each}
      </div>
    {/if}
  {/each}

  <section class="card help">
    <h2>Formula syntax</h2>
    <ul class="plain-list">
      <li>Variables: <code>CC1</code>, <code>CC2</code>, <code>Lab</code> (lab formulas only), <code>Exam</code>.</li>
      <li>Operators: <code>+ - * /</code> and parentheses. Decimals use a dot: <code>0.4</code>.</li>
      <li>Functions: <code>AVG()</code>, <code>MIN()</code>, <code>MAX()</code>, <code>ABS()</code>, <code>ROUND(x, digits)</code>.</li>
      <li>Gradly only calculates “the exam grade you need” when the formula is a simple, increasing function of <code>Exam</code> (sums, weights, averages).
        Formulas like <code>MAX(Exam, …)</code> still compute a grade but won't get a required-exam hint.</li>
    </ul>
  </section>
{/if}
