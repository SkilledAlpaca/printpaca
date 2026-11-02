import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// Runs public/sw.js (the source, with its build tokens filled in) against fake worker globals, so the
// caching rules are tested without a browser. The worker's scope is /admin/ (it registers as "sw.js" from the
// admin page), and the precache list is relative to that scope.

type Handler = (event: unknown) => void;

function loadWorker(precache: string[], network: (req: FakeRequest) => FakeResponse) {
  const source = readFileSync(resolve(__dirname, "../../public/sw.js"), "utf-8")
    .replaceAll("__BUILD_ID__", "test")
    .replaceAll("__PRECACHE_LIST__", JSON.stringify(precache));

  const store = new Map<string, FakeResponse>();
  const handlers: Record<string, Handler> = {};
  const caches = {
    match: async (req: FakeRequest | string) => store.get(typeof req === "string" ? `http://x${req}` : req.url),
    open: async () => ({
      put: async (req: FakeRequest, res: FakeResponse) => {
        store.set(req.url, res);
      },
      addAll: async () => {},
    }),
    keys: async () => [],
  };
  const self = {
    location: { origin: "http://x" },
    registration: { scope: "http://x/admin/" },
    addEventListener: (type: string, h: Handler) => {
      handlers[type] = h;
    },
    skipWaiting: async () => {},
    clients: { claim: async () => {} },
    __PRECACHE__: undefined as string[] | undefined,
  };
  new Function("self", "caches", "fetch", "Response", source)(self, caches, async (req: FakeRequest) => network(req), FakeResponseCtor);

  /** Whether the worker called respondWith for a GET of path; false means the request goes straight to the
   * network, as if no worker existed. */
  function handles(path: string, mode = "no-cors"): boolean {
    const request: FakeRequest = { url: `http://x${path}`, method: "GET", mode };
    let handled = false;
    handlers.fetch!({ request, respondWith: () => (handled = true) });
    return handled;
  }

  async function fetchThroughWorker(path: string, mode = "no-cors"): Promise<FakeResponse> {
    const request: FakeRequest = { url: `http://x${path}`, method: "GET", mode };
    let answer: Promise<FakeResponse> | undefined;
    handlers.fetch!({ request, respondWith: (p: Promise<FakeResponse>) => (answer = p) });
    if (!answer) throw new Error("worker did not handle " + path);
    const result = await answer;
    await new Promise((r) => setTimeout(r, 0)); // let the fire-and-forget cache.put settle
    return result;
  }
  return { store, fetchThroughWorker, handles, self };
}

interface FakeRequest {
  url: string;
  method: string;
  mode: string;
}

class FakeResponse {
  constructor(
    public ok: boolean,
    public body: string,
  ) {}
  clone() {
    return new FakeResponse(this.ok, this.body);
  }
  static error() {
    return new FakeResponse(false, "error");
  }
}
const FakeResponseCtor = FakeResponse;

describe("service worker caching", () => {
  it("caches a successful /assets/ response (cache-first path)", async () => {
    const w = loadWorker([], () => new FakeResponse(true, "js"));
    await w.fetchThroughWorker("/admin/assets/app-abc.js");
    expect(w.store.get("http://x/admin/assets/app-abc.js")?.body).toBe("js");
  });

  it("does not cache a non-ok /assets/ response (cache-first path)", async () => {
    const w = loadWorker([], () => new FakeResponse(false, "404 page"));
    const res = await w.fetchThroughWorker("/admin/assets/missing.js");
    expect(res.ok).toBe(false); // the caller still sees the failure
    expect(w.store.size).toBe(0);
  });

  it("caches a successful precached shell file (stale-while-revalidate path)", async () => {
    const w = loadWorker(["manifest.webmanifest"], () => new FakeResponse(true, "{}"));
    await w.fetchThroughWorker("/admin/manifest.webmanifest");
    expect(w.store.get("http://x/admin/manifest.webmanifest")?.body).toBe("{}");
  });

  it("does not overwrite a cached shell file with a non-ok response (stale-while-revalidate path)", async () => {
    let ok = true;
    const w = loadWorker(["manifest.webmanifest"], () => new FakeResponse(ok, ok ? "good" : "502 page"));
    await w.fetchThroughWorker("/admin/manifest.webmanifest");
    ok = false;
    await w.fetchThroughWorker("/admin/manifest.webmanifest");
    expect(w.store.get("http://x/admin/manifest.webmanifest")?.body).toBe("good");
  });

  it("resolves the precache list against the worker's scope", () => {
    const w = loadWorker(["./", "index.html", "assets/a.js"], () => new FakeResponse(true, ""));
    expect(w.self.__PRECACHE__).toEqual(["/admin/", "/admin/index.html", "/admin/assets/a.js"]);
  });

  it("never touches api/* or healthz, even if listed", () => {
    const w = loadWorker(["api/quotes", "healthz"], () => new FakeResponse(true, ""));
    expect(w.handles("/admin/api/quotes")).toBe(false);
    expect(w.handles("/admin/api/me")).toBe(false);
    expect(w.handles("/admin/healthz")).toBe(false);
  });

  it("ignores everything outside the /admin/ scope, including the public routes", () => {
    const w = loadWorker(["./"], () => new FakeResponse(true, ""));
    for (const path of ["/", "/order", "/api/public/config", "/api/quotes", "/item-images/a.png", "/assets/x.js", "/administrator"]) {
      expect(w.handles(path), path).toBe(false);
    }
  });

  it("falls back to the cached app shell for an offline admin navigation", async () => {
    let online = true;
    const w = loadWorker(["./"], () => {
      if (!online) throw new Error("offline");
      return new FakeResponse(true, "shell");
    });
    w.store.set("http://x/admin/", new FakeResponse(true, "cached shell"));
    online = false;
    const res = await w.fetchThroughWorker("/admin/", "navigate");
    expect(res.body).toBe("cached shell");
  });
});
