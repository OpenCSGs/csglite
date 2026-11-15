import { signal } from "@preact/signals";

export type ConsoleAccessMode = "desktop" | "local" | "lan" | "token";

// Runtime metadata only. The server remains the authority for every request.
export const consoleAccessMode = signal<ConsoleAccessMode | null>(null);

export function publicCSRFToken(): string | undefined {
  return document.cookie.split(";").map((entry) => entry.trim())
    .find((entry) => entry.startsWith("__Host-csglite_csrf="))
    ?.slice("__Host-csglite_csrf=".length);
}

export function shouldUsePublicLogin(): boolean {
  const mode = consoleAccessMode.value;
  if (mode !== null) return mode === "token";
  // Before metadata arrives, only a secure console session cookie can justify a
  // redirect. Never turn a failed desktop bootstrap into a login page.
  const url = new URL(window.location.href);
  return url.protocol === "https:" && !!publicCSRFToken();
}

export function publicSessionHeaders(url: string, method: string, headers?: HeadersInit): Headers {
  const result = new Headers(headers);
  const target = new URL(url, window.location.href);
  if (target.origin === window.location.origin && !["GET", "HEAD"].includes(method.toUpperCase())) {
    const csrf = publicCSRFToken();
    if (csrf) result.set("X-CSGLite-CSRF", csrf);
  }
  return result;
}
