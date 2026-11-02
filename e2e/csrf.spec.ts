import { test, expect } from "@playwright/test";

// Browser-level proof that csrfGuard protects a real mutation endpoint (POST /api/quotes) on the live
// :8099 server, even with auth disabled. The guard checks only method, Content-Type, and Origin, so a
// request that clears it may still fail validation (asserted as "not 415/403", not as a 2xx).

test("a non-JSON Content-Type on a mutating request is rejected with 415", async ({ request }) => {
  const res = await request.post("/admin/api/quotes", {
    headers: { "Content-Type": "text/plain" },
    data: "{}",
  });
  expect(res.status()).toBe(415);
});

test("a cross-origin Origin header on a JSON mutating request is rejected with 403", async ({ request }) => {
  const res = await request.post("/admin/api/quotes", {
    headers: { "Content-Type": "application/json", Origin: "http://evil.example" },
    data: {},
  });
  expect(res.status()).toBe(403);
});

test("a same-origin JSON request clears the CSRF guard (it may still fail its own validation)", async ({
  request,
  baseURL,
}) => {
  const res = await request.post("/admin/api/quotes", {
    headers: { "Content-Type": "application/json", Origin: baseURL! },
    data: {},
  });
  expect(res.status()).not.toBe(415);
  expect(res.status()).not.toBe(403);
});
