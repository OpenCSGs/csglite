import type { ComponentChildren } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { clearCloudToken, getSettings, openExternalURL } from "../api/client";
import type { AppSettings } from "../api/client";
import {
  cloudAuth,
  cloudAuthError,
  cloudUserLabel,
  communityName,
  communitySettings,
  refreshCloudAuth,
  saveCommunitySelection,
  savingCommunity,
  setCloudAuth,
  waitForCommunitySelection,
} from "../cloudAccount";
import {
  COMMUNITY_SITES,
  CUSTOM_SITE_ID,
  matchCommunitySite,
} from "../data/communitySites";
import { locale, t } from "../i18n";
import { AccountBalance } from "./AccountBalance";

function AccountIcon({
  kind,
}: {
  kind: "user" | "login" | "settings" | "chevron" | "external";
}) {
  const paths = {
    user: "M20 21v-2a7 7 0 00-14 0v2M17 7a4 4 0 11-8 0 4 4 0 018 0",
    login: "M10 17l5-5-5-5M15 12H3M13 3h6a2 2 0 012 2v14a2 2 0 01-2 2h-6",
    settings:
      "M12 8a4 4 0 100 8 4 4 0 000-8M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1z",
    chevron: "m9 5 7 7-7 7",
    external:
      "M14 3h7v7m0-7L10 14M10 3H5a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-5",
  };
  return (
    <svg
      class="h-4 w-4 shrink-0"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      stroke-width="1.5"
      aria-hidden="true"
    >
      <path stroke-linecap="round" stroke-linejoin="round" d={paths[kind]} />
    </svg>
  );
}

function AccountAvatar({ large = false }: { large?: boolean }) {
  const [failed, setFailed] = useState(false);
  const avatar = cloudAuth.value?.user?.avatar;
  const label = cloudUserLabel(cloudAuth.value);
  useEffect(() => setFailed(false), [avatar]);
  const size = large ? "h-12 w-12 text-xl" : "h-8 w-8 text-sm";
  return avatar && !failed ? (
    <img
      src={avatar}
      alt=""
      onError={() => setFailed(true)}
      class={`${size} shrink-0 rounded-full border border-gray-100 object-cover`}
    />
  ) : (
    <span
      class={`${size} flex shrink-0 items-center justify-center rounded-full bg-[#169F95] font-semibold text-white`}
      aria-hidden="true"
    >
      {Array.from(label)[0]?.toUpperCase() || <AccountIcon kind="user" />}
    </span>
  );
}

function AccountDialog({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ComponentChildren;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
  }, []);
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-labelledby="account-dialog-title"
      class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-md overflow-y-auto rounded-2xl border border-gray-200 bg-white p-0 text-gray-900 shadow-2xl backdrop:bg-gray-900/40"
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        const rect = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < rect.left ||
          event.clientX > rect.right ||
          event.clientY < rect.top ||
          event.clientY > rect.bottom
        )
          onClose();
      }}
    >
      <div class="flex items-center justify-between gap-4 border-b border-gray-100 px-6 py-4">
        <h2 id="account-dialog-title" class="text-lg font-semibold">
          {title}
        </h2>
        <button
          type="button"
          onClick={onClose}
          aria-label={t("dash.close")}
          class="rounded-lg p-2 text-gray-400 hover:bg-gray-50 hover:text-gray-700"
        >
          <svg
            class="h-4 w-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2"
            aria-hidden="true"
          >
            <path stroke-linecap="round" d="m6 6 12 12M18 6 6 18" />
          </svg>
        </button>
      </div>
      {children}
    </dialog>
  );
}

const fieldClass =
  "mt-1.5 w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100 disabled:bg-gray-50 disabled:text-gray-500";
const primaryClass =
  "inline-flex items-center justify-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60";

export function SidebarAccount({
  showSettings,
  onSettings,
}: {
  showSettings: boolean;
  onSettings: () => void;
}) {
  void locale.value;
  const auth = cloudAuth.value;
  const settings = communitySettings.value;
  const hasAccount = !!auth?.has_token;
  const [menuOpen, setMenuOpen] = useState(false);
  const [dialog, setDialog] = useState<"login" | "account" | null>(null);
  const [siteID, setSiteID] = useState("main");
  const [serverURL, setServerURL] = useState("");
  const [gatewayURL, setGatewayURL] = useState("");
  const [error, setError] = useState("");
  const [opening, setOpening] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [waiting, setWaiting] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuItemsRef = useRef<HTMLDivElement>(null);
  const loginAttempt = useRef(0);

  useEffect(() => {
    void refreshCloudAuth();
    const onFocus = () => {
      void refreshCloudAuth();
    };
    window.addEventListener("focus", onFocus);
    return () => {
      window.removeEventListener("focus", onFocus);
      loginAttempt.current += 1;
    };
  }, []);

  useLayoutEffect(() => {
    if (!menuOpen) return;
    const outside = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setMenuOpen(false);
    };
    const keyboard = (event: KeyboardEvent) => {
      const items = Array.from(
        menuItemsRef.current?.querySelectorAll<HTMLButtonElement>(
          "button:not(:disabled)",
        ) || [],
      );
      if (event.key === "Escape") {
        event.preventDefault();
        setMenuOpen(false);
        triggerRef.current?.focus();
      }
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        const current = items.indexOf(
          document.activeElement as HTMLButtonElement,
        );
        const index =
          event.key === "Home"
            ? 0
            : event.key === "End"
              ? items.length - 1
              : (current + (event.key === "ArrowUp" ? -1 : 1) + items.length) %
                items.length;
        items[index]?.focus();
      }
      if (event.key === "Tab") setMenuOpen(false);
    };
    menuItemsRef.current?.querySelector<HTMLButtonElement>("button")?.focus();
    window.addEventListener("pointerdown", outside);
    window.addEventListener("keydown", keyboard);
    return () => {
      window.removeEventListener("pointerdown", outside);
      window.removeEventListener("keydown", keyboard);
    };
  }, [menuOpen]);

  useEffect(() => {
    if (!waiting) return;
    let active = true;
    let timer: number | undefined;
    const deadline = Date.now() + 5 * 60 * 1000;
    const poll = async () => {
      const status = await refreshCloudAuth();
      if (!active) return;
      if (status?.authenticated && status.user) {
        setWaiting(false);
        setDialog(null);
        setError("");
        window.dispatchEvent(new Event("csghub:providers-changed"));
      } else if (Date.now() >= deadline) {
        setWaiting(false);
        setError(t("account.loginTimeout"));
      } else {
        timer = window.setTimeout(poll, 1500);
      }
    };
    void poll();
    return () => {
      active = false;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [waiting]);

  function applyDraft(data: AppSettings) {
    setSiteID(
      matchCommunitySite(data.server_url, data.ai_gateway_url, {
        serverURL: data.default_server_url,
        gatewayURL: data.default_ai_gateway_url,
      }),
    );
    setServerURL(
      data.server_url.trim() ||
        data.default_server_url ||
        COMMUNITY_SITES[0].serverURL,
    );
    setGatewayURL(
      data.ai_gateway_url.trim() ||
        data.default_ai_gateway_url ||
        COMMUNITY_SITES[0].gatewayURL,
    );
  }

  async function openLogin() {
    setMenuOpen(false);
    setError("");
    setDialog("login");
    await waitForCommunitySelection();
    if (communitySettings.value) applyDraft(communitySettings.value);
    else {
      try {
        const data = await getSettings();
        communitySettings.value = data;
        applyDraft(data);
      } catch {
        setError(t("account.settingsFailed"));
      }
    }
  }

  function chooseSite(id: string) {
    setSiteID(id);
    const site = COMMUNITY_SITES.find((item) => item.id === id);
    if (!site) return;
    const server =
      site.id === "main"
        ? settings?.default_server_url || site.serverURL
        : site.serverURL;
    const gateway =
      site.id === "main"
        ? settings?.default_ai_gateway_url || site.gatewayURL
        : site.gatewayURL;
    setServerURL(server);
    setGatewayURL(gateway);
    void persistSelection(server, gateway);
  }

  async function persistSelection(server: string, gateway: string) {
    if (hasAccount || opening || waiting) return;
    try {
      if (
        ![server, gateway].every((value) =>
          ["http:", "https:"].includes(new URL(value).protocol),
        )
      )
        return;
    } catch {
      return;
    }
    setError("");
    try {
      await saveCommunitySelection(server.trim(), gateway.trim());
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : t("settings.serviceUrlsSaveFailed"),
      );
    }
  }

  function closeDialog() {
    if (dialog === "login" && siteID === CUSTOM_SITE_ID)
      void persistSelection(serverURL, gatewayURL);
    setDialog(null);
    requestAnimationFrame(() => triggerRef.current?.focus());
  }

  async function login() {
    if (opening || savingCommunity.value || !settings) return;
    const server = serverURL.trim();
    const gateway = gatewayURL.trim();
    try {
      if (
        ![server, gateway].every((value) =>
          ["http:", "https:"].includes(new URL(value).protocol),
        )
      )
        throw new Error();
    } catch {
      setError(t("account.invalidURL"));
      return;
    }
    // Reserve a browser tab before awaiting settings/auth; WebViews use the system browser.
    const tab = settings.desktop_mode ? null : window.open("", "_blank");
    if (tab) tab.opener = null;
    if (!settings.desktop_mode && !tab) {
      setError(t("account.popupBlocked"));
      return;
    }
    setOpening(true);
    setError("");
    const attempt = ++loginAttempt.current;
    try {
      const changingCommunity =
        server !== settings.server_url || gateway !== settings.ai_gateway_url;
      if (changingCommunity) {
        await saveCommunitySelection(server, gateway);
      }
      const status = await refreshCloudAuth();
      if (attempt !== loginAttempt.current) {
        tab?.close();
        return;
      }
      if (!status?.login_url) throw new Error(t("account.loginFailed"));
      if (tab) tab.location.href = status.login_url;
      else if (!(await openExternalURL(status.login_url)))
        throw new Error(t("account.loginFailed"));
      setWaiting(true);
    } catch (err) {
      tab?.close();
      setError(err instanceof Error ? err.message : t("account.loginFailed"));
    } finally {
      setOpening(false);
    }
  }

  async function logout() {
    if (loggingOut) return;
    setLoggingOut(true);
    setError("");
    loginAttempt.current += 1;
    setWaiting(false);
    try {
      setCloudAuth(await clearCloudToken());
      setMenuOpen(false);
      setDialog(null);
      window.dispatchEvent(new Event("csghub:providers-changed"));
    } catch {
      setError(t("account.logoutFailed"));
    } finally {
      setLoggingOut(false);
    }
  }

  async function retry() {
    setRetrying(true);
    await refreshCloudAuth();
    setRetrying(false);
  }

  const label = cloudUserLabel(auth) || t("account.unverified");
  const menuClass =
    "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm text-gray-600 transition-colors hover:bg-gray-50 focus:bg-indigo-50 focus:text-indigo-700 focus:outline-none";
  const accountError = error || cloudAuthError.value;

  return (
    <div class="relative" ref={menuRef}>
      {hasAccount ? (
        <>
          <button
            ref={triggerRef}
            type="button"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            aria-controls="sidebar-account-menu"
            onClick={() => setMenuOpen(!menuOpen)}
            class={`flex w-full items-center gap-2.5 rounded-lg px-2 py-2.5 text-left transition-colors ${menuOpen ? "bg-indigo-50" : "hover:bg-gray-50"}`}
          >
            <AccountAvatar />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm font-medium text-gray-900">
                {label}
              </span>
              <span class="mt-0.5 block truncate text-[11px] text-gray-500">
                {communityName()}
              </span>
            </span>
            <svg
              class="h-3.5 w-3.5 shrink-0 text-gray-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.5"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d={menuOpen ? "m6 15 6-6 6 6" : "m6 9 6 6 6-6"}
              />
            </svg>
          </button>
          {menuOpen && (
            <div
              id="sidebar-account-menu"
              ref={menuItemsRef}
              role="menu"
              aria-label={t("account.menu")}
              class="absolute bottom-full left-0 z-40 mb-2 w-64 max-w-[calc(100vw-1.5rem)] rounded-xl border border-gray-200 bg-white p-2 shadow-xl"
            >
              <div class="border-b border-gray-100 px-3 pb-3 pt-2">
                <p class="truncate text-sm font-semibold text-gray-900">
                  {label}
                </p>
                <p class="mt-1 truncate text-xs text-gray-400">
                  {auth?.user?.username ? `@${auth.user.username} · ` : ""}
                  {communityName()}
                </p>
              </div>
              <button
                type="button"
                role="menuitem"
                class={`${menuClass} mt-1`}
                onClick={() => {
                  setMenuOpen(false);
                  setError("");
                  setDialog("account");
                }}
              >
                <AccountIcon kind="user" />
                <span class="flex-1">{t("account.info")}</span>
                <AccountIcon kind="chevron" />
              </button>
              {showSettings && (
                <button
                  type="button"
                  role="menuitem"
                  class={menuClass}
                  onClick={() => {
                    setMenuOpen(false);
                    onSettings();
                  }}
                >
                  <AccountIcon kind="settings" />
                  <span class="flex-1">{t("nav.settings")}</span>
                  <AccountIcon kind="chevron" />
                </button>
              )}
              {!auth?.authenticated && (
                <button
                  type="button"
                  role="menuitem"
                  class={menuClass}
                  onClick={() => void openLogin()}
                >
                  <AccountIcon kind="login" />
                  {t("account.signInAgain")}
                </button>
              )}
              {accountError && (
                <p class="px-3 py-2 text-xs text-red-600" role="alert">
                  {accountError}
                </p>
              )}
              <div class="mt-1 border-t border-gray-100 pt-1">
                <button
                  type="button"
                  role="menuitem"
                  class={`${menuClass} text-red-600 hover:bg-red-50`}
                  disabled={loggingOut}
                  onClick={() => void logout()}
                >
                  <AccountIcon kind="login" />
                  {t(loggingOut ? "settings.loggingOut" : "settings.logout")}
                </button>
              </div>
            </div>
          )}
        </>
      ) : auth ? (
        <button
          ref={triggerRef}
          type="button"
          onClick={() => void openLogin()}
          class="flex w-full items-center gap-2.5 rounded-lg py-1.5 pl-5 pr-3 text-sm font-normal leading-5 text-gray-600 transition-colors hover:bg-gray-50 hover:text-gray-900 focus-visible:bg-gray-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-indigo-500"
        >
          <AccountIcon kind="login" />
          {t(waiting ? "account.waiting" : "account.login")}
        </button>
      ) : cloudAuthError.value ? (
        <button
          type="button"
          title={cloudAuthError.value}
          onClick={() => void retry()}
          disabled={retrying}
          class="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-gray-500 hover:bg-gray-50"
        >
          <AccountIcon kind="user" />
          {t(retrying ? "account.loading" : "account.retry")}
        </button>
      ) : (
        <div
          role="status"
          aria-label={t("account.loading")}
          class="h-10 animate-pulse rounded-lg bg-gray-50"
        />
      )}

      {dialog === "login" && (
        <AccountDialog title={t("account.loginTitle")} onClose={closeDialog}>
          <form
            class="space-y-5 p-6"
            onSubmit={(event) => {
              event.preventDefault();
              void login();
            }}
          >
            <p class="text-sm text-gray-500">{t("account.loginDescription")}</p>
            {settings ? (
              <>
                <label class="block">
                  <span class="text-sm font-medium text-gray-700">
                    {t("settings.sitePicker")}
                  </span>
                  <select
                    class={fieldClass}
                    value={siteID}
                    disabled={
                      hasAccount || opening || waiting || savingCommunity.value
                    }
                    onChange={(event) => chooseSite(event.currentTarget.value)}
                  >
                    {COMMUNITY_SITES.map((site) => (
                      <option key={site.id} value={site.id}>
                        {t(site.nameKey)}
                      </option>
                    ))}
                    <option value={CUSTOM_SITE_ID}>
                      {t("settings.siteCustom")}
                    </option>
                  </select>
                  <span class="mt-1.5 block text-xs text-gray-400">
                    {t(
                      hasAccount
                        ? "settings.siteSwitchSignedIn"
                        : "account.communityHint",
                    )}
                  </span>
                </label>
                {siteID === CUSTOM_SITE_ID && (
                  <>
                    <label class="block">
                      <span class="text-sm font-medium text-gray-700">
                        {t("settings.serverUrl")}
                      </span>
                      <input
                        type="url"
                        required
                        value={serverURL}
                        disabled={
                          hasAccount ||
                          opening ||
                          waiting ||
                          savingCommunity.value
                        }
                        onInput={(event) =>
                          setServerURL(event.currentTarget.value)
                        }
                        class={fieldClass}
                      />
                    </label>
                    <label class="block">
                      <span class="text-sm font-medium text-gray-700">
                        {t("settings.aiGatewayUrl")}
                      </span>
                      <input
                        type="url"
                        required
                        value={gatewayURL}
                        disabled={
                          hasAccount ||
                          opening ||
                          waiting ||
                          savingCommunity.value
                        }
                        onInput={(event) =>
                          setGatewayURL(event.currentTarget.value)
                        }
                        class={fieldClass}
                      />
                    </label>
                  </>
                )}
                <button
                  type="submit"
                  class={`${primaryClass} w-full`}
                  disabled={opening || waiting || savingCommunity.value}
                >
                  {t(
                    opening
                      ? "account.opening"
                      : savingCommunity.value
                        ? "account.savingCommunity"
                        : waiting
                          ? "account.waiting"
                          : "account.continue",
                  )}
                  <AccountIcon kind="external" />
                </button>
                <p class="text-center text-xs leading-5 text-gray-400">
                  {t("account.browserHint")}
                </p>
              </>
            ) : (
              <button
                type="button"
                class={primaryClass}
                onClick={() => void openLogin()}
              >
                {t("account.retry")}
              </button>
            )}
            {accountError && (
              <p class="text-sm text-red-600" role="alert">
                {accountError}
              </p>
            )}
            {waiting && (
              <button
                type="button"
                onClick={() => {
                  loginAttempt.current += 1;
                  setWaiting(false);
                }}
                class="w-full text-sm text-gray-500 hover:text-gray-800"
              >
                {t("account.cancelWaiting")}
              </button>
            )}
          </form>
        </AccountDialog>
      )}
      {dialog === "account" && (
        <AccountDialog title={t("account.info")} onClose={closeDialog}>
          <div class="space-y-5 p-6">
            <div class="flex items-center gap-3">
              <AccountAvatar large />
              <div class="min-w-0 flex-1">
                <p class="truncate font-semibold">{label}</p>
                <p class="mt-1 truncate text-xs text-gray-500">
                  {auth?.user?.username ? `@${auth.user.username}` : "—"}
                </p>
              </div>
              <span
                class={`rounded-full px-2 py-1 text-xs ${auth?.authenticated ? "bg-emerald-50 text-emerald-700" : "bg-amber-50 text-amber-700"}`}
              >
                {t(
                  auth?.authenticated
                    ? "settings.signedIn"
                    : "account.unverified",
                )}
              </span>
            </div>
            {auth?.authenticated && (
              <AccountBalance
                key={`${auth.user?.uuid || auth.user?.username}:${settings?.server_url}`}
              />
            )}
            <dl class="divide-y divide-gray-100 rounded-xl border border-gray-200 bg-gray-50/60 px-4 text-sm">
              {[
                [t("account.username"), auth?.user?.username],
                [t("account.email"), auth?.user?.email],
                [t("account.community"), communityName()],
                [t("settings.serverUrl"), settings?.server_url],
              ].map(([name, value]) => (
                <div
                  key={name}
                  class="flex flex-wrap justify-between gap-2 py-3"
                >
                  <dt class="text-gray-500">{name}</dt>
                  <dd class="min-w-0 break-all text-gray-800">
                    {value || "—"}
                  </dd>
                </div>
              ))}
            </dl>
            <p class="text-xs text-gray-400">
              {t("settings.siteSwitchSignedIn")}
            </p>
            {!auth?.authenticated && (
              <div class="space-y-3">
                <p class="text-sm text-amber-700">
                  {t("account.unverifiedHint")}
                </p>
                <div class="flex gap-3">
                  <button
                    type="button"
                    disabled={retrying}
                    onClick={() => void retry()}
                    class="text-sm text-indigo-600"
                  >
                    {t(retrying ? "account.loading" : "account.retry")}
                  </button>
                  <button
                    type="button"
                    onClick={() => void openLogin()}
                    class="text-sm text-indigo-600"
                  >
                    {t("account.signInAgain")}
                  </button>
                </div>
              </div>
            )}
            {accountError && (
              <p class="text-sm text-red-600" role="alert">
                {accountError}
              </p>
            )}
            <div class="flex justify-between border-t border-gray-100 pt-5">
              <button
                type="button"
                onClick={() => void logout()}
                disabled={loggingOut}
                class="rounded-lg border border-red-200 px-4 py-2 text-sm text-red-600 hover:bg-red-50 disabled:opacity-60"
              >
                {t(loggingOut ? "settings.loggingOut" : "settings.logout")}
              </button>
              <button type="button" onClick={closeDialog} class={primaryClass}>
                {t("account.done")}
              </button>
            </div>
          </div>
        </AccountDialog>
      )}
    </div>
  );
}
