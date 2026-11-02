import { afterEach, describe, expect, it, vi } from "vitest";
import { createUser, createUserBody, updateUser, type UserRecord } from "./usersApi";

afterEach(() => {
  vi.unstubAllGlobals();
});

function record(overrides: Partial<UserRecord> = {}): UserRecord {
  return {
    id: "u1",
    username: "flo",
    permissions: [],
    email: "",
    disabled: false,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function stubFetch(body: unknown, status = 200) {
  const fn = vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fn);
  return fn;
}

describe("createUserBody", () => {
  it("omits email when blank so the request matches a pre-email client", () => {
    expect(createUserBody("flo", "password123", ["clients"], "  ")).toEqual({
      username: "flo",
      password: "password123",
      permissions: ["clients"],
    });
  });

  it("sends a trimmed email when set", () => {
    expect(createUserBody("flo", "password123", [], " Flo@Example.com ")).toMatchObject({ email: "Flo@Example.com" });
  });
});

describe("createUser / updateUser email round trip", () => {
  it("POSTs the email and returns the stored one", async () => {
    const fetchFn = stubFetch(record({ email: "flo@example.com" }), 201);
    const created = await createUser("flo", "password123", [], "Flo@Example.com");
    const init = (fetchFn.mock.calls[0] as [string, RequestInit])[1];
    expect(JSON.parse(init.body as string).email).toBe("Flo@Example.com");
    expect(created.email).toBe("flo@example.com");
  });

  it("PUTs only the email when that is the only change, and can clear it", async () => {
    const fetchFn = stubFetch(record({ email: "" }));
    await updateUser("u1", { email: "" });
    const init = (fetchFn.mock.calls[0] as [string, RequestInit])[1];
    expect(JSON.parse(init.body as string)).toEqual({ email: "" });
  });

  it("surfaces the server's duplicate-email message", async () => {
    stubFetch({ errors: ['email "flo@example.com" is already used by another user'] }, 409);
    await expect(updateUser("u2", { email: "flo@example.com" })).rejects.toThrow(/already used/);
  });
});
