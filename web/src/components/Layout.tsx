import { ComponentChildren } from "preact";
import { useLocation } from "preact-iso";
import { useEffect, useRef, useState } from "preact/hooks";
import { getSettings, logout, openExternalURL } from "../api/client";
import { ChangePasswordDialog } from "./ChangePasswordDialog";
import { t, locale, setLocale } from "../i18n";
import { isLicensed, licenseState, loadLicense, productTitle } from "../license";
import { authSession, currentRole, loadAuthSession, loginRequired, seesEverything, userLabel } from "../auth";

const helpURL = "https://opencsg.com/docs/csghub/101/function/csghub-lite/intro";

async function openHelp(event: MouseEvent) {
  event.preventDefault();
  try {
    if (await openExternalURL(helpURL)) return;
  } catch (error) {
    console.error("failed to open help in the system browser", error);
  }
  window.open(helpURL, "_blank", "noopener,noreferrer");
}

type NavItem = {
  id: string;
  path: string;
  key: string;
  icon: () => preact.JSX.Element;
  feature?: string;
  /** "staff" items show only to administrators and auditors, "member" items only to everyone else. */
  audience?: "staff" | "member";
};

// The sidebar groups pages the way LiteLLM's admin UI does. A member, like
// an internal user in LiteLLM, sees only chat and their own API keys; the
// machine's models, datasets, apps and the management pages are for
// administrators and auditors, as OpenRouter keeps provider, member and
// billing settings to administrators.
const navGroups: { id: string; key: string; management?: boolean; items: NavItem[] }[] = [
  {
    id: "workspace",
    key: "nav.groupWorkspace",
    items: [
      { id: "dashboard", path: "/", key: "nav.dashboard", icon: DashboardIcon, audience: "staff" },
      { id: "chat", path: "/chat", key: "nav.chat", icon: ChatIcon },
      { id: "my-keys", path: "/account/keys", key: "nav.myKeys", icon: KeyIcon, audience: "member" },
      { id: "ai-apps", path: "/ai-apps", key: "nav.aiApps", icon: AIAppsIcon, audience: "staff" },
    ],
  },
  {
    id: "models",
    key: "nav.groupModels",
    management: true,
    items: [
      { id: "marketplace", path: "/marketplace", key: "nav.marketplace", icon: MarketplaceIcon },
      { id: "library", path: "/library", key: "nav.library", icon: LibraryIcon },
      { id: "datasets", path: "/datasets", key: "nav.datasets", icon: DatasetsIcon },
    ],
  },
  {
    id: "management",
    key: "nav.groupManagement",
    management: true,
    items: [
      { id: "ai-gateway", path: "/ai-gateway", key: "nav.aiGateway", icon: AIGatewayIcon },
      { id: "cluster", path: "/cluster", key: "nav.cluster", icon: ClusterIcon },
      { id: "observability", path: "/observability", key: "nav.observability", icon: ObservabilityIcon },
      { id: "team", path: "/team", key: "nav.team", icon: TeamIcon },
    ],
  },
];

export function Layout({ children }: { children: ComponentChildren }) {
  const { path, route } = useLocation();
  const [hiddenNavItems, setHiddenNavItems] = useState<Set<string> | null>(null);
  void locale.value;
  const license = licenseState.value;

  useEffect(() => {
    void loadLicense();
  }, []);

  useEffect(() => {
    let active = true;
    getSettings()
      .then((settings) => {
        if (active) setHiddenNavItems(new Set(settings.hidden_nav_items ?? []));
      })
      .catch(() => {
        if (active) setHiddenNavItems(new Set());
      });
    return () => {
      active = false;
    };
  }, []);

  const session = authSession.value;
  const canManage = seesEverything(session);
  const showNavItem = (id: string) => hiddenNavItems !== null && !hiddenNavItems.has(id);
  const forAudience = (item: NavItem) => !item.audience || (item.audience === "staff") === canManage;

  // A member who lands on a page meant for staff, such as the dashboard at
  // "/", is taken to chat.
  useEffect(() => {
    if (!session || canManage) return;
    const staffPage = navGroups.some((group) =>
      group.items.some(
        (item) => (group.management || item.audience === "staff") && (item.path === "/" ? path === "/" : path.startsWith(item.path)),
      ),
    );
    if (staffPage) route("/chat", true);
  }, [session, canManage, path]);
  // A gated page stays in the menu with a lock so people can find the
  // feature; the page itself explains how to unlock it.
  const locked = (feature?: string) => !!feature && !(license?.features ?? []).includes(feature);

  return (
    <div class="flex h-screen overflow-hidden">
      <aside class="w-52 flex-shrink-0 border-r border-gray-200 bg-white flex flex-col">
        <div class="flex items-center gap-2 px-5 py-5">
          <img src="/favicon.svg" alt={productTitle(license)} class="w-8 h-8" />
          <span class="font-semibold text-base text-gray-900" title={isLicensed(license) ? t("settings.licenseEditionEnterprise") : undefined}>
            CSGLite
            {isLicensed(license) && (
              <span class="ml-1.5 inline-flex items-center rounded-md bg-indigo-600 px-1.5 py-0.5 text-[11px] font-bold leading-none tracking-wide text-white align-middle">
                EE
              </span>
            )}
          </span>
        </div>
        <nav class="flex-1 overflow-y-auto px-3 pb-2">
          {navGroups
            .filter((group) => !group.management || canManage)
            .map((group) => {
              const items = group.items.filter((item) => forAudience(item) && showNavItem(item.id));
              if (items.length === 0) return null;
              return (
                <div key={group.id} class="mt-3 first:mt-1">
                  <p class="px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-gray-400">{t(group.key)}</p>
                  <div class="space-y-0.5">
                    {items.map((item) => {
                      const active = path === item.path || (item.path !== "/" && path.startsWith(item.path));
                      return (
                        <a
                          key={item.path}
                          href={item.path}
                          onClick={(event) => {
                            event.preventDefault();
                            route(item.path);
                          }}
                          class={`flex items-center gap-3 rounded-lg pl-5 pr-3 py-2 text-sm font-medium transition-colors ${
                            active ? "bg-indigo-50 text-indigo-700" : "text-gray-600 hover:bg-gray-50 hover:text-gray-900"
                          }`}
                        >
                          <item.icon />
                          <span class="flex-1">{t(item.key)}</span>
                          {locked(item.feature) && <LockIcon />}
                        </a>
                      );
                    })}
                  </div>
                </div>
              );
            })}
        </nav>
        <div class="border-t border-gray-100 py-2">
        <AccountMenu showNavItem={showNavItem} />
        </div>
      </aside>
      <main class="flex min-h-0 flex-1 flex-col overflow-hidden bg-gray-50">
        <div class="min-h-0 flex-1 overflow-auto">{children}</div>
        <div class="flex-shrink-0 py-3 text-xs text-gray-400 text-center">
          &copy; OpenCSG &middot; Powered By OpenCSG
        </div>
      </main>
    </div>
  );
}

/**
 * The profile menu at the foot of the sidebar. Like OpenRouter's account
 * menu it also holds the pages people rarely visit (settings, help), so it
 * shows even while accounts are off. Pricing is linked from the CSGHub
 * provider on the gateway page.
 */
function AccountMenu({ showNavItem }: { showNavItem: (id: string) => boolean }) {
  const session = authSession.value;
  const { route } = useLocation();
  const [open, setOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", close);
    window.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", escape);
    };
  }, [open]);

  // Without accounts, or on a server that predates them, the operator is
  // the only user.
  const signedIn = !!session?.accounts_enabled && !!session.authenticated && !session.local_operator;
  const role = currentRole(session);
  const name = signedIn ? userLabel(session?.user) : t(`team.role.${role}`);
  const detail = signedIn ? session?.user?.email || session?.user?.username || "" : "";

  const signOut = async () => {
    try {
      await logout();
    } finally {
      await loadAuthSession();
      loginRequired.value = true;
    }
  };
  const go = (path: string) => {
    setOpen(false);
    route(path);
  };
  const canManage = seesEverything(session);
  const item = "flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-50";

  return (
    <div ref={menuRef} class="relative mx-3 mb-1">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        class={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm transition-colors ${open ? "bg-gray-100" : "hover:bg-gray-50"}`}
        aria-haspopup="menu"
        aria-expanded={open}
      >
        <Avatar name={signedIn ? name : ""} />
        <span class="min-w-0 flex-1">
          <span class="block truncate font-medium text-gray-900">{name}</span>
          {signedIn && <span class="block truncate text-xs text-gray-500">{t(`team.role.${role}`)}</span>}
        </span>
        <svg class="h-4 w-4 flex-shrink-0 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" d="M8 9l4-4 4 4m0 6l-4 4-4-4" />
        </svg>
      </button>
      {open && (
        <div role="menu" class="absolute bottom-full left-0 z-40 mb-1 w-64 overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl">
          <div class="flex items-center gap-3 border-b border-gray-100 px-3 py-3">
            <Avatar name={signedIn ? name : ""} />
            <div class="min-w-0">
              <p class="truncate text-sm font-semibold text-gray-900">{name}</p>
              {detail && <p class="truncate text-xs text-gray-500">{detail}</p>}
            </div>
            {signedIn && (
              <span class="ml-auto rounded-full bg-indigo-50 px-2 py-0.5 text-[11px] font-medium text-indigo-700">{t(`team.role.${role}`)}</span>
            )}
          </div>
          <div class="py-1">
            <button type="button" role="menuitem" class={item} onClick={() => go(canManage ? "/ai-gateway" : "/account/keys")}>
              {t("account.myKeys")}
            </button>
            {signedIn && (
              <button
                type="button"
                role="menuitem"
                class={item}
                onClick={() => {
                  setOpen(false);
                  setPasswordOpen(true);
                }}
              >
                {t("auth.changePassword")}
              </button>
            )}
          </div>
          {(canManage && showNavItem("settings")) || showNavItem("help") ? (
            <div class="border-t border-gray-100 py-1">
              {canManage && showNavItem("settings") && (
                <button type="button" role="menuitem" class={item} onClick={() => go("/settings")}>
                  {t("nav.settings")}
                </button>
              )}
              {showNavItem("help") && (
                <a
                  href={helpURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  role="menuitem"
                  class={item}
                  onClick={(event) => {
                    setOpen(false);
                    void openHelp(event);
                  }}
                >
                  {t("nav.help")}
                  <svg class="ml-auto h-3.5 w-3.5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M14 5h5v5m0-5l-8 8M10 5H6a1 1 0 00-1 1v12a1 1 0 001 1h12a1 1 0 001-1v-4" />
                  </svg>
                </a>
              )}
            </div>
          ) : null}
          <div class="flex items-center justify-between border-t border-gray-100 px-3 py-2 text-sm text-gray-700">
            <span>{t("account.language")}</span>
            <span class="inline-flex overflow-hidden rounded-lg border border-gray-200 text-xs">
              {(["zh", "en"] as const).map((code) => (
                <button
                  key={code}
                  type="button"
                  onClick={() => setLocale(code)}
                  class={`px-2.5 py-1 ${locale.value === code ? "bg-indigo-600 text-white" : "text-gray-600 hover:bg-gray-50"}`}
                >
                  {code === "zh" ? "中文" : "EN"}
                </button>
              ))}
            </span>
          </div>
          {signedIn && (
            <div class="border-t border-gray-100 py-1">
              <button type="button" role="menuitem" onClick={signOut} class="flex w-full px-3 py-2 text-left text-sm text-red-600 hover:bg-red-50">
                {t("auth.signOut")}
              </button>
            </div>
          )}
        </div>
      )}
      {passwordOpen && <ChangePasswordDialog onClose={() => setPasswordOpen(false)} />}
    </div>
  );
}

// Without a name (no one is signed in) the avatar shows a generic person.
function Avatar({ name }: { name: string }) {
  return (
    <span class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-indigo-100 text-xs font-semibold text-indigo-700">
      {name ? (
        name.slice(0, 1).toUpperCase()
      ) : (
        <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <circle cx="12" cy="8" r="4" />
          <path stroke-linecap="round" d="M4 21a8 8 0 0116 0" />
        </svg>
      )}
    </span>
  );
}

function LockIcon() {
  return (
    <svg class="h-3.5 w-3.5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-label={t("team.lockedNav")}>
      <rect x="5" y="11" width="14" height="10" rx="2" />
      <path stroke-linecap="round" d="M8 11V7a4 4 0 118 0v4" />
    </svg>
  );
}

function TeamIcon() {
  return (
    <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" />
    </svg>
  );
}

function DashboardIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zm10 0a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zm10 0a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z" />
    </svg>
  );
}

function MarketplaceIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M3 3h2l.4 2M7 13h10l4-8H5.4M7 13L5.4 5M7 13l-2.293 2.293c-.63.63-.184 1.707.707 1.707H17m0 0a2 2 0 100 4 2 2 0 000-4zm-8 2a2 2 0 100 4 2 2 0 000-4z" />
    </svg>
  );
}

function LibraryIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
    </svg>
  );
}

function DatasetsIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4m0 5c0 2.21-3.582 4-8 4s-8-1.79-8-4" />
    </svg>
  );
}

function KeyIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z" />
    </svg>
  );
}

function ChatIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
    </svg>
  );
}

function AIAppsIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M19 15l.9 2.6L22.5 18l-2.6.9L19 21l-.9-2.1-2.6-.9 2.6-.4L19 15zM5 15l.9 2.6L8.5 18l-2.6.9L5 21l-.9-2.1-2.6-.9 2.6-.4L5 15z" />
    </svg>
  );
}

function AIGatewayIcon() {
  return (
    <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="16" y="16" width="6" height="6" rx="1" />
      <rect x="2" y="16" width="6" height="6" rx="1" />
      <rect x="9" y="2" width="6" height="6" rx="1" />
      <path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3M12 12V8" />
    </svg>
  );
}

function ObservabilityIcon() {
  return (
    <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 19V9m5 10V5m5 14v-7m5 7V3" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M3 19h18" />
    </svg>
  );
}

function ClusterIcon() {
  return (
    <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <rect x="3" y="4" width="18" height="6" rx="1.5" stroke-linecap="round" stroke-linejoin="round" />
      <rect x="3" y="14" width="18" height="6" rx="1.5" stroke-linecap="round" stroke-linejoin="round" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M7 7h.01M7 17h.01M11 7h4M11 17h4" />
    </svg>
  );
}
