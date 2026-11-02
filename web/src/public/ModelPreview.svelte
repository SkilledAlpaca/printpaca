<script lang="ts">
  // Renders a decimated triangle set (math in lib/modelPreview.ts; DOM, canvas and pointer wiring here) as a
  // draggable isometric-ish 3D preview. Purely visual: it takes no part in estimate, upload or submit, so a
  // rendering failure just paints nothing.
  import { onDestroy, onMount } from "svelte";
  import {
    computeBoundingBox,
    computeFit,
    depthSortTriangles,
    projectTriangle,
    type Triangle,
  } from "../lib/modelPreview";

  let { triangles, fileName }: { triangles: Triangle[]; fileName: string } = $props();

  let canvasEl: HTMLCanvasElement | undefined;
  let wrapEl: HTMLDivElement | undefined;

  // Default angle: not true isometric (35.26° pitch); 45° yaw / 30° pitch reads as isometric-ish and avoids
  // yaw=0, a flat silhouette down one axis for most models.
  const DEFAULT_YAW = Math.PI / 4;
  const DEFAULT_PITCH = -Math.PI / 6;
  // Keeps the model from being dragged past near-vertical, where an orthographic top-down view reads as
  // flipping inside out.
  const MAX_PITCH = Math.PI / 2 - 0.05;
  const DRAG_RADIANS_PER_PIXEL = 0.01;
  // Drop-zone column width cap. Must match the `.model-preview` max-width in the stylesheet; otherwise the
  // canvas backing store stays at the old resolution and blurs.
  const MAX_CSS_SIZE = 320;
  const FALLBACK_ACCENT_RGB: [number, number, number] = [224, 96, 32]; // theme.css light-scheme --accent, used only if the CSS var can't be read

  let yaw = $state(DEFAULT_YAW);
  let pitch = $state(DEFAULT_PITCH);
  // Square side in CSS px, kept in sync with the wrapper's layout box via ResizeObserver.
  let cssSize = $state(240);

  let dragging = false;
  let lastClientX = 0;
  let lastClientY = 0;
  let rafHandle: number | null = null;
  let resizeObserver: ResizeObserver | null = null;

  /** Coalesces state changes within one paint into a single render instead of one per pointermove, which
   * can fire faster than the display refresh. */
  function scheduleRender() {
    if (rafHandle !== null) return;
    rafHandle = requestAnimationFrame(() => {
      rafHandle = null;
      render();
    });
  }

  function accentRgb(el: Element): [number, number, number] {
    const raw = getComputedStyle(el).getPropertyValue("--accent").trim();
    const hex = raw.startsWith("#") ? raw.slice(1) : raw;
    if (hex.length !== 6) return FALLBACK_ACCENT_RGB;
    const r = Number.parseInt(hex.slice(0, 2), 16);
    const g = Number.parseInt(hex.slice(2, 4), 16);
    const b = Number.parseInt(hex.slice(4, 6), 16);
    if (![r, g, b].every(Number.isFinite)) return FALLBACK_ACCENT_RGB;
    return [r, g, b];
  }

  function render() {
    const canvas = canvasEl;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    // Backing store sized in device pixels for hi-DPI; ctx.setTransform then lets draw calls use CSS-pixel
    // coordinates.
    const dpr = window.devicePixelRatio || 1;
    const deviceSize = Math.max(1, Math.round(cssSize * dpr));
    if (canvas.width !== deviceSize || canvas.height !== deviceSize) {
      canvas.width = deviceSize;
      canvas.height = deviceSize;
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cssSize, cssSize); // transparent: the surrounding .card's own background shows through in both themes

    if (triangles.length === 0) return;
    const bbox = computeBoundingBox(triangles);
    if (!bbox) return;
    const fit = computeFit(bbox, cssSize);

    const [ar, ag, ab] = accentRgb(canvas);
    const projected = triangles.map((t) => projectTriangle(t, fit, yaw, pitch, cssSize, cssSize));
    const sorted = depthSortTriangles(projected); // painter's algorithm: farthest first, nearest last (drawn on top)

    for (const t of sorted) {
      const color = `rgb(${Math.round(ar * t.shade)}, ${Math.round(ag * t.shade)}, ${Math.round(ab * t.shade)})`;
      ctx.beginPath();
      ctx.moveTo(t.x1, t.y1);
      ctx.lineTo(t.x2, t.y2);
      ctx.lineTo(t.x3, t.y3);
      ctx.closePath();
      ctx.fillStyle = color;
      // Same-color 1px stroke hides antialiasing seams between adjacent flat-shaded triangles.
      ctx.strokeStyle = color;
      ctx.lineWidth = 1;
      ctx.fill();
      ctx.stroke();
    }
  }

  // Re-renders when a fresh triangle set, a resize (cssSize) or a drag (yaw/pitch) arrives; explicit reads
  // make the dependency list obvious.
  $effect(() => {
    void triangles;
    void yaw;
    void pitch;
    void cssSize;
    scheduleRender();
  });

  function onPointerDown(e: PointerEvent) {
    dragging = true;
    lastClientX = e.clientX;
    lastClientY = e.clientY;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }
  function onPointerMove(e: PointerEvent) {
    if (!dragging) return;
    const dx = e.clientX - lastClientX;
    const dy = e.clientY - lastClientY;
    lastClientX = e.clientX;
    lastClientY = e.clientY;
    yaw += dx * DRAG_RADIANS_PER_PIXEL;
    pitch = Math.max(-MAX_PITCH, Math.min(MAX_PITCH, pitch + dy * DRAG_RADIANS_PER_PIXEL));
  }
  function onPointerUp() {
    dragging = false;
  }

  onMount(() => {
    if (wrapEl) {
      resizeObserver = new ResizeObserver((entries) => {
        const width = entries[0]?.contentRect.width;
        if (width && width > 0) cssSize = Math.round(Math.min(MAX_CSS_SIZE, width));
      });
      resizeObserver.observe(wrapEl);
    }
  });
  onDestroy(() => {
    resizeObserver?.disconnect();
    if (rafHandle !== null) cancelAnimationFrame(rafHandle);
  });
</script>

<!-- role="img"/aria-label live on this wrapper, not the pointer-handling <canvas>: a static role there
     trips a11y_no_interactive_element_to_noninteractive_role. Decorative, so no keyboard equivalent for
     dragging. -->
<div class="model-preview" role="img" aria-label={`3D preview of ${fileName}`} bind:this={wrapEl}>
  <canvas
    bind:this={canvasEl}
    onpointerdown={onPointerDown}
    onpointermove={onPointerMove}
    onpointerup={onPointerUp}
    onpointercancel={onPointerUp}
  ></canvas>
</div>

<style>
  /* Fits the drop-zone column: full width up to a cap, always square so the fit math's single viewport size
   * matches the drawing. Must match MAX_CSS_SIZE in the script. */
  .model-preview {
    width: 100%;
    max-width: 320px;
    margin: 0 auto;
  }
  canvas {
    display: block;
    width: 100%;
    aspect-ratio: 1;
    /* Stops the browser's touch scroll/pinch from competing with drag-to-rotate. */
    touch-action: none;
    cursor: grab;
    border-radius: var(--radius);
    background: transparent;
  }
  canvas:active {
    cursor: grabbing;
  }
</style>
