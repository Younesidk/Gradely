<script>
  import { api } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from './Modal.svelte';

  // When `existing` is set the dialog edits that semester; otherwise it creates a new one.
  let { existing = null, onclose, onsaved } = $props();

  let name = $state(existing?.name ?? '');
  let academicYear = $state(existing?.academicYear ?? '');
  let startDate = $state(existing?.startDate ?? '');
  let endDate = $state(existing?.endDate ?? '');
  let error = $state(null);
  let busy = $state(false);

  async function save(e) {
    e.preventDefault();
    busy = true;
    error = null;
    const body = { name, academicYear: academicYear || null, startDate: startDate || null, endDate: endDate || null };
    try {
      const saved = existing ? await api.put(`/semesters/${existing.id}`, body) : await api.post('/semesters', body);
      toast.success(existing ? 'Semester updated' : 'Semester created');
      onsaved?.(saved.id);
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }
</script>

<Modal title={existing ? 'Edit semester' : 'New semester'} {onclose}>
  <form class="form" onsubmit={save}>
    {#if error}<div class="alert alert-danger" role="alert">{error}</div>{/if}
    <div class="field">
      <label for="sem-name">Name</label>
      <input id="sem-name" class="input" bind:value={name} placeholder="S5, Semester 1, Spring 2027…" maxlength="100" required />
    </div>
    <div class="field">
      <label for="sem-year">Academic year <span class="opt">(optional)</span></label>
      <input id="sem-year" class="input" bind:value={academicYear} placeholder="2026–2027" maxlength="50" />
    </div>
    <div class="field-row">
      <div class="field">
        <label for="sem-start">Start date <span class="opt">(optional)</span></label>
        <input id="sem-start" class="input" type="date" bind:value={startDate} />
      </div>
      <div class="field">
        <label for="sem-end">End date <span class="opt">(optional)</span></label>
        <input id="sem-end" class="input" type="date" bind:value={endDate} />
      </div>
    </div>
    <div class="modal-actions">
      <button type="button" class="btn btn-ghost" onclick={() => onclose?.()}>Cancel</button>
      <button type="submit" class="btn btn-primary" disabled={busy}>{existing ? 'Save changes' : 'Create semester'}</button>
    </div>
  </form>
</Modal>
