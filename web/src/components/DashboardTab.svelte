<script lang="ts">
  import { onMount } from "svelte";
  import type { EffectiveConfig } from "../lib/engine/index";
  import {
    fetchDashboard,
    fetchTopItems,
    optionsLine,
    attentionGroups,
    attentionTiles,
    attentionListHash,
    sourceSplit,
    TOP_ITEMS_WINDOWS,
    type AttentionItem,
    type AttentionKind,
    type Dashboard,
    type TopItems,
    type TopItemsWindow,
  } from "../lib/dashboardApi";
  import { money, formatDate } from "../lib/format";
  import { invoiceHash } from "../lib/hashRoute";
  import { extractConfigUser, hasTabAccess } from "../lib/permissions";
  import StatusBadge from "./StatusBadge.svelte";
  import { announce } from "../lib/a11y.svelte";
  import ScrollRegion from "./ScrollRegion.svelte";
  import PageHeader from "./ui/PageHeader.svelte";
  import Card from "./ui/Card.svelte";
  import StatTile from "./ui/StatTile.svelte";

  let { cfg }: { cfg: EffectiveConfig } = $props();

  let dashboard = $state<Dashboard | null>(null);
  let loading = $state(true);
  // A network or server failure shows a muted note instead of throwing (see fetchDashboard); the quote tiles
  // come from the same response, so there is no separate offline source.
  let unavailable = $state(false);

  const TOP_ITEMS_STORAGE_KEY = "printpaca.dashboard.topItemsDays";
  function topItemsDaysFromStorage(): TopItemsWindow {
    try {
      const v = Number(localStorage.getItem(TOP_ITEMS_STORAGE_KEY));
      return TOP_ITEMS_WINDOWS.find((w) => w === v) ?? 30;
    } catch {
      return 30;
    }
  }
  function persistTopItemsDays(d: TopItemsWindow) {
    try {
      localStorage.setItem(TOP_ITEMS_STORAGE_KEY, String(d));
    } catch {
      /* Safari private mode / quota: losing the remembered window isn't fatal. */
    }
  }

  let topItemsDays = $state<TopItemsWindow>(topItemsDaysFromStorage());
  let topItems = $state<TopItems | null>(null);
  let topItemsLoading = $state(true);
  let topItemsUnavailable = $state(false);
  // Bumped per request so a slow response for a window the user already left can't overwrite a newer one.
  let topItemsRequest = 0;

  async function loadTopItems(days: TopItemsWindow, announceResult = false) {
    const request = ++topItemsRequest;
    topItemsLoading = true;
    const result = await fetchTopItems(days);
    if (request !== topItemsRequest) return;
    topItems = result;
    topItemsUnavailable = result === null;
    topItemsLoading = false;
    if (announceResult) announce(
      result === null
        ? "Couldn't load top items."
        : `Top items updated for the last ${days} days: ${result.items.length} ${result.items.length === 1 ? "item" : "items"}.`,
      result === null ? "assertive" : "polite",
    );
  }

  function selectTopItemsDays(days: TopItemsWindow) {
    if (days === topItemsDays) return;
    topItemsDays = days;
    persistTopItemsDays(days);
    void loadTopItems(days, true);
  }

  onMount(() => {
    void loadTopItems(topItemsDays);
    (async () => {
      const dashboardResult = await fetchDashboard();
      if (dashboardResult === null) {
        unavailable = true;
      } else {
        dashboard = dashboardResult;
      }
      loading = false;
    })();
  });

  // Every row is a real link to the record, at the same hash the list tabs use for their own rows: the router
  // hands the id to the tab it mounts (App.svelte's routePendingOpen), so back/forward and "open in a new tab"
  // work like any other link.
  function recordHash(item: { kind: AttentionKind; id: string }): string {
    if (item.kind === "new-request" || item.kind === "print-time-request") return `#/requests#${item.id}`;
    if (item.kind === "sent-quote") return `#/quote/${encodeURIComponent(item.id)}`;
    return invoiceHash(item.id);
  }
  const ATTENTION_LABELS: Record<AttentionKind, string> = {
    "new-request": "New request",
    "print-time-request": "Needs a print time",
    "overdue-invoice": "Overdue invoice",
    "draft-invoice": "Draft invoice",
    "sent-quote": "Quote awaiting reply",
  };
  function attentionDetail(item: AttentionItem): string {
    switch (item.kind) {
      case "new-request":
        return `Received ${formatDate(item.date)}${item.amountCents > 0 ? ` · ${money(item.amountCents, cfg)}` : ""}`;
      case "print-time-request":
        return `Received ${formatDate(item.date)} · price to be confirmed`;
      case "overdue-invoice":
        return `${item.number} · ${money(item.amountCents, cfg)} due ${formatDate(item.date)}`;
      case "draft-invoice":
        return `${item.number} · ${money(item.amountCents, cfg)} · not sent yet`;
      case "sent-quote":
        return `Sent ${formatDate(item.date)}${item.amountCents > 0 ? ` · ${money(item.amountCents, cfg)}` : ""}`;
    }
  }
  // The five action tiles, in a fixed order. `tab` is the page the tile's records live on: a tile
  // for a page this user can't open (or that Settings turned off) isn't shown, and the server leaves those
  // rows out of the response anyway.
  const configUser = $derived(extractConfigUser(cfg));
  const tiles = $derived(
    dashboard
      ? attentionTiles(dashboard.attentionCounts, (tab) => !cfg.ui.disabledTabs.includes(tab) && hasTabAccess(tab, configUser))
      : [],
  );
  const totalAttention = $derived(tiles.reduce((sum, t) => sum + t.value, 0));

  // null is "All", the default. Clicking the pressed tile goes back to All.
  let selectedKind = $state<AttentionKind | null>(null);
  function selectKind(kind: AttentionKind | null) {
    selectedKind = kind === selectedKind ? null : kind;
    if (!dashboard) return;
    const tile = tiles.find((t) => t.kind === selectedKind);
    announce(
      tile ? `Showing ${tile.label.toLowerCase()}: ${tile.value}.` : `Showing everything that needs attention: ${totalAttention}.`,
      "polite",
    );
  }

  // Rows grouped in display order (the server already sorts them), each group with a "View all N" link when
  // the list is cut short. A selected tile narrows this to its own kind.
  const attentionGrouped = $derived.by(() => {
    if (!dashboard) return [];
    const visible = new Set(tiles.map((t) => t.kind));
    return attentionGroups(dashboard.attentionCounts)
      .filter((g) => visible.has(g.kind) && (selectedKind === null || g.kind === selectedKind))
      .map((g) => ({ ...g, items: dashboard!.needsAttention.filter((i) => i.kind === g.kind) }))
      .filter((g) => g.items.length > 0);
  });

  const MONTH_LABELS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  function monthLabel(month: string): string {
    const idx = Number(month.split("-")[1]) - 1;
    return MONTH_LABELS[idx] ?? month;
  }
  function monthWithYear(month: string): string {
    return `${monthLabel(month)} ${month.split("-")[0]}`;
  }

  // Chart geometry: a fixed viewBox gives a stable aspect ratio while CSS makes the <svg> responsive.
  const CHART_WIDTH = 480;
  const CHART_HEIGHT = 160;
  const PAD_X = 20;
  const BAR_AREA_TOP = 12;
  const BASELINE_Y = 118;
  const LABEL_Y = 140;
  const BAR_GAP = 8;

  const months = $derived(dashboard?.revenueByMonth ?? []);
  const barWidth = $derived(
    months.length > 0 ? (CHART_WIDTH - 2 * PAD_X - BAR_GAP * (months.length - 1)) / months.length : 0,
  );
  // At least 1 so an all-zero series (no payments yet) divides cleanly; every bar then has height 0.
  const maxCents = $derived(Math.max(1, ...months.map((m) => m.cents)));

  /** The smallest "round" number >= v: 1, 2, 2.5 or 5 times a power of ten. Scaling to the raw peak put
   * gridlines at 50% and 100% of it ("$296.50 / $593.00"), restating the peak instead of a measurable
   * scale; rounding up gives "$300 / $600". */
  function niceCeil(v: number): number {
    if (!(v > 0)) return 1;
    const base = Math.pow(10, Math.floor(Math.log10(v)));
    for (const m of [1, 2, 2.5, 5]) {
      if (v <= m * base) return m * base;
    }
    return 10 * base;
  }
  // The value the top of the plot area represents; bars and gridlines share it so a gridline lines up with
  // the bar that reaches it.
  const axisMax = $derived(niceCeil(maxCents));

  function barX(i: number): number {
    return PAD_X + i * (barWidth + BAR_GAP);
  }
  function barHeight(cents: number): number {
    if (cents <= 0) return 0;
    // A tiny nonzero month still renders a visible sliver; its presence matters as much as its exact height
    // at this scale.
    return Math.max(2, (cents / axisMax) * (BASELINE_Y - BAR_AREA_TOP));
  }

  // Two gridlines at 50% and 100% of axisMax, using barHeight's value-to-y mapping.
  function gridY(fraction: number): number {
    return BASELINE_Y - fraction * (BASELINE_Y - BAR_AREA_TOP);
  }
  // Gridlines and labels are meaningless with no revenue: maxCents is clamped to 1 for dividing, but a
  // "$0.01" axis would misstate the data.
  const hasRevenue = $derived(months.some((m) => m.cents > 0));
  // Text alternative for the chart (WCAG 1.1.1): the range and total in the label, every month in the table.
  const revenueTotalCents = $derived(months.reduce((sum, m) => sum + m.cents, 0));
  const chartLabel = $derived(
    months.length > 0
      ? `Revenue by month, ${monthWithYear(months[0]!.month)} to ${monthWithYear(months[months.length - 1]!.month)}, total ${money(revenueTotalCents, cfg)}. A table of the same data follows.`
      : "Revenue by month",
  );
  // The one bar that gets the peak label: the first month at maxCents with index > 1, else -1. A single index
  // avoids labeling every tied month; indices 0-1 are skipped because the 100% gridline label at x=PAD_X
  // would overlap.
  const peakIndex = $derived(months.findIndex((m, i) => m.cents > 0 && m.cents === maxCents && i > 1));
  const gridlines = $derived([
    { y: gridY(0.5), cents: Math.round(axisMax * 0.5) },
    { y: gridY(1), cents: axisMax },
  ]);
</script>

<div class="page">
  <PageHeader title="Dashboard" />

  {#if loading}
    <p role="status">Loading…</p>
  {:else}
    {#if unavailable}
      <p class="banner-warn" role="alert">Couldn't load the dashboard. The server couldn't be reached.</p>
    {/if}

    {#if dashboard}
    <div class="tiles" role="group" aria-label="Filter the to-do list">
      {#each tiles as t (t.kind)}
        <StatTile label={t.label} value={t.value} caption={t.caption} pressed={selectedKind === t.kind} onclick={() => selectKind(t.kind)} />
      {/each}
    </div>

    <Card as="section" aria-labelledby="attention-heading">
      <div class="todo-head">
        <h2 id="attention-heading">Needs attention</h2>
        <div class="chips">
          <button type="button" class:active={selectedKind === null} aria-pressed={selectedKind === null} onclick={() => selectKind(null)}>All ({totalAttention})</button>
        </div>
      </div>
      {#if attentionGrouped.length === 0}
        <p class="empty-state">Nothing needs attention.</p>
      {:else}
        <ul class="plain-list">
          {#each attentionGrouped as g (g.kind)}
            {#each g.items as item (`${item.kind}-${item.id}`)}
              <li>
                <a class="row-link attention-row" href={recordHash(item)}>
                  <span class="attention-kind" class:overdue={item.kind === "overdue-invoice"}>{ATTENTION_LABELS[item.kind]}</span>
                  <span class="attention-name">{item.name}</span>
                  <span class="attention-detail">{attentionDetail(item)}</span>
                </a>
              </li>
            {/each}
            {#if g.count > g.items.length}
              <li class="attention-more"><a href={attentionListHash(g.kind)}>View all {g.count} {g.noun}</a></li>
            {/if}
          {/each}
        </ul>
      {/if}
    </Card>

    <section class="month" aria-labelledby="month-heading">
      <h2 id="month-heading">This month</h2>
      <div class="tiles">
        <StatTile label="Revenue" value={money(dashboard.revenueMonthCents, cfg)} period="This month" href="#/invoices" />
        <StatTile label="Outstanding" value={money(dashboard.outstandingCents, cfg)} period="Unpaid invoices" href="#/invoices" />
        <StatTile
          label="Average margin"
          value={dashboard.quotesAvgMarginPctMonth !== null ? `${dashboard.quotesAvgMarginPctMonth.toFixed(0)}%` : "-"}
          period="This month"
          caption="Quotes invoiced"
          href="#/quotes"
        />
        <StatTile
          label="Quotes saved"
          value={dashboard.quotesSavedThisMonth}
          period="This month"
          caption={dashboard.quotesInvoicedSharePct !== null ? `${dashboard.quotesInvoicedSharePct.toFixed(0)}% invoiced` : undefined}
          href="#/quotes"
        />
      </div>
    </section>

    <div class="cards">
      <Card>
        <h2>Revenue by month</h2>
        {#if hasRevenue}
          <svg
            class="revenue-chart"
            viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
            role="img"
            aria-label={chartLabel}
          >
            {#each gridlines as g (g.y)}
              <line x1={PAD_X} y1={g.y} x2={CHART_WIDTH - PAD_X} y2={g.y} class="gridline" />
            {/each}
            <line x1={PAD_X} y1={BASELINE_Y} x2={CHART_WIDTH - PAD_X} y2={BASELINE_Y} class="baseline" />
            {#each months as m, i (m.month)}
              <rect
                x={barX(i)}
                y={BASELINE_Y - barHeight(m.cents)}
                width={barWidth}
                height={barHeight(m.cents)}
                rx="2"
                class="bar"
              ><title>{monthLabel(m.month)}: {money(m.cents, cfg)}</title></rect>
              {#if i === peakIndex}
                <text
                  x={barX(i) + barWidth / 2}
                  y={BASELINE_Y - barHeight(m.cents) - 3}
                  text-anchor="middle"
                  class="bar-value-label"
                >{money(m.cents, cfg)}</text>
              {/if}
              <text x={barX(i) + barWidth / 2} y={LABEL_Y} text-anchor="middle" class="axis-label" class:alt={i % 2 === 1}>{monthLabel(m.month)}</text>
            {/each}
            <!-- Painted after the bars so a tall bar never covers a gridline's value label. -->
            {#each gridlines as g (g.y)}
              <text x={PAD_X} y={g.y - 3} text-anchor="start" class="grid-label">{money(g.cents, cfg)}</text>
            {/each}
          </svg>
          <details class="chart-data">
            <summary>Revenue by month as a table</summary>
            <ScrollRegion label="Revenue by month table">
              <table>
                <thead>
                  <tr><th scope="col">Month</th><th scope="col" class="num">Revenue</th></tr>
                </thead>
                <tbody>
                  {#each months as m (m.month)}
                    <tr><th scope="row">{monthWithYear(m.month)}</th><td class="num">{money(m.cents, cfg)}</td></tr>
                  {/each}
                </tbody>
              </table>
            </ScrollRegion>
          </details>
        {:else}
          <p class="empty-state">No revenue yet. Convert a quote to an invoice and record a payment to see revenue here.</p>
        {/if}
        <p class="year-total">Revenue this year: <a href="#/invoices">{money(dashboard.revenueYearCents, cfg)}</a></p>
        {#if dashboard.quotesAvgMarginPct !== null}
          <p class="year-total">Average margin, all invoiced quotes: {dashboard.quotesAvgMarginPct.toFixed(0)}%</p>
        {/if}
      </Card>

      <Card class="recent-card">
        <h2>Recent invoices</h2>
        {#if dashboard.recentInvoices.length === 0}
          <p class="empty-state">No invoices yet. Convert a saved quote on the Quotes page, or create one on the Invoices page.</p>
        {:else}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <div class="recent-scroll" tabindex="0" role="region" aria-label="Recent invoices">
          <ul class="plain-list">
            {#each dashboard.recentInvoices as inv (inv.id)}
              <li>
                <a class="row-link inv-row" href={invoiceHash(inv.id)}>
                  <span class="inv-number">{inv.number}</span>
                  <span class="inv-total">{money(inv.totalCents, cfg)}</span>
                  <span class="inv-client">{inv.clientName}{#if inv.issueDate || inv.createdAt} · <time datetime={inv.issueDate || inv.createdAt}>{formatDate(inv.issueDate || inv.createdAt)}</time>{/if}</span>
                  <span class="inv-status"><StatusBadge status={inv.status} dueDate={inv.dueDate} /></span>
                </a>
              </li>
            {/each}
          </ul>
          </div>
        {/if}
      </Card>
    </div>
    {/if}

    <Card>
      <div class="top-items-head">
        <h2>Top items</h2>
        <div class="chips" role="group" aria-label="Top items window">
          {#each TOP_ITEMS_WINDOWS as days (days)}
            <button
              type="button"
              class:active={topItemsDays === days}
              aria-pressed={topItemsDays === days}
              onclick={() => selectTopItemsDays(days)}
            >Last {days} days</button>
          {/each}
        </div>
      </div>

      <p class="hint">
        Booked sales, not payments received. Finalized shop orders count, and so do invoice lines linked to a shop item once the invoice is sent or paid, dated by its issue date. Deleting a request doesn't remove its sale.
      </p>

      {#if topItemsLoading && topItems === null}
        <p class="empty-state" role="status">Loading…</p>
      {:else if topItemsUnavailable}
        <p class="banner-warn" role="alert">Couldn't load top items. The server couldn't be reached.</p>
      {:else if topItems}
        {#if topItems.items.length === 0}
          <p class="empty-state">No shop-order or in-person item sales in the last {topItems.days} days.</p>
        {:else}
          <p class="top-items-summary">
            {topItems.totalUnits} {topItems.totalUnits === 1 ? "unit" : "units"} sold,
            {money(topItems.totalRevenueCents, cfg)} booked in the last {topItems.days} days.
          </p>
          <p class="top-items-summary">
            {sourceSplit(topItems.galleryUnits, topItems.inPersonUnits)}
          </p>
          <p class="top-items-summary">Booked is the line amount less its share of any invoice discount, before tax. It is not payments received.</p>
          <ScrollRegion label="Top items table">
            <table>
              <thead>
                <tr>
                  <th class="num">Rank</th>
                  <th>Item</th>
                  <th class="num">Units</th>
                  <th class="num">Orders</th>
                  <th class="num">Booked</th>
                </tr>
              </thead>
              <tbody>
                {#each topItems.items as it, i (it.itemId)}
                  <tr>
                    <td class="num">{i + 1}</td>
                    <td>
                      {it.name}{#if it.removed} <span class="muted-note">(removed)</span>{/if}
                      {#if it.galleryUnits > 0 && it.inPersonUnits > 0}
                        <div class="muted-note">{sourceSplit(it.galleryUnits, it.inPersonUnits)}</div>
                      {/if}
                      {#if it.options.length > 0}
                        <div class="muted-note">{optionsLine(it.options)}</div>
                      {/if}
                    </td>
                    <td class="num">{it.units}</td>
                    <td class="num">{it.orders}</td>
                    <td class="num">{money(it.revenueCents, cfg)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </ScrollRegion>
        {/if}
      {/if}
    </Card>
  {/if}
</div>

<style>
  /* Tile rows: as many columns as fit at the small-field width, sharing the row evenly. */
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(var(--field-s), 1fr));
    gap: var(--gap);
  }
  .month h2, .todo-head h2 { margin: 0; }
  .month .tiles { margin-top: var(--space-3); }
  .todo-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2) var(--space-4); margin-bottom: var(--space-2); }
  .attention-more { padding: var(--space-1) var(--space-3) var(--space-2); font-size: var(--text-sm); }

  /* The chart and recent-invoices cards sit side by side from 900px (the app's one desktop breakpoint), the chart
   * taking the wider share; below that they stack. */
  .cards {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--gap);
    /* Each card ends where its content ends; stretched, the shorter card (usually the chart, whose SVG
     * can't use extra height) gained empty space below the axis labels. */
    align-items: start;
  }
  @media (min-width: 900px) {
    .cards { grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); align-items: stretch; }
    /* The Revenue by month card sets the row height; the Recent invoices card matches it and scrolls its
     * list inside. contain: size keeps the list's own height out of the row's sizing. The min-height keeps a
     * usable list when the chart card is short (no revenue yet). */
  }
  /* Recent invoices: a flex column so the list region takes the card's remaining height. On one column the
   * region is capped at about six two-line rows and scrolls. */
  .cards :global(.recent-card) { display: flex; flex-direction: column; }
  .recent-scroll {
    overflow-y: auto;
    min-height: 0;
    flex: 1 1 auto;
    max-height: 26.4rem; /* six two-line rows */
    scrollbar-gutter: stable;
  }
  @media (min-width: 900px) {
    .recent-scroll { max-height: none; }
    .cards :global(.recent-card) { contain: size; min-height: calc(var(--control-h) * 7); }
  }
  .year-total { margin: var(--space-3) 0 0; color: var(--muted); font-size: var(--text-sm); font-variant-numeric: tabular-nums; }

  .revenue-chart { width: 100%; height: auto; display: block; }
  .baseline { stroke: var(--border); stroke-width: 1; }
  .gridline { stroke: var(--border); stroke-width: 1; stroke-dasharray: 2 2; }
  .bar { fill: var(--accent); }
  .bar:hover { opacity: 0.8; }
  /* SVG chart text sizes are pinned in px, a deliberate exception to the --text-* scale: they live in the
   * viewBox coordinate system, not the rem layout. */
  .axis-label {
    fill: var(--muted);
    font-size: 9px;
    font-variant-numeric: tabular-nums;
  }
  /* Narrow screens scale the chart down, so every other month label is hidden to keep them apart; the
   * "Revenue by month table" below still lists all of them. */
  @media (max-width: 600px) {
    .axis-label.alt { display: none; }
  }
  .grid-label {
    fill: var(--muted);
    font-size: 9px;
    font-variant-numeric: tabular-nums;
  }
  .bar-value-label {
    fill: var(--text);
    font-size: 8px;
    font-variant-numeric: tabular-nums;
  }

  .top-items-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2) var(--space-4); }
  .top-items-head h2 { margin: 0; }
  .top-items-summary { color: var(--muted); font-size: var(--text-sm); font-variant-numeric: tabular-nums; margin: var(--space-2) 0 0; }
  .muted-note { color: var(--muted); font-size: var(--text-sm); }

  li { border-top: 1px solid var(--border); }
  li:first-child { border-top: none; }

  /* Two-line rows: number and amount on the first line, client and status on the second. The old single
   * line gave the amount its own non-shrinking column, so in the narrow recent-invoices card it outgrew the
   * row and the card clipped it. Here each line is a two-track grid whose first track (minmax(0, 1fr)) is
   * the only one that gives way, and the card itself can't be pushed wider (min-width: 0). */
  .row-link {
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--space-1) var(--space-3);
    padding: var(--space-2) 0;
    color: inherit;
    text-decoration: none;
    min-height: var(--control-h);
  }
  .row-link:hover { background: var(--surface-2); }
  .inv-number { font-weight: 600; white-space: nowrap; }
  .inv-total { font-variant-numeric: tabular-nums; text-align: right; white-space: nowrap; font-weight: 600; }
  /* Wraps instead of truncating (WCAG 1.4.12 text spacing, 1.4.10 reflow): the name keeps a readable floor. */
  .inv-client { min-width: 0; overflow-wrap: anywhere; color: var(--muted); font-size: var(--text-sm); }
  .inv-status { text-align: right; }

  .attention-row { grid-template-columns: 10rem minmax(0, 1fr); }
  .attention-kind { font-size: var(--text-sm); font-weight: 600; }
  .attention-kind.overdue { color: var(--danger); }
  .attention-name { font-weight: 600; min-width: 0; overflow-wrap: anywhere; }
  .attention-detail { grid-column: 2; color: var(--muted); font-size: var(--text-sm); min-width: 0; overflow-wrap: anywhere; }
  @media (max-width: 520px) {
    .attention-row { grid-template-columns: minmax(0, 1fr); }
    .attention-detail { grid-column: 1; }
  }
  .chart-data { margin-top: var(--space-2); color: var(--muted); font-size: var(--text-sm); }
  .chart-data summary { cursor: pointer; }
  .chart-data table { color: var(--text); }
</style>
