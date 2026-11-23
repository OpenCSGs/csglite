import { signal } from "@preact/signals";

export type ConsoleAccessMode = "desktop" | "local" | "lan" | "token";

// Runtime metadata only. The server remains the authority for every request.
export const consoleAccessMode = signal<ConsoleAccessMode | null>(null);

export function publicCSRFToken(): string | undefined {
  const url = new URL(window.location.href);
  const prefix = url.protocol === "http:" ? "csglite_http_csrf=" : "__Host-csglite_csrf=";
  return document.cookie.split(";").map((entry) => entry.trim())
    .find((entry) => entry.startsWith(prefix))
    ?.slice(prefix.length);
}

export function shouldUsePublicLogin(): boolean {
  const mode = consoleAccessMode.value;
  if (mode !== null) return mode === "token";
  // A console CSRF cookie can justify a redirect before metadata arrives.
  return !!publicCSRFToken();
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
