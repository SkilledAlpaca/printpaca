import { defineConfig, type Plugin, type Rollup } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { gzipSync } from "node:zlib";
import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { createHash } from "node:crypto";

// Every file the entry chunk named `entryName` needs at load time: the chunk, its static and dynamic
// imports (transitively), and the CSS and assets Vite attached to each. Null when the bundle has no such
// entry. viteMetadata is Vite's chunk metadata (absent from Rollup's OutputChunk type).
function entryFiles(bundle: Rollup.OutputBundle, entryName: string): Set<string> | null {
  const files = new Set<string>();
  const visited = new Set<string>();

  const visitChunk = (fileName: string) => {
    if (visited.has(fileName)) return;
    visited.add(fileName);
    const item = bundle[fileName];
    if (!item || item.type !== "chunk") return;
    const chunk = item as Rollup.OutputChunk;
    files.add(chunk.fileName);
    const meta = (
      chunk as unknown as {
        viteMetadata?: { importedCss?: Set<string>; importedAssets?: Set<string> };
      }
    ).viteMetadata;
    for (const css of meta?.importedCss ?? []) files.add(css);
    for (const asset of meta?.importedAssets ?? []) files.add(asset);
    for (const imp of chunk.imports) visitChunk(imp);
    for (const imp of chunk.dynamicImports) visitChunk(imp);
  };

  let foundEntry = false;
  for (const item of Object.values(bundle)) {
    if (item.type === "chunk" && item.isEntry && item.name === entryName) {
      foundEntry = true;
      visitChunk(item.fileName);
    }
  }
  return foundEntry ? files : null;
}

// Emits dist/public-manifest.json: every file the public entry needs (its chunks, chunks shared with admin,
// static files public.html references). server/public.go enforces it as an allowlist so the admin entry built
// into the same dist is never reachable on the public listener.
function publicManifestPlugin(): Plugin {
  return {
    name: "printpaca-public-manifest",
    apply: "build",
    // Runs after Vite's core plugins so bundle["public.html"] is already rewritten with hashed hrefs.
    enforce: "post",
    generateBundle(_options, bundle) {
      const files = entryFiles(bundle, "public");
      if (!files) {
        this.error(
          'printpaca-public-manifest: no entry chunk named "public" found in the bundle -- ' +
            "check rollupOptions.input below still declares a public entry",
        );
      }

      files.add("public.html");
      const htmlAsset = bundle["public.html"];
      if (htmlAsset && htmlAsset.type === "asset") {
        const asset = htmlAsset as Rollup.OutputAsset;
        const source =
          typeof asset.source === "string" ? asset.source : new TextDecoder().decode(asset.source);
        // Refs copied verbatim from web/public or served by the server (custom.css) never appear in the module
        // graph; read them from the emitted HTML. They are relative to public.html ("./alpaca-logo.svg",
        // "custom.css"), which the server serves at both / and /order, so each resolves from the root.
        for (const match of source.matchAll(/(?:href|src)="(?!\w+:|\/\/|#)(?:\.\/|\/)?([^"?#]+)[^"]*"/g)) {
          files.add(match[1]);
        }
      }

      this.emitFile({
        type: "asset",
        fileName: "public-manifest.json",
        source: JSON.stringify([...files].sort(), null, 2) + "\n",
      });
    },
  };
}

// Substitutes __BUILD_ID__ (hash of the sorted precache list, deterministic) and __PRECACHE_LIST__ in
// web/public/sw.js. sw.js is copied verbatim so it is absent from the bundle; writeBundle runs before
// closeBundle, so precompressPlugin's gzip sees the substituted file.
function adminPrecachePlugin(): Plugin {
  let precache: string[] = [];

  return {
    name: "printpaca-admin-precache",
    apply: "build",
    enforce: "post",
    generateBundle(_options, bundle) {
      const files = entryFiles(bundle, "main");
      if (!files) {
        this.error(
          'printpaca-admin-precache: no entry chunk named "main" found in the bundle -- ' +
            "check rollupOptions.input below still declares a main (admin) entry",
        );
      }

      // Relative to the service worker's scope (/admin/): sw.js resolves each entry against
      // self.registration.scope, so the same list works wherever the app is mounted.
      precache = [
        "./",
        "index.html",
        "manifest.webmanifest",
        "icons/icon-192.png",
        "icons/icon-512.png",
        "icons/icon-maskable-512.png",
        "alpaca-logo.svg",
        ...files,
      ].sort();
    },
    writeBundle(options) {
      const outDir = options.dir ?? new URL("dist", import.meta.url).pathname;
      const swPath = join(outDir, "sw.js");
      const source = readFileSync(swPath, "utf8");

      const buildId = createHash("sha256").update(JSON.stringify(precache)).digest("hex").slice(0, 10);

      const substituted = source
        .replaceAll("__BUILD_ID__", buildId)
        .replaceAll("__PRECACHE_LIST__", JSON.stringify(precache));

      writeFileSync(swPath, substituted);
    },
  };
}

// Emits foo.js.gz next to every output file; server/static.go prefers the sibling when the client accepts
// gzip. Runs in closeBundle by walking outDir. gzip only, no brotli.
function precompressPlugin(): Plugin {
  return {
    name: "printpaca-precompress",
    apply: "build",
    closeBundle() {
      const outDir = new URL("dist", import.meta.url).pathname;
      for (const file of listFiles(outDir)) {
        if (file.endsWith(".gz")) continue;
        const original = readFileSync(file);
        const compressed = gzipSync(original, { level: 9 });
        // Skip the sibling when gzip doesn't shrink the file; the static handler falls back to the original.
        if (compressed.length < original.length) {
          writeFileSync(file + ".gz", compressed);
        }
      }
    },
  };
}

function listFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...listFiles(full));
    } else {
      out.push(full);
    }
  }
  return out;
}

export default defineConfig({
  plugins: [svelte(), publicManifestPlugin(), adminPrecachePlugin(), precompressPlugin()],
  // Relative base: the admin entry (/admin/) and the public entry (/ and /order) each resolve their assets
  // against their own HTML, so one build serves both ("/admin/assets/..." and "/assets/...").
  base: "./",
  // The dev server serves the admin app at "/": its relative "api/..." calls arrive as "/api/...", which the
  // production server answers under /admin/api/. The public page's /api/public/... passes straight through.
  server: {
    proxy: {
      "/api/public": "http://localhost:8080",
      "/api": { target: "http://localhost:8080", rewrite: (path) => "/admin" + path },
    },
  },
  build: {
    // The floor the SPA supports (README requirements): AbortSignal.any needs Chrome 116, Firefox 124, Safari 17.4.
    target: ["chrome116", "firefox124", "safari17.4", "edge116"],
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      // Two entries in one dist: admin SPA (index.html, server/static.go) and public quote page (public.html,
      // server/public.go). Named so publicManifestPlugin can find the "public" chunk.
      input: {
        main: new URL("index.html", import.meta.url).pathname,
        public: new URL("public.html", import.meta.url).pathname,
      },
    },
  },
});
