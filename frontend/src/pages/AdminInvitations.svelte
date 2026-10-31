<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { toast, confirmDialog, copyText } from '../lib/state.svelte.js';
  import { date } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  let rows = $state(null);
  let emailConfigured = $state(false);
  let issued = $state(null); // { invitation, url, emailSent, emailError }
  let email = $state('');
  let busy = $state(false);

  async function load() {
    const r = await api.get('/admin/invitations');
    rows = r.items;
    emailConfigured = r.emailConfigured;
  }
  onMount(load);

  const issuedMessage = $derived.by(() => {
    if (!issued) return '';
    if (issued.emailSent === true) return 'The invitation email was sent. You can also share this link yourself.';
    if (issued.emailSent === false) return `The email couldn't be sent (${issued.emailError}). Share this link manually.`;
    return "Email delivery isn't configured — share this link with the invitee.";
  });

  async function run(action) {
    busy = true;
    try {
      await action();
      await load();
    } catch (err) {
      toast.error(err.message);
    } finally {
      busy = false;
    }
  }

  const create = (e) => {
    e.preventDefault();
    return run(async () => {
      issued = await api.post('/admin/invitations', { email });
      email = '';
      toast.success(issued.emailSent === true ? 'Invitation created and emailed' : 'Invitation created');
    });
  };

  const regenerate = (r) =>
    run(async () => {
      issued = await api.post(`/admin/invitations/${r.id}/regenerate`);
      toast.success('New invitation link generated. The previous link no longer works.');
    });

  async function revoke(r) {
    if (!(await confirmDialog(`Revoke the invitation for ${r.email}?`, 'The link will stop working immediately.', 'Revoke invitation'))) return;
    await run(async () => {
      await api.post(`/admin/invitations/${r.id}/revoke`);
      if (issued?.invitation.id === r.id) issued = null;
      toast.success('Invitation revoked');
    });
  }

  async function copy(text) {
    if (await copyText(text)) toast.success('Link copied to clipboard');
    else toast.info("Couldn't access the clipboard — select the link and copy it manually");
  }

  const tone = (s) => ({ Pending: 'warn', Redeemed: 'success', Expired: 'neutral' })[s] ?? 'danger-soft';
  const when = (r) =>
    ({
      Redeemed: 'Used ' + date(r.redeemedAt),
      Revoked: 'Revoked ' + date(r.revokedAt),
      Expired: 'Expired ' + date(r.expiresAt),
    })[r.status] ?? 'Expires ' + date(r.expiresAt);
</script>

<svelte:head><title>Invitations · Gradely</title></svelte:head>

<div class="page-head">
  <div>
    <h1>Invitations</h1>
    <p class="muted">Gradely is invitation-only. Links are single-use and expire after seven days.</p>
  </div>
</div>

<section class="card">
  <h2>Invite someone</h2>
  <form class="form form-inline" onsubmit={create}>
    <div class="field grow">
      <label for="inv-email">Email address</label>
      <input id="inv-email" type="email" class="input" bind:value={email} placeholder="student@university.edu" autocomplete="off" required />
    </div>
    <button type="submit" class="btn btn-primary" disabled={busy}><Icon name="mail" size={16} />Create invitation</button>
  </form>
  <p class="muted small">
    {#if emailConfigured}
      <span>Email delivery is <strong>on</strong>: the invitation is also sent by email.</span>
    {:else}
      <span>Email delivery is <strong>not configured</strong> (set <code>RESEND_API_KEY</code> and <code>RESEND_FROM_EMAIL</code>). Copy the link and send it yourself.</span>
    {/if}
  </p>

  {#if issued}
    <div class="callout callout-success" role="status">
      <strong>Invitation link for {issued.invitation.email}</strong>
      <p class="small">{issuedMessage}</p>
      <div class="link-box">
        <input class="input mono" readonly value={issued.url} aria-label="Invitation link" id="issued-link" onfocus={(e) => e.currentTarget.select()} />
        <button type="button" class="btn btn-primary btn-sm" onclick={() => copy(issued.url)}><Icon name="copy" size={15} />Copy</button>
      </div>
      <p class="small muted">This is the only time the full link is shown — only a hash is stored. Lost it? Use “New link” below.</p>
    </div>
  {/if}
</section>

<div class="section-title"><h2>All invitations</h2></div>
{#if rows === null}
  <Loading />
{:else if rows.length === 0}
  <EmptyState icon="mail" title="No invitations yet" text="Invite your first user using the form above." />
{:else}
  <div class="table-wrap">
    <table class="table">
      <thead>
        <tr>
          <th scope="col">Email</th><th scope="col">Status</th><th scope="col">Created</th>
          <th scope="col">Expires / used</th><th scope="col">Email sent</th><th scope="col"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as r (r.id)}
          <tr>
            <td data-label="Email">{r.email}</td>
            <td data-label="Status"><span class="badge badge-{tone(r.status)}">{r.status}</span></td>
            <td data-label="Created">{date(r.createdAt)}</td>
            <td data-label="Expires / used">{when(r)}</td>
            <td data-label="Email sent">{r.emailSentAt ? date(r.emailSentAt) : '—'}</td>
            <td class="row-actions">
              {#if r.status === 'Pending' || r.status === 'Expired'}
                <button type="button" class="btn btn-ghost btn-sm" onclick={() => regenerate(r)}>{r.status === 'Expired' ? 'Regenerate' : 'New link'}</button>
              {/if}
              {#if r.status === 'Pending'}
                <button type="button" class="btn btn-ghost btn-sm text-danger" onclick={() => revoke(r)}>Revoke</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
