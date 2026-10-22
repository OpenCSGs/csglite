import { signal } from "@preact/signals";
import { getAuthSession, LOGIN_REQUIRED, PASSWORD_CHANGE_REQUIRED } from "./api/client";
import type { AuthSession, TeamRole } from "./api/client";

/**
 * Who is using the web UI. Null until the first check returns; a server that
 * predates accounts answers 404 and is treated as accounts off.
 */
export const authSession = signal<AuthSession | null>(null);
export const authChecked = signal(false);
/** Set when any request is refused with `login_required`. */
export const loginRequired = signal(false);

export async function loadAuthSession(): Promise<AuthSession | null> {
  try {
    const session = await getAuthSession();
    authSession.value = session;
    loginRequired.value = session.accounts_enabled && !session.authenticated;
    return session;
  } catch {
    authSession.value = null;
    return null;
  } finally {
    authChecked.value = true;
  }
}

/** The caller's role; everyone is an administrator while accounts are off. */
export function currentRole(session: AuthSession | null): TeamRole {
  if (!session || !session.accounts_enabled || session.local_operator) return "admin";
  return session.user?.role ?? "member";
}

export function isAdmin(session: AuthSession | null): boolean {
  return currentRole(session) === "admin";
}

/** Administrators and auditors see the management pages and every record. */
export function seesEverything(session: AuthSession | null): boolean {
  const role = currentRole(session);
  return role === "admin" || role === "auditor";
}

/** True while the signed-in user must replace a password someone else chose. */
export function mustChangePassword(session: AuthSession | null): boolean {
  return !!session?.accounts_enabled && !session.local_operator && !!session.user?.must_change_password;
}

let interceptorInstalled = false;

/**
 * Watches every same-origin response for a `login_required` refusal, so a
 * session that expires mid-use returns the user to the sign-in page instead
 * of leaving each page to show its own error, and for
 * `password_change_required`, which brings up the password change screen.
 */
export function installAuthInterceptor() {
  if (interceptorInstalled || typeof window === "undefined") return;
  interceptorInstalled = true;
  const original = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const resp = await original(input, init);
    if ((resp.status === 401 || resp.status === 403) && isSameOrigin(input) && (resp.headers.get("content-type") || "").includes("application/json")) {
      try {
        const body = (await resp.clone().json()) as { code?: unknown };
        if (resp.status === 401 && body.code === LOGIN_REQUIRED) loginRequired.value = true;
        if (resp.status === 403 && body.code === PASSWORD_CHANGE_REQUIRED) void loadAuthSession();
      } catch {
        /* not a JSON error body */
      }
    }
    return resp;
  };
}

function isSameOrigin(input: RequestInfo | URL): boolean {
  const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  try {
    return new URL(raw, window.location.href).origin === window.location.origin;
  } catch {
    return false;
  }
}

/** A user's display label: display name, falling back to the username. */
export function userLabel(user: { display_name?: string; username: string } | undefined | null): string {
  if (!user) return "";
  return user.display_name?.trim() || user.username;
}
