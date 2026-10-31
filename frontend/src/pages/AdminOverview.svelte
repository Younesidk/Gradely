<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import { bytes, stamp } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';

  let data = $state(null);
  let busy = $state(false);
  let lab = $state('');

  async function load(refresh) {
    data = await api.get('/admin/overview' + (refresh ? '?refresh=1' : ''));
    lab = data.defaultLabFormula ?? '';
  }
  onMount(() => load(false));

  async function refreshStorage() {
    busy = true;
    try {
      await load(true);
      toast.success('Storage estimates recalculated');
    } finally {
      busy = false;
    }
  }

  async function saveLab(e) {
    e.preventDefault();
    busy = true;
    try {
      await api.put('/admin/default-lab-formula', { expression: lab });
      toast.success(lab.trim() === '' ? 'Default lab formula cleared' : 'Default lab formula saved');
      await load(false);
    } catch (err) {
      toast.error(err.message);
    } finally {
      busy = false;
    }
  }

  const top = $derived(data ? Math.max(1, ...data.largestUsers.map((u) => u.storageBytes)) : 1);
</script>

<svelte:head><title>Administration · Gradly</title></svelte:head>

<div class="page-head">
  <div>
    <h1>Administration</h1>
    <p class="muted">Platform overview, storage estimates and defaults</p>
  </div>
  <div class="page-actions">
    <a class="btn btn-ghost" href="/admin/users"><Icon name="users" size={16} />Users</a>
    <a class="btn btn-primary" href="/admin/invitations"><Icon name="mail" size={16} />Invite someone</a>
  </div>
</div>

{#if data === null}
  <Loading />
{:else}
  <div class="stat-grid">
    <div class="stat"><div class="stat-label">Users</div><div class="stat-value">{data.users}</div></div>
    <div class="stat stat-success"><div class="stat-label">Active</div><div class="stat-value">{data.activeUsers}</div></div>
    <div class="stat stat-danger"><div class="stat-label">Disabled</div><div class="stat-value">{data.disabledUsers}</div></div>
    <div class="stat stat-warn"><div class="stat-label">Pending invitations</div><div class="stat-value">{data.pendingInvitations}</div></div>
    <div class="stat"><div class="stat-label">Semesters</div><div class="stat-value">{data.semesters}</div></div>
    <div class="stat"><div class="stat-label">Modules</div><div class="stat-value">{data.modules}</div></div>
  </div>

  <section class="card">
    <div class="card-head">
      <div>
        <h2>Storage (estimate)</h2>
        <p class="muted small">
          SQLite keeps all data in one file, so exact per-user disk usage can't be measured.
          These figures are <strong>approximate logical sizes</strong> of each user's own records
          (account, semesters, modules, formulas). Calculated {stamp(data.storage.calculatedAt)}, cached for a few minutes.
        </p>
      </div>
      <button type="button" class="btn btn-ghost btn-sm" onclick={refreshStorage} disabled={busy}>Recalculate</button>
    </div>
    <div class="stat-grid stat-grid-compact">
      <div class="stat"><div class="stat-label">Estimated user data</div><div class="stat-value">{bytes(data.storage.totalEstimatedBytes)}</div></div>
      <div class="stat">
        <div class="stat-label">Database file</div>
        <div class="stat-value">{bytes(data.storage.databaseFileBytes)}</div>
        <div class="stat-note">+ WAL {bytes(data.storage.walFileBytes)}</div>
      </div>
    </div>
    <h3 class="subhead">Largest consumers</h3>
    {#if data.largestUsers.length === 0}
      <p class="muted">No data yet.</p>
    {:else}
      <ul class="bar-list">
        {#each data.largestUsers as u (u.id)}
          <li>
            <a href="/admin/users/{u.id}" class="bar-name">{u.userName}</a>
            <div class="bar" role="img" aria-label={bytes(u.storageBytes)}>
              <div class="bar-fill" style="width: {Math.max(2, Math.round((100 * u.storageBytes) / top))}%"></div>
            </div>
            <span class="bar-value">≈ {bytes(u.storageBytes)}</span>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  <section class="card">
    <h2>Default lab formula</h2>
    <p class="muted small">
      Applied to <strong>new</strong> accounts as their starter formula for modules with a lab exam.
      Gradly doesn't assume a lab weighting: leave this empty to make each student define their own.
      Existing users can add it from Formula settings.
    </p>
    <form class="form" onsubmit={saveLab}>
      <div class="field">
        <label for="lab-formula">Expression</label>
        <input id="lab-formula" class="input mono" bind:value={lab} placeholder="e.g. (CC1 + CC2) / 2 * 0.2 + Lab * 0.2 + Exam * 0.6" />
      </div>
      <button type="submit" class="btn btn-primary" disabled={busy}>Save default</button>
    </form>
  </section>
{/if}
