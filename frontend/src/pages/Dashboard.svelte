<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import { navigate } from '../lib/router.svelte.js';
  import { fixed2, plural } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import SummaryStats from '../components/SummaryStats.svelte';
  import SemesterCard from '../components/SemesterCard.svelte';
  import SemesterDialog from '../components/SemesterDialog.svelte';
  import PassLine from '../components/PassLine.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';

  let data = $state(null);
  let showNew = $state(false);

  onMount(async () => {
    try {
      data = await api.get('/dashboard');
    } catch (err) {
      toast.error(err.message);
      data = { semesters: [], overall: { moduleCount: 0 }, current: null };
    }
  });
</script>

<svelte:head><title>Dashboard · Gradely</title></svelte:head>

{#if data === null}
  <Loading text="Loading your dashboard…" />
{:else}
  <div class="page-head">
    <div>
      <h1>Dashboard</h1>
      <p class="muted">
        {data.semesters.length === 0 ? 'No semesters yet' : `${plural(data.semesters.length, 'semester')} · ${plural(data.overall.moduleCount, 'module')}`}
      </p>
    </div>
    <div class="page-actions">
      <button type="button" class="btn btn-primary" onclick={() => (showNew = true)}><Icon name="plus" size={16} />New semester</button>
    </div>
  </div>

  {#if data.semesters.length === 0}
    <section class="card onboarding">
      <h2>Get started in three steps</h2>
      <ol class="steps">
        <li><span class="step-num">1</span><div><strong>Create a semester</strong><p class="muted">For example “S5” or “Spring 2027”. Add as many as you need.</p></div></li>
        <li><span class="step-num">2</span><div><strong>Add your modules</strong><p class="muted">Give each a coefficient. Passing threshold and maximum grade are adjustable.</p></div></li>
        <li><span class="step-num">3</span><div><strong>Enter your grades</strong><p class="muted">CC1, CC2, lab and exam. Gradely tells you the exam grade you need to pass.</p></div></li>
      </ol>
      <div class="onboarding-actions">
        <button type="button" class="btn btn-primary" onclick={() => (showNew = true)}><Icon name="plus" size={16} />Create your first semester</button>
        <a class="btn btn-ghost" href="/formulas">Review grading formulas</a>
      </div>
    </section>
  {:else}
    <SummaryStats summary={data.overall} label="Overall average" />

    {#if data.current}
      {@const cur = data.current}
      <section class="card current">
        <div class="card-head">
          <div>
            <div class="eyebrow">Current semester</div>
            <h2><a href="/semesters/{cur.semester.id}">{cur.semester.name}</a></h2>
          </div>
          <div class="card-head-actions">
            <a class="btn btn-ghost btn-sm" href="/semesters/{cur.semester.id}">Open</a>
            <a class="btn btn-primary btn-sm" href="/semesters/{cur.semester.id}?add=1"><Icon name="plus" size={15} />Add module</a>
          </div>
        </div>
        {#if cur.modules.length === 0}
          <p class="muted">No modules yet. Add one to start tracking grades.</p>
        {:else}
          <ul class="mini-list">
            {#each cur.modules.slice(0, 8) as m (m.module.id)}
              <li>
                <span class="mini-name">{m.module.name}</span>
                {#if m.result.grade !== null}
                  <PassLine value={m.result.grade} max={m.module.maxGrade} threshold={m.module.passingThreshold} passed={m.result.passed} />
                {/if}
                <span class="mini-grade">{m.result.grade !== null ? fixed2(m.result.grade) : '—'}</span>
                <StatusBadge result={m.result} />
              </li>
            {/each}
          </ul>
          {#if cur.modules.length > 8}
            <p class="muted small">{plural(cur.modules.length - 8, 'more module')} in this semester</p>
          {/if}
        {/if}
      </section>
    {/if}

    <div class="section-title"><h2>Your semesters</h2><a href="/semesters" class="link">Manage</a></div>
    <div class="card-grid">
      {#each data.semesters as s (s.semester.id)}
        <SemesterCard view={s} href="/semesters/{s.semester.id}" />
      {/each}
    </div>
  {/if}
{/if}

{#if showNew}
  <SemesterDialog onclose={() => (showNew = false)} onsaved={(id) => navigate('/semesters/' + id)} />
{/if}
