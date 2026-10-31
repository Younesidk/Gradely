<script>
  import { groupScopes, GROUP_LABELS } from '../../lib/integrations.js';

  let { meta } = $props();
  const groups = $derived(groupScopes(meta.scopes));
</script>

<section class="card">
  <h2>How permissions work</h2>
  <p class="muted">Every API key and every connected app has its own permissions. Gradly checks them on the server for every single request, whatever the assistant asks for.</p>
  <div class="perm-explainer">
    <div>
      <h3><span class="tag">View</span></h3>
      <p class="small muted">Lets the integration read that kind of data. It can't change anything.</p>
    </div>
    <div>
      <h3><span class="tag tag-warn">Change</span></h3>
      <p class="small muted">Lets the integration create, edit and delete that kind of data on your behalf. Give it only to tools you trust.</p>
    </div>
  </div>
  <p class="small muted">Tip: start with <strong>Read-only</strong> and add “Change” permissions later if you need them. You can edit a key's permissions at any time; the change applies to its next request.</p>
</section>

<div class="table-wrap">
  <table class="table">
    <thead>
      <tr><th scope="col">Data</th><th scope="col">View</th><th scope="col">Change</th></tr>
    </thead>
    <tbody>
      {#each groups as g (g.group)}
        <tr>
          <td data-label="Data"><strong>{GROUP_LABELS[g.group] ?? g.group}</strong></td>
          <td data-label="View">{#if g.read}<div class="cell-title"><code>{g.read.scope}</code><span class="muted small">{g.read.description}</span></div>{:else}—{/if}</td>
          <td data-label="Change">{#if g.write}<div class="cell-title"><code>{g.write.scope}</code><span class="muted small">{g.write.description}</span></div>{:else}<span class="muted small">Read-only by nature</span>{/if}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
