<script>
  import { num, fixed2 } from '../lib/format.js';
  import Icon from './Icon.svelte';
  import PassLine from './PassLine.svelte';
  import StatusBadge from './StatusBadge.svelte';
  import GradeCell from './GradeCell.svelte';

  // Modules of one semester. Grades are edited inline (saved when the field loses focus / Enter);
  // in readonly mode (administrator inspection) every control is replaced by plain text.
  let {
    modules, formulas = [], readonly = false, revision = 0,
    ongrades, onformula, onedit, ondelete, onmove, oninvalid,
  } = $props();

  let expanded = $state({});

  const toggle = (id) => (expanded[id] = !expanded[id]);
  const isFirst = (v) => modules.length > 0 && modules[0].module.id === v.module.id;
  const isLast = (v) => modules.length > 0 && modules[modules.length - 1].module.id === v.module.id;
  const tone = (kind) => ({ needed: 'info', alreadySecured: 'success', impossible: 'danger' })[kind] ?? 'warn';

  function edit(v, field, value) {
    const m = v.module;
    ongrades?.(m.id, { cc1: m.cc1, cc2: m.cc2, lab: m.lab, exam: m.exam, [field]: value });
  }
</script>

<div class="table-wrap">
  <table class="table modules-table">
    <thead>
      <tr>
        <th scope="col">Module</th>
        <th scope="col" class="num">Coef.</th>
        <th scope="col" class="grade-col">CC1</th>
        <th scope="col" class="grade-col">CC2</th>
        <th scope="col" class="grade-col">Lab</th>
        <th scope="col" class="grade-col">Exam</th>
        <th scope="col" class="num">Final grade</th>
        <th scope="col">Status</th>
      </tr>
    </thead>
    <tbody>
      {#each modules as v (v.module.id + ':' + revision)}
        {@const m = v.module}
        {@const open = !!expanded[m.id]}
        <tr class="mod-row {open ? 'is-open' : ''}">
          <td data-label="Module" class="mod-name">
            <button type="button" class="expander" aria-expanded={open}
              aria-label="{open ? 'Hide' : 'Show'} details for {m.name}" onclick={() => toggle(m.id)}>
              <Icon name="chevron" size={16} />
            </button>
            <div class="mod-title">
              <strong>{m.name}</strong>
              {#if m.code?.trim()}<span class="muted small">{m.code}</span>{/if}
            </div>
          </td>
          <td data-label="Coef." class="num">{num(m.coefficient)}</td>
          <td data-label="CC1" class="grade-col">
            <GradeCell value={m.cc1} max={m.maxGrade} {readonly} label="CC1 grade for {m.name}" {oninvalid} onchange={(g) => edit(v, 'cc1', g)} />
          </td>
          <td data-label="CC2" class="grade-col">
            <GradeCell value={m.cc2} max={m.maxGrade} {readonly} label="CC2 grade for {m.name}" {oninvalid} onchange={(g) => edit(v, 'cc2', g)} />
          </td>
          <td data-label="Lab" class="grade-col">
            {#if m.hasLabExam}
              <GradeCell value={m.lab} max={m.maxGrade} {readonly} label="Lab grade for {m.name}" {oninvalid} onchange={(g) => edit(v, 'lab', g)} />
            {:else}
              <span class="muted" title="This module has no lab exam">—</span>
            {/if}
          </td>
          <td data-label="Exam" class="grade-col">
            <GradeCell value={m.exam} max={m.maxGrade} {readonly} label="Exam grade for {m.name}" {oninvalid} onchange={(g) => edit(v, 'exam', g)} />
          </td>
          <td data-label="Final grade" class="num">
            {#if v.result.grade !== null}
              <span class="final-wrap">
                <PassLine value={v.result.grade} max={m.maxGrade} threshold={m.passingThreshold} passed={v.result.passed} />
                <span>
                  <strong class="final {v.result.passed ? 'final-pass' : 'final-fail'}">{fixed2(v.result.grade)}</strong>
                  <span class="muted small">/ {num(m.maxGrade)}</span>
                </span>
              </span>
            {:else}
              <span class="muted">—</span>
            {/if}
          </td>
          <td data-label="Status"><StatusBadge result={v.result} /></td>
        </tr>
        {#if open}
          <tr class="detail-row">
            <td colspan="8">
              <div class="detail">
                <dl class="facts">
                  <div>
                    <dt>Formula used</dt>
                    <dd>
                      {#if !readonly}
                        <select class="input input-sm" aria-label="Formula for {m.name}"
                          onchange={(e) => onformula?.(m.id, e.currentTarget.value || null)}>
                          {#if !v.formula}<option value="" selected>— none selected —</option>{/if}
                          {#each formulas.filter((f) => f.forLabExam === m.hasLabExam) as f (f.id)}
                            <option value={f.id} selected={f.id === m.formulaId}>{f.name}</option>
                          {/each}
                        </select>
                      {:else}
                        {v.formula?.name ?? '— none —'}
                      {/if}
                    </dd>
                  </div>
                  <div><dt>Expression</dt><dd>{#if v.formula}<code>{v.formula.expression}</code>{:else}<span class="muted">—</span>{/if}</dd></div>
                  <div><dt>Calculated grade</dt><dd>{v.result.grade !== null ? `${fixed2(v.result.grade)} / ${num(m.maxGrade)}` : '—'}</dd></div>
                  <div><dt>Passing threshold</dt><dd>{num(m.passingThreshold)} / {num(m.maxGrade)}</dd></div>
                  <div><dt>Coefficient</dt><dd>{num(m.coefficient)}</dd></div>
                  <div><dt>Calculation status</dt><dd>{v.result.statusText}</dd></div>
                </dl>

                {#if v.result.requiredExam.kind !== 'notApplicable'}
                  <div class="callout callout-{tone(v.result.requiredExam.kind)}" role="note">
                    <strong>Required exam grade.</strong> {v.result.requiredExam.message}
                  </div>
                {/if}
                {#if v.result.status === 'formulaError' || v.result.status === 'noFormula'}
                  <div class="callout callout-danger" role="alert">
                    {#if v.result.status === 'noFormula'}
                      {m.hasLabExam
                        ? 'No lab formula is selected for this module. Create or pick one in Formula settings.'
                        : 'No formula is selected for this module.'}
                    {:else}
                      {v.result.formulaError}
                    {/if}
                  </div>
                {/if}
                {#if v.result.status === 'incomplete' && v.result.missingInputs.length > 0}
                  <div class="callout callout-warn">Missing: {v.result.missingInputs.join(', ')}. No final grade is shown until these are entered.</div>
                {/if}

                {#if !readonly}
                  <div class="detail-actions">
                    <button type="button" class="btn btn-ghost btn-sm" onclick={() => onedit?.(v)}><Icon name="edit" size={15} />Edit module</button>
                    <button type="button" class="btn btn-ghost btn-sm" disabled={isFirst(v)} onclick={() => onmove?.(m.id, -1)}><Icon name="up" size={15} />Move up</button>
                    <button type="button" class="btn btn-ghost btn-sm" disabled={isLast(v)} onclick={() => onmove?.(m.id, 1)}><Icon name="down" size={15} />Move down</button>
                    <button type="button" class="btn btn-ghost btn-sm text-danger" onclick={() => ondelete?.(v)}><Icon name="trash" size={15} />Delete</button>
                  </div>
                {/if}
              </div>
            </td>
          </tr>
        {/if}
      {/each}
    </tbody>
  </table>
</div>
