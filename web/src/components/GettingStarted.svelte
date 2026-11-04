<script lang="ts">
  import { onMount, tick } from "svelte";
  import { GUIDE_TOPICS, SHORTCUTS } from "../lib/guideContent";
  import { splitHash } from "../lib/hashRoute";
  import Card from "./ui/Card.svelte";
  import PageHeader from "./ui/PageHeader.svelte";

  onMount(async () => {
    // Covers both CardHelp's "More in the guide" (this component mounts fresh on tab switch) and a cold-load
    // deep link: App.svelte's onMount only normalizes a hash that doesn't resolve to the current tab, so
    // "#/guide#margin-vs-markup" keeps its fragment.
    const anchor = splitHash(location.hash).fragment;
    if (!anchor) return;
    const heading = document.getElementById(anchor);
    if (!heading) return;
    // App.svelte's syncTabFromHash resets the <main> scroll after swapping tabs; wait a tick and a frame so
    // that reset lands first and this scroll wins.
    await tick();
    requestAnimationFrame(() => {
      heading.scrollIntoView({ block: "start" });
    });
  });
</script>

<div class="page measure-cap-prose guide-page">
  <PageHeader title="Guide" />

  <Card heading="Getting started">
    <ol class="steps">
      <li><strong>Pick a printer and material.</strong> Choose them in the Print box. Your printer and material settings fill in power draw, filament price, and waste.</li>
      <li><strong>Add the print job.</strong> Drop a sliced .gcode file on the Model file box to fill weight and time automatically, or type them in.</li>
      <li><strong>Set your costs.</strong> The Costs box holds your shop rates. Cost breakdown updates live as you type.</li>
      <li><strong>Set a price.</strong> Turn on Set a selling price in the Price and profit card to get a recommended price that accounts for margin, fees, shipping, and tax.</li>
      <li><strong>Save and share.</strong> Save the quote in the Saved quotes panel, open any of your last 5 saves, and print a client-ready copy.</li>
    </ol>
  </Card>

  <Card heading="Running the business">
    <ol class="steps">
      <li><strong>Link a client and save.</strong> In the Saved quotes panel on the New quote page, type a client name in the Client field next to the label field, then press Save quote.</li>
      <li><strong>Track its status.</strong> Every save shows up on the Quotes page with a status badge. Emailing a quote marks it sent automatically; use Mark sent, Mark accepted, or Mark declined to record moves you make outside printpaca. The row shows the most likely next status. The other status options, Email quote, Duplicate, and Delete are in the row's ⋯ (More actions) menu.</li>
      <li><strong>Email or print it.</strong> Press Email quote in the Quotes page row's ⋯ menu to send it to the client, or use Print client quote on the New quote page for a paper copy.</li>
      <li><strong>Convert it once accepted.</strong> Press Convert to invoice on the Quotes page to turn an accepted quote into an invoice on the Invoices page.</li>
      <li><strong>Record payment.</strong> Open the invoice on the Invoices page and log each payment as it comes in.</li>
      <li><strong>Watch the dashboard.</strong> The Dashboard page rolls up revenue, outstanding balances, and quote-to-invoice conversion so you can see where the business stands.</li>
    </ol>
  </Card>

  <Card heading="Concepts">
    <div class="topics">
      {#each GUIDE_TOPICS as topic (topic.id)}
        <section>
          <h3 id={topic.id}>{topic.title}</h3>
          <p>{topic.body}</p>
        </section>
      {/each}
    </div>
  </Card>

  <Card>
    <h2 id="keyboard-shortcuts">Keyboard shortcuts</h2>
    <p>The sidebar footer lists the shortcuts that work on the page you are on. Shortcuts need Ctrl (or Cmd on a Mac), so typing letters never triggers them. Ctrl+K does nothing while you are typing in a text box.</p>
    <table>
      <thead><tr><th scope="col">Keys</th><th scope="col">What it does</th><th scope="col">Where</th></tr></thead>
      <tbody>
        {#each SHORTCUTS as sc (sc.keys)}
          <tr><th scope="row">{sc.keys}</th><td>{sc.action}</td><td>{sc.where}</td></tr>
        {/each}
      </tbody>
    </table>
  </Card>

  <p class="footnote">
    On the New quote page, press the ? next to any card heading for an explanation of that card.
  </p>
</div>

<style>
  /* .measure-cap-prose (theme.css) only caps width; left un-centered it hugs the left edge on wide
   * viewports. This page is prose, not a form, so centering reads better. */
  .guide-page {
    margin-inline: auto;
  }

  .steps {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    margin: var(--space-3) 0 0;
    padding-left: var(--space-5);
  }

  .topics {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    margin: var(--space-3) 0 0;
  }
  .topics h3 { margin: 0 0 var(--space-1); }
  .topics p {
    margin: 0;
    color: var(--text);
  }

  .footnote {
    color: var(--muted);
    font-size: var(--text-sm);
    margin: 0;
  }
</style>
