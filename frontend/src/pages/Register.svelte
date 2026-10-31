<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { auth } from '../lib/state.svelte.js';
  import { navigate, route } from '../lib/router.svelte.js';
  import Loading from '../components/Loading.svelte';

  const INVALID = 'This invitation link is invalid, has expired or was already used.';
  const token = route.query.get('token') ?? '';

  let email = $state(null);
  let loaded = $state(false);
  let userName = $state('');
  let password = $state('');
  let confirm = $state('');
  let error = $state(null);
  let busy = $state(false);

  onMount(async () => {
    try {
      email = (await api.get('/auth/invitation?token=' + encodeURIComponent(token))).email;
    } catch {
      email = null;
    }
    loaded = true;
  });

  async function submit(e) {
    e.preventDefault();
    error = null;
    const name = userName.trim();
    if (name.length < 3 || name.length > 40) {
      error = 'Usernames are 3 to 40 characters.';
      return;
    }
    if (password !== confirm) {
      error = "The passwords don't match.";
      return;
    }
    busy = true;
    try {
      auth.user = await api.post('/auth/register', { token, userName: name, password });
      navigate('/', { replace: true });
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Create your account · Gradly</title></svelte:head>

{#if !loaded}
  <Loading />
{:else if email === null}
  <h1 class="auth-title">Invitation not valid</h1>
  <div class="alert alert-danger" role="alert">{INVALID}</div>
  <p class="muted">Ask your administrator to send you a new invitation.</p>
  <a class="btn btn-ghost btn-block" href="/account/login">Go to sign in</a>
{:else}
  <h1 class="auth-title">Create your account</h1>
  <p class="muted auth-sub">You've been invited to Gradly. Choose a username and a password.</p>
  {#if error}<div class="alert alert-danger" role="alert">{error}</div>{/if}

  <form class="form" onsubmit={submit}>
    <div class="field">
      <label for="email">Email</label>
      <input id="email" class="input" value={email} readonly aria-readonly="true" />
      <span class="hint">This is the address your invitation was sent to.</span>
    </div>
    <div class="field">
      <label for="username">Username</label>
      <input id="username" class="input" bind:value={userName} autocomplete="username" required />
    </div>
    <div class="field">
      <label for="password">Password</label>
      <input id="password" type="password" class="input" bind:value={password} autocomplete="new-password" required />
      <span class="hint">At least 10 characters, including a digit and a lowercase letter.</span>
    </div>
    <div class="field">
      <label for="confirm">Confirm password</label>
      <input id="confirm" type="password" class="input" bind:value={confirm} autocomplete="new-password" required />
    </div>
    <button type="submit" class="btn btn-primary btn-block" disabled={busy}>Create account</button>
  </form>
{/if}
