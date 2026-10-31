<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { navigate, route } from '../lib/router.svelte.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import SettingsTabs from '../components/SettingsTabs.svelte';
  import OverviewPanel from '../components/integrations/OverviewPanel.svelte';
  import ConnectionsPanel from '../components/integrations/ConnectionsPanel.svelte';
  import ApiKeysPanel from '../components/integrations/ApiKeysPanel.svelte';
  import PermissionsPanel from '../components/integrations/PermissionsPanel.svelte';
  import ActivityPanel from '../components/integrations/ActivityPanel.svelte';

  const sections = [
    { id: 'overview', label: 'Overview', icon: 'dashboard' },
    { id: 'connections', label: 'MCP connections', icon: 'plug' },
    { id: 'keys', label: 'API keys', icon: 'key' },
    { id: 'permissions', label: 'Permissions', icon: 'shield' },
    { id: 'activity', label: 'Activity', icon: 'activity' },
  ];

  let meta = $state(null);
  let overview = $state(null);
  let loadError = $state(null);

  const tab = $derived(sections.some((s) => s.id === route.query.get('tab')) ? route.query.get('tab') : 'overview');

  async function load() {
    try {
      [meta, overview] = await Promise.all([api.get('/integrations/meta'), api.get('/integrations/overview')]);
      loadError = null;
    } catch (err) {
      loadError = err.message;
    }
  }
  onMount(load);

  const refreshOverview = async () => {
    try {
      overview = await api.get('/integrations/overview');
    } catch {
      /* the panel that changed already reported its own result */
    }
  };

  function select(id) {
    navigate(id === 'overview' ? '/settings/integrations' : `/settings/integrations?tab=${id}`, { replace: true });
  }

  function onKey(e, index) {
    const step = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
    if (!step) return;
    e.preventDefault();
    const next = sections[(index + step + sections.length) % sections.length];
    select(next.id);
    document.getElementById(`int-tab-${next.id}`)?.focus();
  }
</script>

<svelte:head><title>Developer integrations · Gradly</title></svelte:head>

<div class="page-head">
  <div>
    <h1>Developer integrations</h1>
    <p class="muted">Connect AI assistants and scripts to your Gradly data, and control exactly what they can do.</p>
  </div>
</div>

<SettingsTabs />

{#if loadError}
  <div class="alert alert-danger" role="alert">{loadError} <button type="button" class="link-btn" onclick={load}>Try again</button></div>
{:else if meta === null || overview === null}
  <Loading />
{:else}
  <div class="subtabs" role="tablist" aria-label="Developer integrations">
    {#each sections as s, i (s.id)}
      <button type="button" role="tab" id="int-tab-{s.id}" aria-selected={tab === s.id} aria-controls="int-panel" tabindex={tab === s.id ? 0 : -1}
        class="subtab {tab === s.id ? 'active' : ''}" onclick={() => select(s.id)} onkeydown={(e) => onKey(e, i)}>
        <Icon name={s.icon} size={15} />{s.label}
      </button>
    {/each}
  </div>

  <div id="int-panel" role="tabpanel" aria-labelledby="int-tab-{tab}">
    {#if tab === 'overview'}
      <OverviewPanel {overview} />
    {:else if tab === 'connections'}
      <ConnectionsPanel {meta} onchange={refreshOverview} />
    {:else if tab === 'keys'}
      <ApiKeysPanel {meta} onchange={refreshOverview} />
    {:else if tab === 'permissions'}
      <PermissionsPanel {meta} />
    {:else}
      <ActivityPanel />
    {/if}
  </div>
{/if}
