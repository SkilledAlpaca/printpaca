<script lang="ts">
  import type { CostBreakdown, EffectiveConfig } from "../lib/engine/index";
  import { money, moneyPrecise } from "../lib/format";
  import { computeBreakdownSegments } from "../lib/breakdownBar";
  import { breakdownHint } from "../lib/quoteRow";
  import { TILE_HELP } from "../lib/guideContent";
  import CardHelp from "./CardHelp.svelte";

  let { breakdown, cfg, partName = "" }: { breakdown: CostBreakdown; cfg: EffectiveConfig; partName?: string } = $props();

  // Blank per line: only the row whose own field is in error goes "-"; a bad "Monthly print hours"
  // invalidates Overhead alone.
  function amount(cents: number, lineInvalid: boolean): string {
    return lineInvalid ? "-" : money(cents, cfg);
  }

  /** Hours to at most two decimals, no trailing zeros. */
  function formatHours(h: number): string {
    return String(Math.round(h * 100) / 100);
  }

  const segments = $derived(computeBreakdownSegments(breakdown));

  /** One plain sentence per cost line, keyed by the engine's line id, so "Depreciation" and "Overhead" mean
   * something to someone who has never costed a print. Hardware lines carry per-entry ids. */
  const LINE_HELP: Record<string, string> = {
    material: "Filament used, including waste",
    electricity: "Power the printer draws while printing",
    depreciation: "Printer wear: its price spread over its lifespan",
    failure: "Allowance for prints that fail and have to be redone",
    labor: "Your hands-on time to set up and finish the print",
    overhead: "Rent, software, and other monthly costs, shared across your print hours",
  };
  function lineHelp(id: string): string {
    return id.startsWith("hardware-") ? "Extra parts added to each unit, such as magnets or screws" : (LINE_HELP[id] ?? "");
  }

  // Empty-state hint from the shared breakdownHint (lib/quoteRow.ts), also used by QuoteTab's print buttons
  // and HistoryPanel's save row.
  const emptyHint = $derived(breakdownHint(breakdown));
</script>

<div class="card">
  <div class="head-row with-help">
    <h2>Cost breakdown{partName ? `: ${partName}` : ""}{breakdown.quantity > 1 ? " (per unit)" : ""}</h2>
    <CardHelp id="breakdown" title="Cost breakdown" text={TILE_HELP.breakdown} />
  </div>
  {#if segments.length > 0}
    <div class="bar" aria-hidden="true">
      {#each segments as seg (seg.id)}
        <div class="segment" data-id={seg.id} style={`flex: ${seg.percent} 0 0`}></div>
      {/each}
    </div>
    <ul class="legend">
      {#each segments as seg (seg.id)}
        <li><span class="chip" data-id={seg.id}></span>{seg.label} {seg.percent.toFixed(0)}%</li>
      {/each}
    </ul>
  {/if}
  <table class="measure-cap">
    <caption class="visually-hidden">Cost breakdown{breakdown.quantity > 1 ? " per unit" : ""}</caption>
    <tbody>
      {#each breakdown.perUnit as line (line.id)}
        {@const invalid = line.warnings.some((w) => w.severity === "error")}
        <tr class:muted={line.amount === 0}>
          <td>
            <details>
              <summary><span>{line.label}</span>{#if lineHelp(line.id)}<small class="line-help">{lineHelp(line.id)}</small>{/if}</summary>
              <code>{line.formula}</code>
              {#if line.periods && line.periods.length > 0}
                <ul class="periods" aria-label={breakdown.quantity > 1 ? "Electricity by tariff period, whole batch" : "Electricity by tariff period"}>
                  {#each line.periods as p (p.name)}
                    <li><span>{p.name}</span><span class="num">{formatHours(p.hours)} h · {moneyPrecise(p.cost, cfg, 3)}</span></li>
                  {/each}
                </ul>
                {#if breakdown.quantity > 1}<small class="muted">Period costs cover the whole batch of {breakdown.quantity}, printed back to back.</small>{/if}
              {/if}
            </details>
          </td>
          <td class="num">{amount(line.amount, invalid)}</td>
        </tr>
      {/each}
    </tbody>
    <tfoot>
      <tr>
        <th>Cost per unit</th>
        <th class="num">{breakdown.valid ? money(breakdown.perUnitTotal, cfg) : "-"}</th>
      </tr>
      {#if breakdown.quantity > 1}
        <tr>
          <th>Batch total (×{breakdown.quantity})</th>
          <th class="num">{breakdown.valid ? money(breakdown.batchTotal, cfg) : "-"}</th>
        </tr>
      {/if}
    </tfoot>
  </table>
  {#if breakdown.quantity > 1 && breakdown.valid}
    <p class="hint batch-note">
      The batch total is worked out for the whole batch and rounded once, so it can differ from the cost per unit
      times {breakdown.quantity} by a cent or two. The batch total is the one to use.
    </p>
  {/if}
  {#if !breakdown.valid}
    <p class="empty-state">{emptyHint}</p>
  {/if}
</div>

<style>
  /* table/.num baseline, head row and empty-state styles come from theme.css; only the breakdown-specific
   * layout is here. */

  tr.muted td { color: var(--muted); }
  details summary { cursor: pointer; }
  .line-help { display: block; margin: 0.1rem 0 0; color: var(--muted); font-size: var(--text-xs); }
  .batch-note { margin: 0.5rem 0 0; }
  details code { display: block; margin: 0.25rem 0 0; color: var(--muted); font-size: var(--text-sm); }
  tfoot th { font-size: var(--text-lg); }
  .periods { list-style: none; margin: 0.25rem 0 0; padding: 0; font-size: var(--text-sm); color: var(--muted); }
  .periods li { display: flex; justify-content: space-between; gap: 1rem; }

  .bar {
    display: flex;
    height: 0.6rem;
    border-radius: calc(var(--radius) / 2);
    overflow: hidden;
    background: var(--border);
    margin: 0.4rem 0;
  }
  .segment { min-width: 2px; }

  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem 0.8rem;
    list-style: none;
    padding: 0;
    margin: 0 0 0.4rem;
    font-size: var(--text-sm);
    color: var(--muted);
  }
  .legend li { display: flex; align-items: center; gap: 0.3rem; }
  .chip { width: 0.6rem; height: 0.6rem; border-radius: 2px; flex-shrink: 0; }

  /* The six fixed cost-line ids (engine LineItem.id), each a stable color from the theme's semantic tokens
   * via color-mix() so both palettes stay legible. */
  .segment[data-id="material"], .chip[data-id="material"] { background: var(--accent); }
  .segment[data-id="electricity"], .chip[data-id="electricity"] { background: var(--ok); }
  .segment[data-id="depreciation"], .chip[data-id="depreciation"] { background: var(--warn); }
  .segment[data-id="failure"], .chip[data-id="failure"] {
    background: color-mix(in srgb, var(--accent) 55%, var(--text));
  }
  .segment[data-id="labor"], .chip[data-id="labor"] {
    background: color-mix(in srgb, var(--ok) 55%, var(--muted));
  }
  .segment[data-id="overhead"], .chip[data-id="overhead"] {
    background: color-mix(in srgb, var(--warn) 55%, var(--muted));
  }
  /* Hardware lines carry per-entry ids ("hardware-0", ... from server/pricing.go's ComputeQuote), so a
   * prefix match gives them one stable color instead of leaving them invisible. */
  .segment[data-id^="hardware-"], .chip[data-id^="hardware-"] {
    background: color-mix(in srgb, var(--accent) 30%, var(--muted));
  }
</style>
