import { useEffect, useRef, useState } from "preact/hooks";
import {
  getLicenseUpgrade,
  isCloudLoginRequiredError,
  openExternalURL,
  startLicenseUpgrade,
} from "../api/client";
import { locale, t } from "../i18n";
import { CHECKOUT_POLL_MS, checkoutOutcome, editionLabel, editionOf, licenseState } from "../license";

type Phase = "starting" | "login" | "waiting" | "done" | "paidByOther" | "expired" | "error";

/** Opens url in the system browser, falling back to a new browser tab. */
async function openInBrowser(url: string) {
  try {
    if (await openExternalURL(url)) return;
  } catch (error) {
    console.error("failed to open the link in the system browser", error);
  }
  window.open(url, "_blank", "noopener,noreferrer");
}

/**
 * Walks the user through an Enterprise purchase: it starts a checkout session,
 * opens the CSGHub payment page in the browser and polls until the purchase
 * completes and the server has installed the license.
 */
export function LicenseCheckoutDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  void locale.value;
  const [phase, setPhase] = useState<Phase>("starting");
  const [checkoutURL, setCheckoutURL] = useState("");
  const [loginURL, setLoginURL] = useState("");
  const [ownerName, setOwnerName] = useState("");
  const [buyerName, setBuyerName] = useState("");
  const [message, setMessage] = useState("");
  const [attempt, setAttempt] = useState(0);
  const sidRef = useRef("");

  // Start a checkout session each time the dialog opens or the user retries.
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    sidRef.current = "";
    setPhase("starting");
    setMessage("");
    setOwnerName("");
    setBuyerName("");
    startLicenseUpgrade()
      .then(async (session) => {
        if (cancelled) return;
        sidRef.current = session.sid;
        setCheckoutURL(session.checkout_url);
        setPhase("waiting");
        await openInBrowser(session.checkout_url);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (isCloudLoginRequiredError(err)) {
          setLoginURL(err.loginURL || "");
          setPhase("login");
          return;
        }
        setMessage(err instanceof Error ? err.message : String(err));
        setPhase("error");
      });
    return () => {
      cancelled = true;
    };
  }, [open, attempt]);

  // Poll the purchase until it completes or the session expires.
  useEffect(() => {
    if (!open || phase !== "waiting") return;
    let stopped = false;
    const poll = async () => {
      const sid = sidRef.current;
      if (!sid) return;
      try {
        const resp = await getLicenseUpgrade(sid);
        if (stopped) return;
        if (resp.owner_name) setOwnerName(resp.owner_name);
        switch (checkoutOutcome(resp)) {
          case "waiting":
            return;
          case "expired":
            setPhase("expired");
            return;
          case "paidByOther":
            // The license went to the paying account; nothing installs here.
            if (resp.license) licenseState.value = resp.license;
            setBuyerName(resp.buyer_name || "");
            setMessage("");
            setPhase("paidByOther");
            return;
          case "installed":
            licenseState.value = resp.license!;
            setPhase("done");
            return;
          case "installing":
            // Paid, but the license is not installed yet; the next poll retries.
            setMessage(resp.sync_error ? t("licenseCheckout.syncPending", resp.sync_error) : "");
        }
      } catch (err) {
        if (!stopped) setMessage(err instanceof Error ? err.message : String(err));
      }
    };
    const timer = window.setInterval(() => void poll(), CHECKOUT_POLL_MS);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [open, phase]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  if (!open) return null;

  const retry = () => setAttempt((n) => n + 1);

  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4" onClick={onClose}>
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="license-checkout-title"
        class="w-full max-w-md overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl"
        onClick={(event) => event.stopPropagation()}
      >
        <div class="flex items-start justify-between gap-4 border-b border-gray-100 px-5 py-4">
          <div>
            <h2 id="license-checkout-title" class="text-lg font-semibold text-gray-900">
              {t("licenseCheckout.title")}
            </h2>
            <p class="mt-1 text-sm text-gray-500">{t("licenseCheckout.subtitle")}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700"
            aria-label={t("dash.close")}
            title={t("dash.close")}
          >
            <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div class="space-y-3 px-5 py-4 text-sm leading-6 text-gray-600">
          {phase === "starting" && <p>{t("licenseCheckout.starting")}</p>}
          {phase === "login" && <p>{t("licenseCheckout.loginRequired")}</p>}
          {phase === "waiting" && (
            <>
              <p>{t("licenseCheckout.waiting")}</p>
              {ownerName && <p class="font-medium text-gray-900">{t("licenseCheckout.forAccount", ownerName)}</p>}
              <div class="flex items-center gap-2 text-gray-500">
                <span class="inline-block h-2 w-2 animate-pulse rounded-full bg-indigo-500" />
                {t("licenseCheckout.polling")}
              </div>
            </>
          )}
          {phase === "done" && (
            <p class="font-medium text-green-700">{t("licenseCheckout.done", editionLabel(editionOf(licenseState.value)))}</p>
          )}
          {phase === "paidByOther" && (
            <p class="font-medium text-amber-700">
              {t("licenseCheckout.paidByOther", buyerName || t("licenseCheckout.otherAccount"))}
            </p>
          )}
          {phase === "expired" && <p>{t("licenseCheckout.expired")}</p>}
          {phase === "error" && <p class="text-red-600">{t("licenseCheckout.failed")}</p>}
          {message && <p class="break-words text-xs text-gray-500">{message}</p>}
        </div>

        <div class="flex justify-end gap-3 border-t border-gray-100 bg-gray-50 px-5 py-4">
          {phase === "login" && (
            <>
              {loginURL && (
                <button
                  type="button"
                  onClick={() => void openInBrowser(loginURL)}
                  class="rounded-lg border border-gray-200 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
                >
                  {t("licenseCheckout.signIn")}
                </button>
              )}
              <button
                type="button"
                onClick={retry}
                class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
              >
                {t("licenseCheckout.continue")}
              </button>
            </>
          )}
          {phase === "waiting" && checkoutURL && (
            <button
              type="button"
              onClick={() => void openInBrowser(checkoutURL)}
              class="rounded-lg border border-gray-200 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              {t("licenseCheckout.reopen")}
            </button>
          )}
          {(phase === "expired" || phase === "error") && (
            <button
              type="button"
              onClick={retry}
              class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
            >
              {t("licenseCheckout.retry")}
            </button>
          )}
          <button
            type="button"
            onClick={onClose}
            class={
              phase === "done"
                ? "rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
                : "rounded-lg border border-gray-200 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            }
          >
            {t(phase === "done" ? "licenseCheckout.finish" : "licenseCheckout.close")}
          </button>
        </div>
      </section>
    </div>
  );
}
