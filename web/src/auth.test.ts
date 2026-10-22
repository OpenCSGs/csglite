import { afterEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import type { AuthSession, TeamUser } from "./api/client";
import { currentRole, installAuthInterceptor, isAdmin, loginRequired, mustChangePassword, seesEverything, userLabel } from "./auth";

function user(role: TeamUser["role"]): TeamUser {
  return { id: "u1", username: "alice", role, disabled: false, pending: false, must_change_password: false, created_at: "", updated_at: "" };
}

function session(patch: Partial<AuthSession>): AuthSession {
  return { accounts_enabled: true, feature_licensed: true, authenticated: true, local_operator: false, ...patch };
}

describe("roles", () => {
  it("treats everyone as an administrator while accounts are off", () => {
    expect(currentRole(null)).toBe("admin");
    expect(currentRole(session({ accounts_enabled: false }))).toBe("admin");
  });

  it("gives the local operator administrator rights", () => {
    expect(isAdmin(session({ local_operator: true }))).toBe(true);
  });

  it("uses the signed-in user's role", () => {
    expect(currentRole(session({ user: user("member") }))).toBe("member");
    expect(isAdmin(session({ user: user("auditor") }))).toBe(false);
    expect(isAdmin(session({ user: user("admin") }))).toBe(true);
  });

  it("lets administrators and auditors into the management pages", () => {
    expect(seesEverything(session({ user: user("auditor") }))).toBe(true);
    expect(seesEverything(session({ user: user("admin") }))).toBe(true);
    expect(seesEverything(session({ user: user("member") }))).toBe(false);
    expect(seesEverything(null)).toBe(true);
  });

  it("asks for a new password only from a signed-in user who must change it", () => {
    expect(mustChangePassword(session({ user: { ...user("member"), must_change_password: true } }))).toBe(true);
    expect(mustChangePassword(session({ user: user("member") }))).toBe(false);
    expect(mustChangePassword(session({ local_operator: true, user: { ...user("admin"), must_change_password: true } }))).toBe(false);
  });

  it("labels a user by display name, then username", () => {
    expect(userLabel({ username: "alice" })).toBe("alice");
    expect(userLabel({ username: "alice", display_name: " Alice L " })).toBe("Alice L");
  });
});

describe("auth interceptor", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    loginRequired.value = false;
  });

  it("returns to the sign-in page only on a login_required refusal", async () => {
    const responses: Response[] = [
      new Response(JSON.stringify({ error: "wrong password", errorCode: 401 }), { status: 401, headers: { "content-type": "application/json" } }),
      new Response(JSON.stringify({ error: "sign in", errorCode: 401, code: "login_required" }), {
        status: 401,
        headers: { "content-type": "application/json" },
      }),
    ];
    vi.stubGlobal("window", {
      location: { href: "http://localhost:11435/chat", origin: "http://localhost:11435" },
      fetch: async () => responses.shift()!,
    });
    installAuthInterceptor();

    await window.fetch("/api/auth/login");
    expect(loginRequired.value).toBe(false);
    const resp = await window.fetch("/api/settings");
    expect(loginRequired.value).toBe(true);
    // The caller still reads the body it was given.
    expect((await resp.json()).code).toBe("login_required");
  });
});
