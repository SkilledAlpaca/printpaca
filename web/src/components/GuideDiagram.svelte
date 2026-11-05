<script lang="ts">
  import { ENV_ONLY_ROWS, OTHER_PAGE_ROWS, RECORD_FLOW, SAVE_BRANCHES, SAVE_MAIN, SETTINGS_PAGE_ROWS, type FlowBox } from "../lib/guideDiagrams";

  // Two diagrams for the Configuring printpaca topic, drawn as inline SVG from theme tokens (no diagram library).
  // The SVG is decoration (aria-hidden). Its text alternative, a table or a list built from the same data, is
  // always in the accessibility tree and is what you see below 760px, where the SVG would be too small to read.
  let { id }: { id: "settings-map" | "save-flow" } = $props();

  /** Splits text into lines of at most `max` characters, at spaces. */
  function wrap(text: string, max: number): string[] {
    const lines: string[] = [];
    let line = "";
    for (const word of text.split(" ")) {
      if (line && line.length + 1 + word.length > max) {
        lines.push(line);
        line = word;
      } else {
        line = line ? `${line} ${word}` : word;
      }
    }
    if (line) lines.push(line);
    return lines;
  }

  const ROW_H = 34;
  const ROW_STEP = 40;
  const HEAD_Y = 26;
  const rowY = (i: number) => HEAD_Y + 12 + i * ROW_STEP;
  const mapHeight = HEAD_Y + 12 + SETTINGS_PAGE_ROWS.length * ROW_STEP;

  // Save flow geometry: five boxes across, branches below, then the record lane.
  const BW = 164;
  const BH = 100;
  const GAP = 35;
  const boxX = (i: number) => i * (BW + GAP);
  const MAIN_Y = 30;
  const BR_Y = 176;
  const BR_W = 190;
  const BR_H = 124;
  const REC_Y = 372;
  const REC_W = 200;
  const REC_GAP = (960 - 4 * REC_W) / 3;
  const recX = (i: number) => i * (REC_W + REC_GAP);
  const mainIndex = (idv: string) => SAVE_MAIN.findIndex((b) => b.id === idv);
  const toneClass = (b: FlowBox) => `box ${b.tone}`;
</script>

<figure class="diagram">
  {#if id === "settings-map"}
    <svg class="diagram-svg" viewBox={`0 0 960 ${mapHeight}`} aria-hidden="true" focusable="false">
      <text class="head" x="0" y={HEAD_Y - 6}>Settings page cards</text>
      {#each SETTINGS_PAGE_ROWS as r, i (r.where)}
        <rect class="box step" x="0" y={rowY(i)} width="400" height={ROW_H} rx="6" />
        <text class="label" x="12" y={rowY(i) + 22}>{r.where}</text>
        <text class="key" x="388" y={rowY(i) + 22} text-anchor="end">{r.keys.join(", ")}</text>
      {/each}
      <text class="head" x="420" y={HEAD_Y - 6}>Other pages</text>
      {#each OTHER_PAGE_ROWS as r, i (r.where)}
        <rect class="box step" x="420" y={rowY(i)} width="270" height={ROW_H} rx="6" />
        <text class="label" x="432" y={rowY(i) + 22}>{r.where}</text>
        <text class="key" x="678" y={rowY(i) + 22} text-anchor="end">{r.keys.join(", ")}</text>
      {/each}
      <text class="head" x="710" y={HEAD_Y - 6}>Environment variables only</text>
      {#each ENV_ONLY_ROWS as r, i (r.name)}
        <rect class="box warn" x="710" y={rowY(i)} width="250" height={ROW_H} rx="6" />
        <text class="key" x="722" y={rowY(i) + 22}>{r.name}</text>
      {/each}
      <text class="note" x="420" y={rowY(OTHER_PAGE_ROWS.length) + 14}>Right-hand names are keys in config.yaml.</text>
    </svg>
    <div class="diagram-alt">
      <table>
        <caption>Where each setting lives</caption>
        <thead><tr><th scope="col">Where you edit it</th><th scope="col">Key in <code>config.yaml</code></th></tr></thead>
        <tbody>
          {#each SETTINGS_PAGE_ROWS as r (r.where)}
            <tr><th scope="row">Settings, {r.where}</th><td>{#each r.keys as k, i (k)}{#if i > 0}, {/if}<code>{k}</code>{/each}</td></tr>
          {/each}
          {#each OTHER_PAGE_ROWS as r (r.where)}
            <tr><th scope="row">{r.where}</th><td>{#each r.keys as k, i (k)}{#if i > 0}, {/if}<code>{k}</code>{/each}</td></tr>
          {/each}
        </tbody>
      </table>
      <table>
        <caption>Values with no field in the app: environment variables only</caption>
        <thead><tr><th scope="col">Variable</th><th scope="col">What it sets</th></tr></thead>
        <tbody>
          {#each ENV_ONLY_ROWS as r (r.name)}
            <tr><th scope="row"><code>{r.name}</code></th><td>{r.what}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <svg class="diagram-svg" viewBox="0 0 960 560" aria-hidden="true" focusable="false">
      <defs>
        <marker id="guide-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">
          <path d="M0 0 L10 5 L0 10 z" class="arrow-head" />
        </marker>
      </defs>
      <text class="head" x="0" y="16">Saving Settings</text>
      {#each SAVE_MAIN as b, i (b.id)}
        <rect class={toneClass(b)} x={boxX(i)} y={MAIN_Y} width={BW} height={BH} rx="8" />
        <text class="title" x={boxX(i) + 12} y={MAIN_Y + 24}>{b.title}</text>
        {#each wrap(b.detail, 22) as line, k (k)}
          <text class="detail" x={boxX(i) + 12} y={MAIN_Y + 44 + k * 16}>{line}</text>
        {/each}
        {#if i < SAVE_MAIN.length - 1}
          <line class="arrow" x1={boxX(i) + BW} y1={MAIN_Y + BH / 2} x2={boxX(i + 1) - 2} y2={MAIN_Y + BH / 2} marker-end="url(#guide-arrow)" />
        {/if}
      {/each}
      {#each SAVE_BRANCHES as br (br.box.id)}
        {@const cx = boxX(mainIndex(br.from)) + BW / 2}
        <line class="arrow" x1={cx} y1={MAIN_Y + BH} x2={cx} y2={BR_Y - 2} marker-end="url(#guide-arrow)" />
        <rect class={toneClass(br.box)} x={cx - BR_W / 2} y={BR_Y} width={BR_W} height={BR_H} rx="8" />
        <text class="title" x={cx - BR_W / 2 + 12} y={BR_Y + 24}>{br.box.title}</text>
        {#each wrap(br.box.detail, 27) as line, k (k)}
          <text class="detail" x={cx - BR_W / 2 + 12} y={BR_Y + 44 + k * 16}>{line}</text>
        {/each}
      {/each}
      <text class="head" x="0" y={REC_Y - 18}>Saving a quote, an invoice, or a shop item</text>
      {#each RECORD_FLOW as b, i (b.id)}
        <rect class={toneClass(b)} x={recX(i)} y={REC_Y} width={REC_W} height="130" rx="8" />
        <text class="title" x={recX(i) + 12} y={REC_Y + 24}>{b.title}</text>
        {#each wrap(b.detail, 28) as line, k (k)}
          <text class="detail" x={recX(i) + 12} y={REC_Y + 44 + k * 16}>{line}</text>
        {/each}
      {/each}
      <line class="arrow" x1={recX(0) + REC_W} y1={REC_Y + 65} x2={recX(1) - 2} y2={REC_Y + 65} marker-end="url(#guide-arrow)" />
      <line class="arrow" x1={recX(1) + REC_W} y1={REC_Y + 65} x2={recX(2) - 2} y2={REC_Y + 65} marker-end="url(#guide-arrow)" />
      <path class="arrow" d={`M ${recX(1) + REC_W / 2} ${REC_Y + 130} V ${REC_Y + 150} H ${recX(3) + REC_W / 2} V ${REC_Y + 132}`} fill="none" marker-end="url(#guide-arrow)" />
    </svg>
    <div class="diagram-alt">
      <h4>Saving Settings</h4>
      <ol>
        {#each SAVE_MAIN as b (b.id)}
          <li><strong>{b.title}.</strong> {b.detail}</li>
        {/each}
      </ol>
      <p>Two things can go wrong:</p>
      <ul>
        {#each SAVE_BRANCHES as br (br.box.id)}
          <li><strong>{br.box.title}</strong>, after <em>{SAVE_MAIN.find((m) => m.id === br.from)?.title}</em>. {br.box.detail}</li>
        {/each}
      </ul>
      <h4>Saving a quote, an invoice, or a shop item</h4>
      <ol>
        {#each RECORD_FLOW as b (b.id)}
          <li><strong>{b.title}.</strong> {b.detail}</li>
        {/each}
      </ol>
    </div>
  {/if}
</figure>

<style>
  .diagram { position: relative; margin: 0; display: flex; flex-direction: column; gap: var(--space-3); }
  .diagram-svg { width: 100%; height: auto; display: block; }
  .box { stroke-width: 1.5; fill: var(--surface-2); stroke: var(--border); }
  .box.ok { stroke: var(--accent-text); stroke-width: 2.5; }
  .box.warn { stroke: var(--danger); stroke-width: 2; }
  .head { fill: var(--text); font-weight: 700; font-size: 14px; letter-spacing: 0.04em; text-transform: uppercase; }
  .title { fill: var(--text); font-weight: 700; font-size: 15px; }
  .detail, .label { fill: var(--text); font-size: 13px; }
  .key { fill: var(--text); font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 13px; }
  .note { fill: var(--muted); font-size: 12px; }
  .arrow { stroke: var(--text); stroke-width: 2; }
  .arrow-head { fill: var(--text); }
  .diagram-alt h4 { margin: var(--space-3) 0 var(--space-1); }
  .diagram-alt ol, .diagram-alt ul { margin: 0 0 var(--space-2); padding-left: var(--space-5); }
  .diagram-alt table + table { margin-top: var(--space-4); }
  /* Small screens: the text alternative is the content. From 760px the SVG shows and the text stays for screen readers. */
  @media (max-width: 759.98px) {
    .diagram-svg { display: none; }
  }
  @media (min-width: 760px) {
    .diagram-alt {
      position: absolute;
      width: 1px;
      height: 1px;
      margin: -1px;
      overflow: hidden;
      clip-path: inset(50%);
      white-space: nowrap;
    }
  }
</style>
