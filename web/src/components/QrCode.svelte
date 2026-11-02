<script lang="ts">
  import { encode } from "uqr";

  // Inline SVG QR code, always black on white with a quiet zone in both themes so a phone can read the
  // printed page. ecc "M" tolerates smudging and folding.
  let { value, label, size = "30mm" }: { value: string; label: string; size?: string } = $props();

  const QUIET = 4;

  const qr = $derived.by(() => {
    const { data, size: n } = encode(value, { ecc: "M", border: 0 });
    // One path segment per horizontal run of dark modules keeps the SVG small.
    let d = "";
    for (let y = 0; y < n; y++) {
      let x = 0;
      while (x < n) {
        const row = data[y] ?? [];
        if (!row[x]) { x++; continue; }
        const start = x;
        while (x < n && row[x]) x++;
        d += `M${start + QUIET} ${y + QUIET}h${x - start}v1h-${x - start}z`;
      }
    }
    return { d, dim: n + QUIET * 2 };
  });
</script>

<svg
  class="qr"
  viewBox={`0 0 ${qr.dim} ${qr.dim}`}
  width={size}
  height={size}
  role="img"
  aria-label={label}
  shape-rendering="crispEdges"
>
  <rect width={qr.dim} height={qr.dim} fill="#fff" />
  <path d={qr.d} fill="#000" />
</svg>

<style>
  .qr {
    display: block;
    background: #fff;
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
</style>
