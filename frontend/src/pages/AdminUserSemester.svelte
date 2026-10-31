<script>
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import AdminBanner from '../components/AdminBanner.svelte';
  import SummaryStats from '../components/SummaryStats.svelte';
  import ModulesTable from '../components/ModulesTable.svelte';

  let { params } = $props();
  let data = $state(null);
  let notFound = $state(false);

  onMount(async () => {
    try {
      data = await api.get(`/admin/users/${params.id}/semesters/${params.sid}`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) notFound = true;
      else toast.error(err.message);
    }
  });
</script>

<svelte:head><title>{data?.view.semester.name ?? 'Semester'} · Gradely</title></svelte:head>

{#if notFound}
  <EmptyState icon="search" title="Not found"><a class="btn btn-primary" href="/admin/users">Back to users</a></EmptyState>
{:else if data === null}
  <Loading />
{:else}
  {@const view = data.view}
  <a class="back-link" href="/admin/users/{params.id}"><Icon name="back" size={16} />{data.user.userName}</a>
  <AdminBanner userName={data.user.userName} />

  <div class="page-head">
    <div>
      <h1>{view.semester.name}</h1>
      <p class="muted">{view.semester.academicYear?.trim() ? view.semester.academicYear + ' · read-only view' : 'Read-only view'}</p>
    </div>
  </div>

  <SummaryStats summary={view.summary} />

  {#if view.modules.length === 0}
    <EmptyState icon="modules" title="No modules in this semester" />
  {:else}
    <ModulesTable modules={view.modules} readonly />
  {/if}
{/if}
