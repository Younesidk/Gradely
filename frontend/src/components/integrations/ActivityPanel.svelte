<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { stamp } from '../../lib/format.js';
  import { operationLabel, errorLabel } from '../../lib/integrations.js';
  import Loading from '../Loading.svelte';
  import EmptyState from '../EmptyState.svelte';

  let data = $state(null);
  let loadError = $state(null);
  let credential = $state('');
  let operation = $state('');
  let status = $state('');
  let page = $state(1);

  async function load() {
    const q = new URLSearchParams({ page: String(page), pageSize: '25' });
    if (credential) q.set('credential', credential);
    if (operation) q.set('operation', operation);
    if (status) q.set('status', status);
    try {
      data = await api.get('/integrations/activity?' + q);
      loadError = null;
    } catch (err) {
      loadError = err.message;
    }
  }
  onMount(load);

  function filter() {
    page = 1;
    load();
  }

  function goTo(n) {
    page = n;
    load();
  }

  const totalPages = $derived(data ? Math.max(1, Math.ceil(data.total / data.pageSize)) : 1);
  const filtered = $derived(credential !== '' || operation !== '' || status !== '');
</script>

<div class="section-title section-title-first">
  <div>
    <h2>Activity</h2>
    <p class="muted small">What your integrations did. Only the action and its outcome are recorded — never your grades, keys or tokens.{#if data}{' '}Entries are kept for {data.retentionDays} days.{/if}</p>
  </div>
</div>

{#if data}
  <div class="filters" role="group" aria-label="Filter activity">
    <div class="field">
      <label for="f-cred">Integration</label>
      <select id="f-cred" class="input input-sm" bind:value={credential} onchange={filter}>
        <option value="">All integrations</option>
        {#each data.credentials as c (c.id)}<option value={c.id}>{c.name} ({c.type === 'oauth' ? 'app' : 'key'})</option>{/each}
      </select>
    </div>
    <div class="field">
      <label for="f-op">Action</label>
      <select id="f-op" class="input input-sm" bind:value={operation} onchange={filter}>
        <option value="">All actions</option>
        {#each data.operations as op}<option value={op}>{operationLabel(op)}</option>{/each}
      </select>
    </div>
    <div class="field">
      <label for="f-status">Result</label>
      <select id="f-status" class="input input-sm" bind:value={status} onchange={filter}>
        <option value="">Any result</option>
        <option value="success">Success</option>
        <option value="failure">Failed</option>
      </select>
    </div>
  </div>
{/if}

{#if loadError}
  <div class="alert alert-danger" role="alert">{loadError} <button type="button" class="link-btn" onclick={load}>Try again</button></div>
{:else if data === null}
  <Loading />
{:else if data.items.length === 0}
  <EmptyState icon="activity" title={filtered ? 'Nothing matches these filters' : 'No activity yet'}
    text={filtered ? 'Try another integration, action or result.' : 'Requests made with your API keys or connected apps will be listed here.'} />
{:else}
  <div class="table-wrap">
    <table class="table">
      <thead>
        <tr><th scope="col">Time</th><th scope="col">Integration</th><th scope="col">Action</th><th scope="col">Result</th><th scope="col" class="num">Duration</th></tr>
      </thead>
      <tbody>
        {#each data.items as ev (ev.id)}
          <tr>
            <td data-label="Time" class="nowrap">{stamp(ev.time)}</td>
            <td data-label="Integration"><div class="cell-title"><strong>{ev.credentialName}</strong><span class="muted small">{ev.credentialType === 'oauth' ? 'Connected app' : 'API key'}</span></div></td>
            <td data-label="Action" title={ev.operation}>{operationLabel(ev.operation)}</td>
            <td data-label="Result">
              {#if ev.success}<span class="badge badge-success">Success</span>
              {:else}<span class="badge badge-danger">{errorLabel(ev.errorCode)}</span> <span class="muted small">{ev.status}</span>{/if}
            </td>
            <td data-label="Duration" class="num">{ev.durationMs} ms</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <div class="pager">
    <span class="muted small">{data.total} event{data.total === 1 ? '' : 's'} · page {data.page} of {totalPages}</span>
    <div>
      <button type="button" class="btn btn-ghost btn-sm" disabled={data.page <= 1} onclick={() => goTo(data.page - 1)}>Previous</button>
      <button type="button" class="btn btn-ghost btn-sm" disabled={data.page >= totalPages} onclick={() => goTo(data.page + 1)}>Next</button>
    </div>
  </div>
{/if}
