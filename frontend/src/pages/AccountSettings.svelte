<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { auth, toast, signOut } from '../lib/state.svelte.js';
  import { date } from '../lib/format.js';
  import { navigate } from '../lib/router.svelte.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';

  let info = $state(null);
  let busy = $state(false);
  let manualLink = $state(null);
  let userName = $state('');
  let email = $state('');
  let current = $state('');
  let next = $state('');
  let repeat = $state('');

  onMount(async () => {
    info = await api.get('/account');
    userName = info.userName;
  });

  async function run(action) {
    busy = true;
    try {
      await action();
    } catch (err) {
      toast.error(err.message);
    } finally {
      busy = false;
    }
  }

  const saveUserName = (e) => {
    e.preventDefault();
    return run(async () => {
      const r = await api.put('/account/username', { userName });
      info.userName = r.userName;
      auth.user = { ...auth.user, userName: r.userName };
      toast.success('Username updated');
    });
  };

  const requestEmailChange = (e) => {
    e.preventDefault();
    return run(async () => {
      const r = await api.post('/account/email-change', { email });
      manualLink = r.manualLink;
      if (r.emailSent) toast.success('Check your inbox — we sent a confirmation link to the new address');
      else toast.info("Email delivery isn't configured, so the link is shown below");
      email = '';
    });
  };

  const changePassword = (e) => {
    e.preventDefault();
    if (next !== repeat) {
      toast.error("The new passwords don't match");
      return;
    }
    return run(async () => {
      await api.put('/account/password', { current, next });
      auth.user = null;
      navigate('/account/login?notice=password-changed', { replace: true });
    });
  };
</script>

<svelte:head><title>Account settings · Gradely</title></svelte:head>

<div class="page-head">
  <div>
    <h1>Account settings</h1>
    <p class="muted">Your sign-in details</p>
  </div>
</div>

{#if info === null}
  <Loading />
{:else}
  <div class="settings-grid">
    <section class="card">
      <h2>Account</h2>
      <dl class="facts facts-stack">
        <div><dt>Username</dt><dd>{info.userName}</dd></div>
        <div>
          <dt>Email</dt>
          <dd>{info.email} {#if info.emailConfirmed}<span class="tag tag-success">Verified</span>{:else}<span class="tag tag-warn">Not verified</span>{/if}</dd>
        </div>
        <div><dt>Member since</dt><dd>{date(info.createdAt)}</dd></div>
        <div><dt>Role</dt><dd>{info.isAdmin ? 'Administrator' : 'Student'}</dd></div>
      </dl>
      <button type="button" class="btn btn-ghost" onclick={() => signOut()}><Icon name="logout" size={16} />Sign out</button>
    </section>

    <section class="card">
      <h2>Username</h2>
      <form class="form" onsubmit={saveUserName}>
        <div class="field">
          <label for="new-username">New username</label>
          <input id="new-username" class="input" bind:value={userName} autocomplete="username" />
        </div>
        <button type="submit" class="btn btn-primary" disabled={busy}>Change username</button>
      </form>
    </section>

    <section class="card">
      <h2>Email address</h2>
      <p class="muted small">We'll send a confirmation link to the new address. Your email only changes after you open it.</p>
      <form class="form" onsubmit={requestEmailChange}>
        <div class="field">
          <label for="new-email">New email address</label>
          <input id="new-email" type="email" class="input" bind:value={email} autocomplete="email" />
        </div>
        <button type="submit" class="btn btn-primary" disabled={busy}>Send confirmation</button>
      </form>
      {#if manualLink}
        <div class="callout callout-info">
          <strong>Email delivery isn't configured on this server.</strong>
          Open this link to confirm the new address:
          <div class="link-box"><a href={manualLink}>{manualLink}</a></div>
        </div>
      {/if}
    </section>

    <section class="card">
      <h2>Password</h2>
      <form class="form" onsubmit={changePassword}>
        <div class="field">
          <label for="cur-pw">Current password</label>
          <input id="cur-pw" type="password" class="input" bind:value={current} autocomplete="current-password" />
        </div>
        <div class="field">
          <label for="new-pw">New password</label>
          <input id="new-pw" type="password" class="input" bind:value={next} autocomplete="new-password" />
          <span class="hint">At least 10 characters, including a digit and a lowercase letter.</span>
        </div>
        <div class="field">
          <label for="rep-pw">Repeat new password</label>
          <input id="rep-pw" type="password" class="input" bind:value={repeat} autocomplete="new-password" />
        </div>
        <button type="submit" class="btn btn-primary" disabled={busy}>Change password</button>
        <p class="muted small">You'll be signed out of all devices and asked to sign in again.</p>
      </form>
    </section>
  </div>
{/if}
