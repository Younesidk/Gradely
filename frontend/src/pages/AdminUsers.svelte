<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { toast, confirmDialog } from '../lib/state.svelte.js';
  import { bytes, date } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  let page = $state(null);
  let search = $state('');
  let pageNumber = 1;
  let timer;

  const load = async () => (page = await api.get(`/admin/users?page=${pageNumber}&search=${encodeURIComponent(search)}`));
  onMount(load);

  function onSearch() {
    clearTimeout(timer);
    timer = setTimeout(() => {
      pageNumber = 1;
      load();
    }, 250);
  }

  async function goTo(n) {
    pageNumber = n;
    await load();
  }

  async function toggle(u) {
    const disabling = !u.isDisabled;
    if (disabling) {
      const ok = await confirmDialog(`Disable ${u.userName}?`,
        'They will be signed out and unable to sign in. Their data is kept and everything comes back if you re-enable the account.',
        'Disable account');
      if (!ok) return;
    }
    try {
      await api.post(`/admin/users/${u.id}/disabled`, { disabled: disabling });
      toast.success(disabling ? `${u.userName} was disabled` : `${u.userName} was re-enabled`);
    } catch (err) {
      toast.error(err.message);
    }
    await load();
  }
</script>

<svelte:head><title>Users · Gradly</title></svelte:head>

<div class="page-head">
  <div>
    <h1>Users</h1>
    <p class="muted">All registered accounts, with estimated storage</p>
  </div>
  <div class="page-actions">
    <a class="btn btn-primary" href="/admin/invitations"><Icon name="mail" size={16} />Invite someone</a>
  </div>
</div>

<div class="toolbar">
  <input class="input search" type="search" placeholder="Search by username or email…" aria-label="Search users" bind:value={search} oninput={onSearch} />
</div>

{#if page === null}
  <Loading />
{:else if page.items.length === 0}
  <EmptyState icon="users" title="No users found" text={search.trim() === '' ? 'Invite someone to get started.' : 'Try a different search.'} />
{:else}
  <div class="table-wrap">
    <table class="table users-table">
      <thead>
        <tr>
          <th scope="col">Username</th>
          <th scope="col">Email</th>
          <th scope="col">Registered</th>
          <th scope="col">Status</th>
          <th scope="col" class="num">Semesters</th>
          <th scope="col" class="num">Modules</th>
          <th scope="col" class="num">Storage ≈</th>
          <th scope="col"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each page.items as u (u.id)}
          <tr>
            <td data-label="Username"><a href="/admin/users/{u.id}"><strong>{u.userName}</strong></a> {#if u.isAdmin}<span class="tag tag-primary">Admin</span>{/if}</td>
            <td data-label="Email">{u.email}</td>
            <td data-label="Registered">{date(u.createdAt)}</td>
            <td data-label="Status">{#if u.isDisabled}<span class="badge badge-danger">Disabled</span>{:else}<span class="badge badge-success">Active</span>{/if}</td>
            <td data-label="Semesters" class="num">{u.semesters}</td>
            <td data-label="Modules" class="num">{u.modules}</td>
            <td data-label="Storage ≈" class="num">{bytes(u.storageBytes)}</td>
            <td class="row-actions">
              <a class="btn btn-ghost btn-sm" href="/admin/users/{u.id}"><Icon name="eye" size={15} />View</a>
              {#if !u.isAdmin}
                {#if u.isDisabled}
                  <button type="button" class="btn btn-ghost btn-sm" onclick={() => toggle(u)}>Re-enable</button>
                {:else}
                  <button type="button" class="btn btn-ghost btn-sm text-danger" onclick={() => toggle(u)}>Disable</button>
                {/if}
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  <div class="pager">
    <span class="muted small">{page.total} user{page.total === 1 ? '' : 's'} · page {page.page} of {page.totalPages}</span>
    <div>
      <button type="button" class="btn btn-ghost btn-sm" disabled={page.page <= 1} onclick={() => goTo(page.page - 1)}>Previous</button>
      <button type="button" class="btn btn-ghost btn-sm" disabled={page.page >= page.totalPages} onclick={() => goTo(page.page + 1)}>Next</button>
    </div>
  </div>
{/if}
