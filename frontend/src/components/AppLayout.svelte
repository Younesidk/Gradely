<script>
  import { auth, signOut, toggleTheme } from '../lib/state.svelte.js';
  import { isActive, route } from '../lib/router.svelte.js';
  import Icon from './Icon.svelte';

  let { children } = $props();
  let menuOpen = $state(false);

  // Close the mobile menu whenever the page changes.
  $effect(() => {
    route.path;
    menuOpen = false;
  });

  const links = [
    { href: '/', label: 'Dashboard', icon: 'dashboard', exact: true },
    { href: '/semesters', label: 'Semesters', icon: 'semesters' },
    { href: '/formulas', label: 'Formula settings', icon: 'formula' },
    { href: '/settings', label: 'Account settings', icon: 'account' },
  ];
  const adminLinks = [
    { href: '/admin', label: 'Overview', icon: 'shield', exact: true },
    { href: '/admin/users', label: 'Users', icon: 'users' },
    { href: '/admin/invitations', label: 'Invitations', icon: 'mail' },
  ];
</script>

<div class="app-shell {menuOpen ? 'menu-open' : ''}">
  <aside class="sidebar" id="sidebar" aria-label="Sidebar">
    <a class="brand" href="/" aria-label="Gradely home">
      <svg class="brand-mark" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M2 13h16" /><path d="M12 7v10" /></svg>
      <span class="brand-text">Gradely</span>
    </a>

    <nav class="nav" aria-label="Main navigation">
      {#each links as l}
        <a class="nav-link {isActive(l.href, l.exact) ? 'active' : ''}" href={l.href}><Icon name={l.icon} />{l.label}</a>
      {/each}

      {#if auth.user?.isAdmin}
        <div class="nav-section">Administration</div>
        {#each adminLinks as l}
          <a class="nav-link {isActive(l.href, l.exact) ? 'active' : ''}" href={l.href}><Icon name={l.icon} />{l.label}</a>
        {/each}
      {/if}
    </nav>

    <div class="sidebar-foot">
      {#if auth.user}
        <div class="who" title={auth.user.userName}>
          <span class="who-name">{auth.user.userName}</span>
          <span class="who-role">{auth.user.isAdmin ? 'Administrator' : 'Student'}</span>
        </div>
        <button type="button" class="btn btn-ghost btn-block btn-sm" onclick={() => signOut()}>
          <Icon name="logout" size={16} />Sign out
        </button>
      {/if}
    </div>
  </aside>
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="scrim" onclick={() => (menuOpen = false)}></div>

  <div class="main">
    <header class="topbar">
      <button type="button" class="icon-btn menu-btn" aria-label="Open navigation" aria-controls="sidebar"
        aria-expanded={menuOpen} onclick={() => (menuOpen = !menuOpen)}>
        <Icon name="menu" size={18} />
      </button>
      <a class="topbar-brand" href="/">Gradely</a>
      <div class="spacer"></div>
      <button type="button" class="icon-btn theme-btn" aria-label="Toggle light / dark theme" title="Toggle theme" onclick={toggleTheme}>
        <span class="theme-sun"><Icon name="sun" size={18} /></span>
        <span class="theme-moon"><Icon name="moon" size={18} /></span>
      </button>
    </header>

    <main class="content" id="main">
      {@render children?.()}
    </main>
  </div>
</div>
