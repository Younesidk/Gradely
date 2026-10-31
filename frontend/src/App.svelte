<script>
  import { onMount } from 'svelte';
  import { auth, loadUser } from './lib/state.svelte.js';
  import { match, navigate, route } from './lib/router.svelte.js';
  import AppLayout from './components/AppLayout.svelte';
  import AuthLayout from './components/AuthLayout.svelte';
  import Loading from './components/Loading.svelte';
  import ToastHost from './components/ToastHost.svelte';
  import ConfirmHost from './components/ConfirmHost.svelte';

  import Login from './pages/Login.svelte';
  import Register from './pages/Register.svelte';
  import ConfirmEmailChange from './pages/ConfirmEmailChange.svelte';
  import NotFound from './pages/NotFound.svelte';
  import AccessDenied from './pages/AccessDenied.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import Semesters from './pages/Semesters.svelte';
  import SemesterDetail from './pages/SemesterDetail.svelte';
  import Formulas from './pages/Formulas.svelte';
  import AccountSettings from './pages/AccountSettings.svelte';
  import AdminOverview from './pages/AdminOverview.svelte';
  import AdminUsers from './pages/AdminUsers.svelte';
  import AdminUserDetail from './pages/AdminUserDetail.svelte';
  import AdminUserSemester from './pages/AdminUserSemester.svelte';
  import AdminInvitations from './pages/AdminInvitations.svelte';

  // layout: 'auth' pages are public and centred; 'app' pages need a signed-in user; admin pages need an administrator.
  const routes = [
    { path: '/account/login', page: Login, layout: 'auth' },
    { path: '/account/register', page: Register, layout: 'auth' },
    { path: '/account/confirm-email-change', page: ConfirmEmailChange, layout: 'auth' },
    { path: '/', page: Dashboard, layout: 'app' },
    { path: '/semesters', page: Semesters, layout: 'app' },
    { path: '/semesters/:id', page: SemesterDetail, layout: 'app' },
    { path: '/formulas', page: Formulas, layout: 'app' },
    { path: '/settings', page: AccountSettings, layout: 'app' },
    { path: '/admin', page: AdminOverview, layout: 'app', admin: true },
    { path: '/admin/users', page: AdminUsers, layout: 'app', admin: true },
    { path: '/admin/users/:id', page: AdminUserDetail, layout: 'app', admin: true },
    { path: '/admin/users/:id/semesters/:sid', page: AdminUserSemester, layout: 'app', admin: true },
    { path: '/admin/invitations', page: AdminInvitations, layout: 'app', admin: true },
  ];

  onMount(loadUser);

  const current = $derived.by(() => {
    for (const r of routes) {
      const params = match(r.path, route.path);
      if (params) return { ...r, params };
    }
    return { page: NotFound, layout: 'auth', params: {} };
  });

  // Signed-out visitors are sent to sign in (and back afterwards).
  $effect(() => {
    if (auth.ready && !auth.user && current.layout === 'app') {
      navigate('/account/login?returnUrl=' + encodeURIComponent(route.path + location.search), { replace: true });
    }
  });
</script>

{#if !auth.ready}
  <Loading />
{:else if current.layout === 'auth'}
  <AuthLayout>
    {#key route.path}
      {@const Page = current.page}
      <Page params={current.params} />
    {/key}
  </AuthLayout>
{:else if auth.user}
  {#if current.admin && !auth.user.isAdmin}
    <AuthLayout><AccessDenied /></AuthLayout>
  {:else}
    <AppLayout>
      {#key route.path}
        {@const Page = current.page}
        <Page params={current.params} />
      {/key}
    </AppLayout>
  {/if}
{/if}

<ToastHost />
<ConfirmHost />
