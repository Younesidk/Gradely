<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { route } from '../lib/router.svelte.js';
  import Loading from '../components/Loading.svelte';

  let state = $state('working'); // working | ok | failed

  onMount(async () => {
    try {
      await api.post('/auth/confirm-email-change', { token: route.query.get('token') ?? '' });
      state = 'ok';
    } catch {
      state = 'failed';
    }
  });
</script>

<svelte:head><title>Confirm email · Gradely</title></svelte:head>

{#if state === 'working'}
  <Loading />
{:else if state === 'ok'}
  <h1 class="auth-title">Email address confirmed</h1>
  <div class="alert alert-success" role="status">Your email address has been updated.</div>
  <a class="btn btn-primary btn-block" href="/settings">Back to your account</a>
{:else}
  <h1 class="auth-title">Couldn't confirm the address</h1>
  <div class="alert alert-danger" role="alert">This confirmation link is invalid or has expired. Request a new one from Account settings.</div>
  <a class="btn btn-ghost btn-block" href="/account/login">Go to sign in</a>
{/if}
