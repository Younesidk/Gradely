<script>
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api.js';
  import { toast, confirmDialog } from '../lib/state.svelte.js';
  import { bytes, date } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import AdminBanner from '../components/AdminBanner.svelte';
  import SemesterCard from '../components/SemesterCard.svelte';

  let { params } = $props();
  let detail = $state(null);
  let notFound = $state(false);

  async function load() {
    try {
      detail = await api.get(`/admin/users/${params.id}`);
      notFound = false;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        notFound = true;
        detail = null;
      } else {
        toast.error(err.message);
      }
    }
  }
  onMount(load);

  async function toggle() {
    const u = detail.row;
    const disabling = !u.isDisabled;
    if (disabling) {
      const ok = await confirmDialog(`Disable ${u.userName}?`, 'They will be signed out and unable to sign in. Their data is kept.', 'Disable account');
      if (!ok) return;
    }
    try {
      await api.post(`/admin/users/${u.id}/disabled`, { disabled: disabling });
      toast.success(disabling ? 'Account disabled' : 'Account re-enabled');
    } catch (err) {
      toast.error(err.message);
    }
    await load();
  }
</script>

<svelte:head><title>{detail?.row.userName ?? 'User'} · Gradly</title></svelte:head>

{#if notFound}
  <EmptyState icon="search" title="User not found"><a class="btn btn-primary" href="/admin/users">Back to users</a></EmptyState>
{:else if detail === null}
  <Loading />
{:else}
  {@const u = detail.row}
  <a class="back-link" href="/admin/users"><Icon name="back" size={16} />All users</a>
  <AdminBanner userName={u.userName} />

  <div class="page-head">
    <div>
      <h1>{u.userName}</h1>
      <p class="muted">{u.email}</p>
    </div>
    <div class="page-actions">
      {#if !u.isAdmin}
        {#if u.isDisabled}
          <button type="button" class="btn btn-primary" onclick={toggle}>Re-enable account</button>
        {:else}
          <button type="button" class="btn btn-danger" onclick={toggle}>Disable account</button>
        {/if}
      {/if}
    </div>
  </div>

  <section class="card">
    <dl class="facts">
      <div>
        <dt>Status</dt>
        <dd>
          {#if u.isDisabled}<span class="badge badge-danger">Disabled</span>{:else}<span class="badge badge-success">Active</span>{/if}
          {#if u.isAdmin}<span class="tag tag-primary">Administrator</span>{/if}
        </dd>
      </div>
      <div><dt>Registered</dt><dd>{date(u.createdAt)}</dd></div>
      <div><dt>Last sign-in</dt><dd>{date(detail.lastLoginAt)}</dd></div>
      {#if u.isDisabled}<div><dt>Disabled on</dt><dd>{date(detail.disabledAt)}</dd></div>{/if}
      <div><dt>Semesters / modules</dt><dd>{u.semesters} / {u.modules}</dd></div>
      <div><dt>Formulas</dt><dd>{detail.formulas}</dd></div>
      <div><dt>Approx. storage</dt><dd>≈ {bytes(u.storageBytes)} <span class="muted small">(estimate)</span></dd></div>
      <div><dt>Pending invitation for this email</dt><dd>{detail.pendingInvitationsForEmail > 0 ? 'Yes' : 'No'}</dd></div>
    </dl>
  </section>

  <div class="section-title"><h2>Academic records</h2></div>
  {#if detail.semesters.length === 0}
    <EmptyState icon="semesters" title="No semesters" text="This user hasn't created any semesters yet." />
  {:else}
    <div class="card-grid">
      {#each detail.semesters as s (s.semester.id)}
        <SemesterCard view={s} href="/admin/users/{u.id}/semesters/{s.semester.id}" />
      {/each}
    </div>
  {/if}
{/if}
