import { useEffect, useState } from "preact/hooks";
import { getSettings } from "../api/client";

let cachedOrigin: string | undefined;
let pendingOrigin: Promise<string> | undefined;

function currentOrigin(): string {
  return typeof window === "undefined" ? "" : window.location.origin;
}

export function normalizeRuntimeAPIOrigin(value?: string): string {
  if (!value) return "";
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") return "";
    return url.origin;
  } catch {
    return "";
  }
}

export function getRuntimeAPIOrigin(): Promise<string> {
  if (cachedOrigin !== undefined) {
    return Promise.resolve(cachedOrigin);
  }
  if (!pendingOrigin) {
    const fallback = currentOrigin();
    pendingOrigin = getSettings()
      .then((settings) => {
        cachedOrigin = settings.desktop_mode
          ? normalizeRuntimeAPIOrigin(settings.local_api_url) || fallback
          : fallback;
        return cachedOrigin;
      })
      .catch(() => {
        cachedOrigin = fallback;
        return cachedOrigin;
      });
  }
  return pendingOrigin;
}

export function useRuntimeAPIOrigin(): string {
  const [origin, setOrigin] = useState(cachedOrigin ?? currentOrigin());

  useEffect(() => {
    let active = true;
    void getRuntimeAPIOrigin().then((resolved) => {
      if (active) setOrigin(resolved);
    });
    return () => {
      active = false;
    };
  }, []);

  return origin;
}
