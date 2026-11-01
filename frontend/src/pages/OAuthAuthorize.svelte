<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { auth } from '../lib/state.svelte.js';
  import { route } from '../lib/router.svelte.js';
  import { hasWrite } from '../lib/integrations.js';
  import Loading from '../components/Loading.svelte';
  import ScopePicker from '../components/integrations/ScopePicker.svelte';

  // The OAuth consent screen. Everything is validated by the server; this page only shows the request and sends
  // back the signed-in user's decision, then follows the redirect the server returns.
  const keys = ['response_type', 'client_id', 'redirect_uri', 'scope', 'state', 'code_challenge', 'code_challenge_method', 'resource'];
  const params = Object.fromEntries(keys.map((k) => [k, route.query.get(k) ?? '']));

  let info = $state(null);
  let error = $state(null);
  let scopes = $state([]);
  let busy = $state(false);
  let leaving = $state(false);

  onMount(async () => {
    try {
      const q = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== ''));
      info = await api.get('/oauth/authorize?' + q);
      if (info.ok) {
        // Pre-select what the app asked for, but only read access when it didn't say.
        scopes = info.requested.filter((s) => info.scopeWasRequested || s.access === 'read').map((s) => s.scope);
      }
    } catch (err) {
      error = err.message;
    }
  });

  async function decide(approve) {
    busy = true;
    error = null;
    try {
      const r = await api.post('/oauth/authorize', {
        responseType: params.response_type, clientId: params.client_id, redirectUri: params.redirect_uri, scope: params.scope,
        state: params.state, codeChallenge: params.code_challenge, codeChallengeMethod: params.code_challenge_method,
        resource: params.resource, approve, scopes,
      });
      leaving = true;
      window.location.assign(r.redirect);
    } catch (err) {
      error = err.message;
      busy = false;
    }
  }

  function backToApp() {
    leaving = true;
    window.location.assign(info.redirect);
  }
</script>

<svelte:head><title>Authorize application · Gradly</title></svelte:head>

{#if info === null && !error}
  <Loading />
{:else if leaving}
  <h1 class="auth-title">Returning to the application…</h1>
  <p class="muted">You can close this tab if nothing happens.</p>
{:else if error && !info}
  <h1 class="auth-title">Something went wrong</h1>
  <div class="alert alert-danger" role="alert">{error}</div>
  <a class="btn btn-ghost btn-block" href="/">Go to Gradly</a>
{:else if !info.ok}
  <h1 class="auth-title">This request can't be completed</h1>
  <div class="alert alert-danger" role="alert">{info.error}</div>
  {#if info.redirect}
    <button type="button" class="btn btn-primary btn-block" onclick={backToApp}>Return to the application</button>
  {:else}
    <p class="muted small">Nothing was shared. You can close this tab.</p>
    <a class="btn btn-ghost btn-block" href="/">Go to Gradly</a>
  {/if}
{:else}
  <h1 class="auth-title">Allow <span class="consent-app">{info.client.name}</span> to use your Gradly account?</h1>
  <p class="muted auth-sub">Signed in as <strong>{auth.user?.userName}</strong>. After you allow it, you'll be sent back to <span class="mono">{info.redirectHost}</span>.</p>
  {#if info.alreadyGranted.length > 0}
    <div class="callout callout-info small">This app is already connected. Approving again replaces its current permissions.</div>
  {/if}
  {#if error}<div class="alert alert-danger" role="alert">{error}</div>{/if}

  <p class="consent-label">It asks for these permissions. Untick anything you don't want to give:</p>
  <ScopePicker catalog={info.requested} bind:selected={scopes} idPrefix="consent" presets={info.requested.length > 2} />
  {#if hasWrite(scopes)}
    <p class="hint hint-warn consent-warn">This app will be able to change your academic data.</p>
  {/if}
  <p class="hint">You can disconnect it at any time in Account settings → Developer integrations. Gradly never shares your password.</p>

  <div class="consent-actions">
    <button type="button" class="btn btn-ghost" disabled={busy} onclick={() => decide(false)}>Deny</button>
    <button type="button" class="btn btn-primary" disabled={busy || scopes.length === 0} onclick={() => decide(true)}>Allow access</button>
  </div>
{/if}
