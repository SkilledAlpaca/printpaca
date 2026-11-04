// Hash routes for the shop, so the browser's Back button, reload and a shared link all work. The server serves
// the same page at "/" and "/order" and never sees the hash, so no server route is needed.
//
//   #/                    the gallery (or the estimate form when the shop has no items)
//   #/item/<id>           one item's order panel
//   #/estimate            the custom estimate form
//   #/order/received      the item order confirmation
//   #/estimate/received   the estimate confirmation
//
// The two confirmation routes need data that lives in sessionStorage (confirmationStorage.ts); when it is
// gone (a new tab, a cleared session) the page returns to the gallery with a short note.

export type Route =
  | { name: "gallery" }
  | { name: "item"; id: string }
  | { name: "estimate" }
  | { name: "order-received" }
  | { name: "estimate-received" };

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#\/?/, "").replace(/\/+$/, "");
  if (path === "estimate") return { name: "estimate" };
  if (path === "estimate/received") return { name: "estimate-received" };
  if (path === "order/received") return { name: "order-received" };
  if (path.startsWith("item/")) {
    const raw = path.slice("item/".length);
    try {
      const id = decodeURIComponent(raw);
      if (id !== "") return { name: "item", id };
    } catch {
      // A malformed escape is not an item link; fall through to the gallery.
    }
  }
  return { name: "gallery" };
}

export function routeHash(route: Route): string {
  switch (route.name) {
    case "item":
      return `#/item/${encodeURIComponent(route.id)}`;
    case "estimate":
      return "#/estimate";
    case "estimate-received":
      return "#/estimate/received";
    case "order-received":
      return "#/order/received";
    default:
      return "#/";
  }
}
