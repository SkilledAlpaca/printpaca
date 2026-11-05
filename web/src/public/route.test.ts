import { describe, expect, it } from "vitest";
import { parseRoute, routeHash, type Route } from "./route";

describe("parseRoute", () => {
  it("treats an empty hash and the root as the gallery", () => {
    for (const h of ["", "#", "#/", "#//"]) expect(parseRoute(h)).toEqual({ name: "gallery" });
  });

  it("parses the item, estimate and confirmation routes", () => {
    expect(parseRoute("#/item/abc123")).toEqual({ name: "item", id: "abc123" });
    expect(parseRoute("#/estimate")).toEqual({ name: "estimate" });
    expect(parseRoute("#/estimate/")).toEqual({ name: "estimate" });
    expect(parseRoute("#/estimate/received")).toEqual({ name: "estimate-received" });
    expect(parseRoute("#/order/received")).toEqual({ name: "order-received" });
  });

  it("parses a category route and decodes its slug", () => {
    expect(parseRoute("#/category/toys")).toEqual({ name: "category", slug: "toys" });
    expect(parseRoute("#/category/toys/")).toEqual({ name: "category", slug: "toys" });
    expect(parseRoute("#/category/a%2Db")).toEqual({ name: "category", slug: "a-b" });
  });

  it("decodes an escaped item id", () => {
    expect(parseRoute("#/item/a%20b")).toEqual({ name: "item", id: "a b" });
  });

  it("falls back to the gallery for unknown, empty-id and malformed routes", () => {
    for (const h of ["#/nope", "#/item/", "#/item/%E0%A4%A", "#/estimate/other", "#/category/", "#/category/%E0%A4%A"]) {
      expect(parseRoute(h)).toEqual({ name: "gallery" });
    }
  });
});

describe("routeHash", () => {
  it("round-trips every route", () => {
    const routes: Route[] = [
      { name: "gallery" },
      { name: "item", id: "a b/c" },
      { name: "category", slug: "toys-2" },
      { name: "estimate" },
      { name: "estimate-received" },
      { name: "order-received" },
    ];
    for (const r of routes) expect(parseRoute(routeHash(r))).toEqual(r);
  });
});
