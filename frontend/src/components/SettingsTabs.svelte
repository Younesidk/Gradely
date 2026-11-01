<script>
  import { route } from '../lib/router.svelte.js';

  const tabs = [
    { href: '/settings', label: 'Account' },
    { href: '/settings/integrations', label: 'Developer integrations' },
  ];
</script>

<nav class="tabs" aria-label="Settings sections">
  {#each tabs as t}
    <a class="tab {route.path === t.href ? 'active' : ''}" href={t.href} aria-current={route.path === t.href ? 'page' : undefined}>{t.label}</a>
  {/each}
</nav>
