/**
 * Clipboard API polyfill for non-secure contexts.
 *
 * `navigator.clipboard` is only exposed in secure contexts (HTTPS or
 * localhost). When the gateway is reached over plain HTTP from a remote
 * address, `navigator.clipboard` is undefined and every
 * `navigator.clipboard.writeText(...)` call across the app silently fails.
 *
 * This polyfill installs a minimal `writeText` implementation backed by the
 * legacy `document.execCommand("copy")` path only when the native API is
 * missing, so localhost / HTTPS keep using the real Clipboard API untouched.
 * Import once from the app entry; no call site needs to change.
 */
function legacyCopy(text: string): boolean {
  if (typeof document === "undefined") return false;
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.top = "0";
  textarea.style.left = "0";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  document.body.removeChild(textarea);
  return ok;
}

if (typeof navigator !== "undefined" && !navigator.clipboard) {
  const writeText = (text: string): Promise<void> => {
    try {
      if (legacyCopy(text)) return Promise.resolve();
    } catch {
      // fall through to rejection
    }
    return Promise.reject(new Error("Clipboard API unavailable"));
  };
  try {
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
  } catch {
    // Some browsers lock navigator.clipboard; nothing more we can do.
  }
}
