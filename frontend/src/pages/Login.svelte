<script>
  import { api } from '../lib/api.js';
  import { auth } from '../lib/state.svelte.js';
  import { navigate, route } from '../lib/router.svelte.js';
  import { safeLocal } from '../lib/format.js';

  let identifier = $state('');
  let password = $state('');
  let remember = $state(false);
  let error = $state(null);
  let busy = $state(false);

  const notice = $derived(route.query.get('notice'));
  const returnUrl = $derived(safeLocal(route.query.get('returnUrl')));

  $effect(() => {
    if (auth.user) navigate(returnUrl, { replace: true });
  });

  async function submit(e) {
    e.preventDefault();
    busy = true;
    error = null;
    try {
      auth.user = await api.post('/auth/login', { identifier, password, remember });
      navigate(returnUrl, { replace: true });
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Sign in · Gradely</title></svelte:head>

<h1 class="auth-title">Sign in</h1>
<p class="muted auth-sub">Use your username or email and password</p>

{#if notice === 'password-changed'}
  <div class="alert alert-success" role="status">Your password was changed. Please sign in again.</div>
{:else if notice === 'signed-out'}
  <div class="alert alert-info" role="status">You've been signed out.</div>
{/if}
{#if error}<div class="alert alert-danger" role="alert">{error}</div>{/if}

<form class="form" onsubmit={submit}>
  <div class="field">
    <label for="identifier">Username or email</label>
    <input id="identifier" class="input" bind:value={identifier} autocomplete="username" required />
  </div>
  <div class="field">
    <label for="password">Password</label>
    <input id="password" type="password" class="input" bind:value={password} autocomplete="current-password" required />
  </div>
  <label class="check"><input type="checkbox" bind:checked={remember} /> <span>Keep me signed in on this device</span></label>
  <button type="submit" class="btn btn-primary btn-block" disabled={busy}>Sign in</button>
</form>

<p class="muted small auth-foot">Gradely is invitation-only. Ask your administrator for an invitation link.</p>
