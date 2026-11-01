<script>
  import { operationLabel, errorLabel, ago, windowText } from '../../lib/integrations.js';
  import EmptyState from '../EmptyState.svelte';

  let { overview } = $props();

  const limits = $derived(overview.limits);
  const used = $derived(Math.min(100, Math.round((overview.windowRequests / limits.perUser) * 100)));
</script>

<div class="stat-grid">
  <div class="stat"><div class="stat-label">Active API keys</div><div class="stat-value" data-testid="active-keys">{overview.activeKeys}</div></div>
  <div class="stat"><div class="stat-label">Connected apps</div><div class="stat-value">{overview.connectedApps}</div></div>
  <div class="stat"><div class="stat-label">Total requests</div><div class="stat-value">{overview.totalRequests}</div></div>
  <div class="stat"><div class="stat-label">Revoked credentials</div><div class="stat-value">{overview.revokedCredentials}</div></div>
</div>

<section class="card">
  <div class="card-head">
    <div>
      <h2>Current rate-limit window</h2>
      <p class="muted small">Up to {limits.perCredential} requests per key or app and {limits.perUser} in total per {windowText(limits.windowSeconds)}, on a rolling window.</p>
    </div>
    <div class="avg"><span class="avg-value">{overview.windowRequests}</span><span class="avg-scale">/ {limits.perUser}</span></div>
  </div>
  <div class="bar" role="progressbar" aria-label="Requests used in the current window" aria-valuemin="0" aria-valuemax={limits.perUser} aria-valuenow={overview.windowRequests}>
    <div class="bar-fill {used >= 90 ? 'bar-fill-warn' : ''}" style="width: {used}%"></div>
  </div>
  {#if overview.failuresLast24h > 0}
    <p class="muted small rl-note">{overview.failuresLast24h} failed request{overview.failuresLast24h === 1 ? '' : 's'} in the last 24 hours — see Activity for details.</p>
  {/if}
</section>

<div class="section-title"><h2>Recent activity</h2><a class="link" href="/settings/integrations?tab=activity">View all</a></div>
{#if overview.recent.length === 0}
  <EmptyState icon="activity" title="No activity yet" text="Once an assistant or script uses your MCP endpoint, its actions show up here." />
{:else}
  <ul class="activity-list card">
    {#each overview.recent as ev (ev.id)}
      <li>
        <span class="dot {ev.success ? 'dot-success' : 'dot-danger'}" aria-hidden="true"></span>
        <span class="activity-what"><strong>{ev.credentialName}</strong> · {operationLabel(ev.operation)}</span>
        <span class="muted small">{ev.success ? 'Success' : errorLabel(ev.errorCode)}</span>
        <span class="muted small activity-when" title={ev.time}>{ago(ev.time)}</span>
      </li>
    {/each}
  </ul>
{/if}
