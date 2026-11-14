// The login token never enters browser storage. Only the server's HttpOnly
// session cookie and its separate CSRF cookie are used after login.
export function hasPublicSession(): boolean {
  return document.cookie.split(";").some((entry) => entry.trim().startsWith("__Host-csglite_csrf="));
}

const nativeFetch = window.fetch.bind(window);
let sessionCheck: Promise<boolean> | null = null;

// A cloud account or provider can return 401 while console access is still
// valid. Only the console's own session endpoint can end that session.
function consoleSessionExpired(): Promise<boolean> {
  if (!sessionCheck) {
    sessionCheck = nativeFetch("/auth/session", { credentials: "same-origin", cache: "no-store" })
      .then((response) => response.status === 401)
      .catch(() => false)
      .finally(() => { sessionCheck = null; });
  }
  return sessionCheck;
}
window.fetch = async (input, init) => {
  const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
  const publicSession = url.origin === window.location.origin && hasPublicSession();
  if (publicSession) {
    const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
    if (method !== "GET" && method !== "HEAD") {
      const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
      const cookie = document.cookie.split(";").map((entry) => entry.trim()).find((entry) => entry.startsWith("__Host-csglite_csrf="));
      if (cookie) headers.set("X-CSGLite-CSRF", cookie.slice("__Host-csglite_csrf=".length));
      init = { ...init, headers };
    }
  }
  const response = await nativeFetch(input, init);
  if (publicSession && response.status === 401 && !url.pathname.startsWith("/auth/")) {
    if (await consoleSessionExpired()) window.location.replace("/login");
  }
  return response;
};

export async function logoutPublicSession(): Promise<void> {
  const response = await fetch("/auth/logout", { method: "POST" });
  if (response.ok || response.status === 401) window.location.replace("/login");
  else throw new Error("logout failed");
}
