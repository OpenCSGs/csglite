import { useEffect, useRef, useState } from "preact/hooks";
import { getCloudAccountBalance, openExternalURL } from "../api/client";
import type { CloudAccountBalance } from "../api/client";
import { cloudAuth, communitySettings } from "../cloudAccount";
import { locale, t } from "../i18n";

export function AccountBalance() {
  const auth = cloudAuth.value;
  const settings = communitySettings.value;
  const language = locale.value;
  const [balance, setBalance] = useState<CloudAccountBalance | null>(null);
  const [loading, setLoading] = useState(true);
  const [linkError, setLinkError] = useState(false);
  const reload = useRef<(() => Promise<void>) | null>(null);

  useEffect(() => {
    let active = true;
    let request = 0;
    setBalance(null);
    setLinkError(false);
    const load = async () => {
      const id = ++request;
      setLoading(true);
      try {
        const data = await getCloudAccountBalance();
        if (active && id === request) {
          const sameAccount =
            !auth?.user?.uuid || data.user_uuid === auth.user.uuid;
          setBalance(sameAccount ? data : null);
        }
      } catch {
        if (active && id === request) setBalance(null);
      } finally {
        if (active && id === request) setLoading(false);
      }
    };
    reload.current = load;
    void load();
    const onFocus = () => {
      void load();
    };
    window.addEventListener("focus", onFocus);
    return () => {
      active = false;
      reload.current = null;
      window.removeEventListener("focus", onFocus);
    };
  }, [auth?.user?.uuid, auth?.user?.username, settings?.server_url]);

  const amount = balance?.balance;
  const available = typeof amount === "number" && Number.isFinite(amount);
  const rechargeURL = balance?.recharge_url || auth?.recharge_url;
  const formatted = available
    ? new Intl.NumberFormat(language === "zh" ? "zh-CN" : "en-US", {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      }).format(amount / 100)
    : "";

  return (
    <section
      aria-label={t("account.balance")}
      class="rounded-xl border border-indigo-100 bg-indigo-50/40 p-4"
    >
      <div class="flex items-center justify-between gap-3">
        <h3 class="text-sm font-medium text-gray-600">
          {t("account.balance")}
        </h3>
        <button
          type="button"
          disabled={loading}
          onClick={() => void reload.current?.()}
          class="rounded px-1 py-0.5 text-xs text-gray-500 hover:text-indigo-600 disabled:opacity-50"
        >
          {t(loading ? "account.balanceLoading" : "account.refreshBalance")}
        </button>
      </div>
      <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
        <p
          aria-live="polite"
          class={
            available
              ? "text-2xl font-semibold tabular-nums text-gray-900"
              : "text-sm text-gray-500"
          }
        >
          {loading && !available
            ? "…"
            : available
              ? formatted
              : t("account.balanceUnavailable")}
        </p>
        {rechargeURL && (
          <a
            href={rechargeURL}
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1.5 rounded-lg bg-white px-3 py-1.5 text-sm font-medium text-indigo-600 ring-1 ring-indigo-200 transition-colors hover:bg-indigo-50"
            onClick={async (event) => {
              if (!settings?.desktop_mode) return;
              event.preventDefault();
              setLinkError(false);
              try {
                if (await openExternalURL(rechargeURL)) return;
                throw new Error();
              } catch {
                setLinkError(true);
              }
            }}
          >
            {t("account.recharge")}
            <svg
              class="h-3.5 w-3.5"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.5"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M14 3h7v7m0-7L10 14M10 3H5a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-5"
              />
            </svg>
          </a>
        )}
      </div>
      <p class="mt-2 text-xs text-gray-400">{t("account.balanceUnitHint")}</p>
      {linkError && (
        <p role="alert" class="mt-2 text-xs text-red-600">
          {t("account.rechargeOpenFailed")}
        </p>
      )}
    </section>
  );
}
