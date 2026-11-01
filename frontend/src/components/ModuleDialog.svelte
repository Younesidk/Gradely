<script>
  import { api } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from './Modal.svelte';

  // `existing` is the module being edited (a module from a semester view), or null to add a new one.
  let { semesterId, existing = null, formulas, onclose, onsaved } = $props();

  let name = $state(existing?.name ?? '');
  let code = $state(existing?.code ?? '');
  let coefficient = $state(existing?.coefficient ?? 1);
  let maxGrade = $state(existing?.maxGrade ?? 20);
  let passingThreshold = $state(existing?.passingThreshold ?? 10);
  let hasLabExam = $state(existing?.hasLabExam ?? false);
  let formulaId = $state(existing?.formulaId ?? '');
  let error = $state(null);
  let busy = $state(false);

  const matching = $derived(formulas.filter((f) => f.forLabExam === hasLabExam));

  function labToggled() {
    formulaId = ''; // formulas are specific to lab / no-lab modules; fall back to the default
  }

  async function save(e) {
    e.preventDefault();
    busy = true;
    error = null;
    const body = {
      name, code: code || null, coefficient: Number(coefficient), maxGrade: Number(maxGrade),
      passingThreshold: Number(passingThreshold), hasLabExam, formulaId: formulaId || null,
    };
    try {
      if (existing) await api.put(`/modules/${existing.id}`, body);
      else await api.post(`/semesters/${semesterId}/modules`, body);
      toast.success(existing ? 'Module updated' : 'Module added');
      onsaved?.();
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }
</script>

<Modal title={existing ? 'Edit module' : 'Add module'} {onclose}>
  <form class="form" onsubmit={save}>
    {#if error}<div class="alert alert-danger" role="alert">{error}</div>{/if}
    <div class="field-row field-row-wide">
      <div class="field">
        <label for="mod-name">Module name</label>
        <input id="mod-name" class="input" bind:value={name} maxlength="100" placeholder="Information systems design" required />
      </div>
      <div class="field">
        <label for="mod-code">Code <span class="opt">(optional)</span></label>
        <input id="mod-code" class="input" bind:value={code} maxlength="30" placeholder="SID" />
      </div>
    </div>
    <div class="field-row field-row-3">
      <div class="field">
        <label for="mod-coef">Coefficient</label>
        <input id="mod-coef" class="input" type="number" step="any" min="0" bind:value={coefficient} />
      </div>
      <div class="field">
        <label for="mod-max">Maximum grade</label>
        <input id="mod-max" class="input" type="number" step="any" min="0" bind:value={maxGrade} />
      </div>
      <div class="field">
        <label for="mod-pass">Passing threshold</label>
        <input id="mod-pass" class="input" type="number" step="any" min="0" bind:value={passingThreshold} />
      </div>
    </div>

    <label class="check">
      <input type="checkbox" bind:checked={hasLabExam} onchange={labToggled} />
      <span>This module has a lab exam</span>
    </label>

    <div class="field">
      <label for="mod-formula">Grading formula</label>
      <select id="mod-formula" class="input" bind:value={formulaId}>
        <option value="">Automatic (my default for this kind of module)</option>
        {#each matching as f (f.id)}
          <option value={f.id}>{f.name}{f.isDefault ? ' (default)' : ''}</option>
        {/each}
      </select>
      {#if matching.length === 0}
        <span class="hint hint-warn">
          You don't have a formula for modules {hasLabExam ? 'with' : 'without'} a lab exam yet.
          <a href="/formulas">Create one in Formula settings</a> – grades stay incomplete until you do.
        </span>
      {/if}
    </div>

    <div class="modal-actions">
      <button type="button" class="btn btn-ghost" onclick={() => onclose?.()}>Cancel</button>
      <button type="submit" class="btn btn-primary" disabled={busy}>{existing ? 'Save changes' : 'Add module'}</button>
    </div>
  </form>
</Modal>
