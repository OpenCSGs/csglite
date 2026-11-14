import { signal } from "@preact/signals";

export interface ConsoleSession {
  authenticated: boolean;
  access_mode: "local" | "lan" | "token";
  role: "administrator";
  instance_name: string;
  expires_at?: string;
}

export const consoleSession = signal<ConsoleSession | null>(null);
export const consoleSessionError = signal(false);
let pending: Promise<void> | null = null;

export function refreshConsoleSession(): Promise<void> {
  if (pending) return pending;
  pending = (async () => {
    try {
      const response = await fetch("/auth/session", { cache: "no-store" });
      if (response.status === 401) { window.location.replace("/login"); return; }
      if (!response.ok) throw new Error("session unavailable");
      consoleSession.value = await response.json();
      consoleSessionError.value = false;
    } catch { consoleSessionError.value = true; }
    finally { pending = null; }
  })();
  return pending;
}
