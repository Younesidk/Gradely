<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { toast, confirmDialog } from '../lib/state.svelte.js';
  import { plural } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import SemesterCard from '../components/SemesterCard.svelte';
  import SemesterDialog from '../components/SemesterDialog.svelte';

  let semesters = $state(null);
  let dialog = $state(null); // { semester } when open

  const load = async () => (semesters = await api.get('/semesters'));
  onMount(load);

  async function move(s, direction) {
    try {
      await api.post(`/semesters/${s.semester.id}/move`, { direction });
    } catch (err) {
      toast.error(err.message);
    }
    await load();
  }

  async function remove(s) {
    const count = s.modules.length;
    const ok = await confirmDialog(
      `Delete “${s.semester.name}”?`,
      count === 0
        ? 'This semester will be permanently deleted.'
        : `This permanently deletes the semester and its ${plural(count, 'module')} with all their grades. This can't be undone.`,
      'Delete semester',
    );
    if (!ok) return;
    try {
      await api.del(`/semesters/${s.semester.id}`);
      toast.success('Semester deleted');
    } catch (err) {
      toast.error(err.message);
    }
    await load();
  }
</script>

<svelte:head><title>Semesters · Gradly</title></svelte:head>

<div class="page-head">
  <div>
    <h1>Semesters</h1>
    <p class="muted">Each semester has its own modules, grades and weighted average</p>
  </div>
  <div class="page-actions">
    <button type="button" class="btn btn-primary" onclick={() => (dialog = { semester: null })}><Icon name="plus" size={16} />New semester</button>
  </div>
</div>

{#if semesters === null}
  <Loading />
{:else if semesters.length === 0}
  <EmptyState icon="semesters" title="No semesters yet" text="Create your first semester, then add its modules and grades.">
    <button type="button" class="btn btn-primary" onclick={() => (dialog = { semester: null })}><Icon name="plus" size={16} />Create a semester</button>
  </EmptyState>
{:else}
  <div class="card-grid">
    {#each semesters as s, i (s.semester.id)}
      <SemesterCard view={s} href="/semesters/{s.semester.id}">
        {#snippet actions()}
          <div class="card-actions">
            <a class="btn btn-ghost btn-sm" href="/semesters/{s.semester.id}">Open</a>
            <button type="button" class="btn btn-ghost btn-sm" onclick={() => (dialog = { semester: s.semester })}><Icon name="edit" size={15} />Edit</button>
            <button type="button" class="icon-btn" aria-label="Move {s.semester.name} up" title="Move up" disabled={i === 0} onclick={() => move(s, -1)}><Icon name="up" size={16} /></button>
            <button type="button" class="icon-btn" aria-label="Move {s.semester.name} down" title="Move down" disabled={i === semesters.length - 1} onclick={() => move(s, 1)}><Icon name="down" size={16} /></button>
            <button type="button" class="icon-btn text-danger" aria-label="Delete {s.semester.name}" title="Delete" onclick={() => remove(s)}><Icon name="trash" size={16} /></button>
          </div>
        {/snippet}
      </SemesterCard>
    {/each}
  </div>
{/if}

{#if dialog}
  <SemesterDialog existing={dialog.semester} onclose={() => (dialog = null)} onsaved={async () => { dialog = null; await load(); }} />
{/if}
