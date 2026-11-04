// Hand-rolled app-shell service worker for the ADMIN SPA only (no Workbox, no npm dependency). The build id
// and precache list below are literal double-underscore tokens substituted at build time by
// adminPrecachePlugin() in web/vite.config.ts; unreplaced means this is the source file, not a built dist/sw.js.
//
// The admin app is served from /admin/, and the worker registers as "sw.js" relative to it, so its scope is
// /admin/ and it never sees "/", "/order" or "/api/public/*". The precache list is relative to that scope.
const CACHE = "printpaca-shell-66d3efb7f2";
const SCOPE = new URL(self.registration.scope).pathname; // "/admin/"
self.__PRECACHE__ = ["./","alpaca-logo.svg","assets/alpaca-logo-D0HhaUnP.svg","assets/main-DyG6XELp.css","assets/main-kERY2uUv.js","assets/theme-SbkyCdUl.css","assets/theme-Ys5wF3pH.js","icons/icon-192.png","icons/icon-512.png","icons/icon-maskable-512.png","index.html","manifest.webmanifest"].map((p) => new URL(p, self.registration.scope).pathname);

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE).then((cache) => cache.addAll(self.__PRECACHE__)).then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) =>
        Promise.all(
          names
            .filter((name) => name.startsWith("printpaca-shell-") && name !== CACHE)
            .map((name) => caches.delete(name)),
        ),
      )
      .then(() => self.clients.claim()),
  );
});

// Stores a copy of a successful response. A 404/5xx (a deploy mid-rollout, a proxy error page) must never
// replace a good cached entry, or be served from cache afterwards.
function cacheIfOk(request, response) {
  if (!response.ok) return;
  const copy = response.clone();
  caches.open(CACHE).then((cache) => cache.put(request, copy));
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  // A page this worker controls can request paths outside the admin app (a shared host's public routes);
  // those are never ours.
  if (!url.pathname.startsWith(SCOPE)) return;
  const rel = url.pathname.slice(SCOPE.length);

  // Security-critical: api/* and healthz are server-authoritative (quotes, pricing, auth) and must NEVER be
  // read from or written to this cache. Returning without respondWith lets the request hit the network as if
  // this worker didn't exist.
  if (rel.startsWith("api/") || rel === "healthz") return;

  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request).catch(() => caches.match(SCOPE).then((cached) => cached ?? Response.error())),
    );
    return;
  }

  if (rel.startsWith("assets/")) {
    // Content-hashed, immutable: cache-first.
    event.respondWith(
      caches.match(request).then(
        (cached) =>
          cached ??
          fetch(request).then((response) => {
            cacheIfOk(request, response);
            return response;
          }),
      ),
    );
    return;
  }

  // Anything not in the precached shell goes to the network untouched, like the /api bypass above.
  if (!self.__PRECACHE__.includes(url.pathname)) return;

  // Other precached shell files (manifest, icons, alpaca-logo.svg): stale-while-revalidate.
  event.respondWith(
    caches.match(request).then((cached) => {
      const network = fetch(request)
        .then((response) => {
          cacheIfOk(request, response);
          return response;
        })
        .catch(() => cached ?? Response.error());
      return cached ?? network;
    }),
  );
});
