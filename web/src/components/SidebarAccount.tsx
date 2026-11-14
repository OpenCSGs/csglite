import type { ComponentChildren } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { clearCloudToken, getSettings, openExternalURL, saveCloudToken } from "../api/client";
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
import { consoleSession, consoleSessionError, refreshConsoleSession } from "../consoleAccess";
import { logoutPublicSession } from "../utils/publicSession";

function AccountIcon({
  kind,
}: {
  kind: "user" | "login" | "settings" | "chevron" | "external" | "shield" | "unlink";
}) {
  const paths = {
    shield: "M12 3 4 6v6c0 4 4 7 8 9 4-2 8-5 8-9V6l-8-3Zm-3 9 2 2 4-4",
    unlink: "M8 10 5.5 12.5a4 4 0 0 0 6 6L14 16M16 14l2.5-2.5a4 4 0 0 0-6-6L10 8M3 3l18 18",
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

export function SidebarAccount() {
  void locale.value;
  const auth = cloudAuth.value;
  const settings = communitySettings.value;
  const hasAccount = !!auth?.has_token;
  const session = consoleSession.value;
  const [consoleError, setConsoleError] = useState("");
  const [leavingConsole, setLeavingConsole] = useState(false);
  const [platformToken, setPlatformToken] = useState("");
  const [menuOpen, setMenuOpen] = useState(false);
  const [dialog, setDialog] = useState<"login" | "account" | "disconnect" | null>(null);
  const [disconnectReturn, setDisconnectReturn] = useState<"account" | null>(null);
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
    void refreshConsoleSession();
    const onFocus = () => {
      void refreshCloudAuth();
      void refreshConsoleSession();
    };
    const onProvidersChanged = () => {
      void refreshCloudAuth({ force: true });
    };
    const onConnect = () => { void openLogin(); };
    window.addEventListener("csghub:connect", onConnect);
    window.addEventListener("focus", onFocus);
    window.addEventListener("csghub:providers-changed", onProvidersChanged);
    return () => {
      window.removeEventListener("csghub:connect", onConnect);
      window.removeEventListener("focus", onFocus);
      window.removeEventListener(
        "csghub:providers-changed",
        onProvidersChanged,
      );
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
      const status = await refreshCloudAuth({ force: true });
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
    setPlatformToken("");
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
    if (session?.access_mode !== "local") {
      if (!platformToken.trim()) { setError(t("access.platformTokenRequired")); return; }
      setOpening(true); setError("");
      try {
        if (server !== settings.server_url || gateway !== settings.ai_gateway_url) await saveCommunitySelection(server, gateway);
        setCloudAuth(await saveCloudToken(platformToken.trim()));
        setPlatformToken(""); setDialog(null);
        window.dispatchEvent(new Event("csghub:providers-changed"));
      } catch { setError(t("account.loginFailed")); }
      finally { setOpening(false); }
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
      const status = await refreshCloudAuth({ force: true });
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

  function requestDisconnect() {
    setDisconnectReturn(dialog === "account" ? "account" : null);
    setMenuOpen(false);
    setError("");
    setDialog("disconnect");
  }

  function cancelDisconnect() {
    setError("");
    setDialog(disconnectReturn);
    if (!disconnectReturn) requestAnimationFrame(() => triggerRef.current?.focus());
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
    await refreshCloudAuth({ force: true });
    setRetrying(false);
  }

  const label = cloudUserLabel(auth) || t("account.unverified");
  const menuClass =
    "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm text-gray-600 transition-colors hover:bg-gray-50 focus:bg-indigo-50 focus:text-indigo-700 focus:outline-none";
  const accountError = error || cloudAuthError.value;

  return (
    <div class="relative" ref={menuRef}>
      <button ref={triggerRef} type="button" aria-haspopup="menu" aria-expanded={menuOpen}
        aria-controls="sidebar-account-menu" onClick={() => setMenuOpen(!menuOpen)}
        class={`flex w-full items-center gap-2.5 rounded-lg px-3 py-2.5 text-left transition-colors ${menuOpen ? "bg-indigo-50" : "hover:bg-gray-50"}`}>
        <span class="flex h-8 w-8 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600"><AccountIcon kind="shield" /></span>
        <span class="min-w-0 flex-1">
          <span class="block text-sm font-medium text-gray-900">{t("access.title")}</span>
          <span class="mt-0.5 block truncate text-[11px] text-gray-500">{session ? t(`access.${session.access_mode}`) : t("access.loading")}</span>
        </span>
        <AccountIcon kind="chevron" />
      </button>
      {menuOpen && (
        <div id="sidebar-account-menu" ref={menuItemsRef} role="menu" aria-label={t("access.title")}
          class="absolute bottom-full left-0 z-40 mb-2 max-h-[calc(100dvh-7rem)] w-80 max-w-[calc(100vw-1.5rem)] overflow-y-auto rounded-2xl border border-gray-200/80 bg-white p-2 shadow-xl shadow-gray-200/60">
          <section class="rounded-xl bg-gray-50 px-4 py-3.5">
            <div class="flex items-center justify-between gap-2">
              <p class="text-xs font-medium text-gray-500">{t("access.console")}</p>
              <span class="text-gray-400"><AccountIcon kind="shield" /></span>
            </div>
            <p class="mt-2 truncate text-sm font-semibold text-gray-900">{session?.instance_name || t("access.loading")}</p>
            {session && <p class="mt-1 text-xs text-gray-500">{t(`access.${session.access_mode}`)}</p>}
            {session?.expires_at && <p class="mt-2 text-xs text-gray-400">{t("access.expires", new Date(session.expires_at).toLocaleString(locale.value === "zh" ? "zh-CN" : "en-US"))}</p>}
            {consoleSessionError.value && <button role="menuitem" class={menuClass} onClick={() => void refreshConsoleSession()}>{t("access.retry")}</button>}
            {session?.access_mode === "token" && <button role="menuitem" type="button" class={`${menuClass} mt-2`} disabled={leavingConsole}
              onClick={async () => { setLeavingConsole(true); setConsoleError(""); try { await logoutPublicSession(); } catch { setConsoleError(t("publicLogin.logoutFailed")); } finally { setLeavingConsole(false); } }}>
              <AccountIcon kind="login" />{t(leavingConsole ? "access.leaving" : "publicLogin.signOut")}
            </button>}
            {consoleError && <p class="mt-2 text-xs text-red-600" role="alert">{consoleError}</p>}
          </section>
          <section class="px-3 pb-3 pt-5">
            <div class="flex items-center justify-between gap-2">
              <p class="min-w-0 truncate text-sm font-semibold text-gray-900">{communityName()}</p>
              <span class={`inline-flex items-center gap-1.5 text-[11px] ${auth?.authenticated ? "text-emerald-600" : "text-gray-400"}`}>
                <span class="h-1.5 w-1.5 rounded-full bg-current" />
                {t(auth?.authenticated ? "access.connected" : hasAccount ? "access.unverified" : "access.disconnected")}
              </span>
            </div>
            {hasAccount && <div class="mt-3 flex items-center gap-3 rounded-xl border border-gray-100 bg-gray-50/60 px-3 py-3"><AccountAvatar /><div class="min-w-0"><p class="truncate text-sm font-medium text-gray-900">{label}</p><p class="truncate text-xs text-gray-500">{auth?.user?.username ? `@${auth.user.username}` : t("access.unverified")}</p></div></div>}
            <p class="mb-3 mt-3 text-xs leading-5 text-gray-400">{t("access.instanceShared")}</p>
            {hasAccount && <div class="flex items-center gap-2">
              <button role="menuitem" type="button" class="flex min-w-0 flex-1 items-center justify-center gap-1.5 rounded-lg border border-gray-200 bg-white px-2 py-2 text-xs font-medium text-gray-600 transition-colors hover:border-indigo-200 hover:bg-indigo-50 hover:text-indigo-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-indigo-500" onClick={() => { setMenuOpen(false); setError(""); setDialog("account"); }}>{t("access.details")}</button>
              <button role="menuitem" type="button" class="flex items-center justify-center gap-1.5 rounded-lg border border-gray-200 bg-white px-2.5 py-2 text-xs font-medium text-gray-500 transition-colors hover:border-red-200 hover:bg-red-50 hover:text-red-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-indigo-500" aria-label={t("access.disconnect")} disabled={loggingOut} onClick={requestDisconnect}><AccountIcon kind="unlink" />{t("access.disconnectShort")}</button>
            </div>}
            {(!hasAccount || !auth?.authenticated) && <button role="menuitem" type="button" class={menuClass} onClick={() => void openLogin()}><AccountIcon kind="login" />{t(hasAccount ? "account.signInAgain" : "account.login")}</button>}
            {accountError && <div class="mt-2"><p class="text-xs text-red-600" role="alert">{accountError}</p><button role="menuitem" class={menuClass} disabled={retrying} onClick={() => void retry()}>{t(retrying ? "account.loading" : "account.retry")}</button></div>}
          </section>
        </div>
      )}

      {dialog === "disconnect" && (
        <AccountDialog title={t("access.disconnectTitle")} onClose={cancelDisconnect}>
          <div class="p-6">
            <div class="flex items-center gap-3 rounded-xl border border-gray-100 bg-gray-50 p-3.5">
              <AccountAvatar />
              <div class="min-w-0"><p class="truncate text-sm font-semibold text-gray-900">{label}</p><p class="mt-0.5 truncate text-xs text-gray-500">{communityName()}</p></div>
            </div>
            <p class="mt-5 text-sm leading-6 text-gray-600">{t("access.disconnectImpact")}</p>
            <p class="mt-3 rounded-lg bg-indigo-50/70 px-3 py-2.5 text-xs leading-5 text-indigo-700">{t("access.consoleRetained")}</p>
            {error && <p class="mt-4 text-sm text-red-600" role="alert">{error}</p>}
            <div class="mt-6 flex justify-end gap-3">
              <button type="button" disabled={loggingOut} onClick={cancelDisconnect} class="rounded-lg border border-gray-200 bg-white px-4 py-2.5 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-50 disabled:opacity-50">{t("access.keepConnection")}</button>
              <button type="button" disabled={loggingOut} onClick={() => void logout()} class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-red-700 disabled:cursor-wait disabled:opacity-50"><AccountIcon kind="unlink" />{t(loggingOut ? "access.disconnecting" : "access.confirmDisconnect")}</button>
            </div>
          </div>
        </AccountDialog>
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
                {session?.access_mode !== "local" && <label class="block"><span class="text-sm font-medium text-gray-700">{t("access.platformToken")}</span><input type="password" autoComplete="off" required class={fieldClass} value={platformToken} onInput={(event) => setPlatformToken(event.currentTarget.value)} /><span class="mt-2 block text-xs text-gray-400">{t("access.platformTokenHint")}</span></label>}
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
                          : session?.access_mode === "local" ? "account.continue" : "account.login",
                  )}
                  <AccountIcon kind="external" />
                </button>
                <p class="text-center text-xs leading-5 text-gray-400">
                  {t(session?.access_mode === "local" ? "account.browserHint" : "access.platformTokenHint")}
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
                    ? "access.connected"
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
                onClick={requestDisconnect}
                disabled={loggingOut}
                class="rounded-lg border border-red-200 px-4 py-2 text-sm text-red-600 hover:bg-red-50 disabled:opacity-60"
              >
                {t(loggingOut ? "access.disconnecting" : "access.disconnect")}
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
