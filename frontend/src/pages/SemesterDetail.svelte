<script>
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api.js';
  import { toast, confirmDialog } from '../lib/state.svelte.js';
  import { date, plural } from '../lib/format.js';
  import { route } from '../lib/router.svelte.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import SummaryStats from '../components/SummaryStats.svelte';
  import ModulesTable from '../components/ModulesTable.svelte';
  import SemesterDialog from '../components/SemesterDialog.svelte';
  import ModuleDialog from '../components/ModuleDialog.svelte';

  let { params } = $props();
  const id = params.id;

  let view = $state(null);
  let formulas = $state([]);
  let notFound = $state(false);
  let editSemester = $state(false);
  let moduleDialog = $state(null); // { module } when open
  let revision = $state(0);

  async function load() {
    try {
      const data = await api.get(`/semesters/${id}`);
      view = data.view;
      formulas = data.formulas;
      notFound = false;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        notFound = true;
        view = null;
      } else {
        toast.error(err.message);
      }
    }
  }

  onMount(async () => {
    await load();
    if (route.query.get('add') === '1' && view) moduleDialog = { module: null };
  });

  const subtitle = $derived.by(() => {
    if (!view) return '';
    const s = view.semester;
    const parts = [];
    if (s.academicYear?.trim()) parts.push(s.academicYear);
    if (s.startDate) parts.push(s.endDate ? `${date(s.startDate)} – ${date(s.endDate)}` : `from ${date(s.startDate)}`);
    else if (s.endDate) parts.push(`until ${date(s.endDate)}`);
    return parts.length === 0 ? "Click a module's arrow for details and the exam grade you need." : parts.join(' · ');
  });

  async function run(action) {
    try {
      await action();
    } catch (err) {
      toast.error(err.message);
      revision++; // re-create the inputs so rejected text doesn't linger
    }
    await load();
  }

  const saveGrades = (moduleId, grades) => run(() => api.put(`/modules/${moduleId}/grades`, grades));
  const setFormula = (moduleId, formulaId) => run(() => api.put(`/modules/${moduleId}/formula`, { formulaId }));
  const moveModule = (moduleId, direction) => run(() => api.post(`/modules/${moduleId}/move`, { direction }));

  async function deleteModule(v) {
    const ok = await confirmDialog(`Delete “${v.module.name}”?`, 'The module and all of its grades will be permanently deleted.', 'Delete module');
    if (!ok) return;
    await run(async () => {
      await api.del(`/modules/${v.module.id}`);
      toast.success('Module deleted');
    });
  }
</script>

<svelte:head><title>{view?.semester.name ?? 'Semester'} · Gradely</title></svelte:head>

{#if notFound}
  <EmptyState icon="search" title="Semester not found" text="It may have been deleted.">
    <a class="btn btn-primary" href="/semesters">Back to semesters</a>
  </EmptyState>
{:else if view === null}
  <Loading />
{:else}
  <a class="back-link" href="/semesters"><Icon name="back" size={16} />All semesters</a>
  <div class="page-head">
    <div>
      <h1>{view.semester.name}</h1>
      <p class="muted">{subtitle}</p>
    </div>
    <div class="page-actions">
      <button type="button" class="btn btn-ghost" onclick={() => (editSemester = true)}><Icon name="edit" size={16} />Edit semester</button>
      <button type="button" class="btn btn-primary" onclick={() => (moduleDialog = { module: null })}><Icon name="plus" size={16} />Add module</button>
    </div>
  </div>

  <SummaryStats summary={view.summary} />

  {#if view.modules.length === 0}
    <EmptyState icon="modules" title="No modules in this semester" text="Add the modules you're taking, then enter your grades as you receive them.">
      <button type="button" class="btn btn-primary" onclick={() => (moduleDialog = { module: null })}><Icon name="plus" size={16} />Add your first module</button>
    </EmptyState>
  {:else}
    <ModulesTable modules={view.modules} {formulas} {revision}
      ongrades={saveGrades} onformula={setFormula} oninvalid={(msg) => toast.error(msg)}
      onedit={(v) => (moduleDialog = { module: v.module })} ondelete={deleteModule} onmove={moveModule} />
    <p class="muted small table-hint">Grades save automatically when you leave a field. Leave a field empty for “not entered yet” — an empty field is never counted as zero.</p>
  {/if}
{/if}

{#if editSemester && view}
  <SemesterDialog existing={view.semester} onclose={() => (editSemester = false)} onsaved={async () => { editSemester = false; await load(); }} />
{/if}
{#if moduleDialog}
  <ModuleDialog semesterId={id} existing={moduleDialog.module} {formulas} onclose={() => (moduleDialog = null)}
    onsaved={async () => { moduleDialog = null; await load(); }} />
{/if}
