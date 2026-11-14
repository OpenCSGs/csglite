import { ComponentChildren } from "preact";
import type { JSX } from "preact";
import { useLocation } from "preact-iso";
import { useEffect, useState } from "preact/hooks";
import { getSettings } from "../api/client";
import { SidebarAccount } from "./SidebarAccount";
import { communitySettings } from "../cloudAccount";
import { t, locale } from "../i18n";
import { isLicensed, licenseState, loadLicense, productTitle } from "../license";

type NavItem = {
  id: string;
  path: string;
  key: string;
  icon: () => JSX.Element;
};

// Grouped the way the team-access sidebar is: a quiet label, then items
// indented past it. Pricing lives on the CSGHub provider, not in this list.
const navGroups: { id: string; key: string; items: NavItem[] }[] = [
  {
    id: "workspace",
    key: "nav.groupWorkspace",
    items: [
      { id: "dashboard", path: "/", key: "nav.dashboard", icon: DashboardIcon },
      { id: "chat", path: "/chat", key: "nav.chat", icon: ChatIcon },
      { id: "ai-apps", path: "/ai-apps", key: "nav.aiApps", icon: AIAppsIcon },
    ],
  },
  {
    id: "models",
    key: "nav.groupModels",
    items: [
      { id: "marketplace", path: "/marketplace", key: "nav.marketplace", icon: MarketplaceIcon },
      { id: "library", path: "/library", key: "nav.library", icon: LibraryIcon },
      { id: "datasets", path: "/datasets", key: "nav.datasets", icon: DatasetsIcon },
    ],
  },
  {
    id: "management",
    key: "nav.groupManagement",
    items: [
      { id: "ai-gateway", path: "/ai-gateway", key: "nav.aiGateway", icon: AIGatewayIcon },
      { id: "cluster", path: "/cluster", key: "nav.cluster", icon: ClusterIcon },
      { id: "edge", path: "/edge", key: "nav.edge", icon: EdgeIcon },
      { id: "observability", path: "/observability", key: "nav.observability", icon: ObservabilityIcon },
    ],
  },
];

function navItemActive(path: string, itemPath: string): boolean {
  return path === itemPath || (itemPath !== "/" && path.startsWith(itemPath));
}

function NavLink({ item, path, onNavigate }: { item: NavItem; path: string; onNavigate: (path: string) => void }) {
  const active = navItemActive(path, item.path);
  return (
    <a
      href={item.path}
      onClick={(event) => {
        event.preventDefault();
        onNavigate(item.path);
      }}
      class={`flex items-center gap-2.5 rounded-lg py-1.5 pl-5 pr-3 text-sm leading-5 font-normal transition-colors ${
        active
          ? "bg-indigo-50 text-indigo-700"
          : "text-gray-600 hover:bg-gray-50 hover:text-gray-900"
      }`}
    >
      <item.icon />
      {t(item.key)}
    </a>
  );
}

function SettingsIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
    </svg>
  );
}

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
        if (active) {
          communitySettings.value = settings;
          setHiddenNavItems(new Set(settings.hidden_nav_items ?? []));
        }
      })
      .catch(() => {
        if (active) setHiddenNavItems(new Set());
      });
    return () => {
      active = false;
    };
  }, []);

  const showNavItem = (id: string) => hiddenNavItems !== null && !hiddenNavItems.has(id);

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
          {navGroups.map((group) => {
            const items = group.items.filter((item) => showNavItem(item.id));
            if (items.length === 0) return null;
            return (
              <div key={group.id} class="mt-3 first:mt-1">
                <p class="px-3 pb-1 text-xs font-medium text-gray-400">{t(group.key)}</p>
                <div class="space-y-0.5">
                  {items.map((item) => (
                    <NavLink key={item.path} item={item} path={path} onNavigate={route} />
                  ))}
                </div>
              </div>
            );
          })}
        </nav>
        <div class="space-y-0.5 border-t border-gray-100 px-3 py-2">
          {showNavItem("settings") && (
            <NavLink
              item={{ id: "settings", path: "/settings", key: "nav.settings", icon: SettingsIcon }}
              path={path}
              onNavigate={route}
            />
          )}
          <SidebarAccount />
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

function DashboardIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zm10 0a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zm10 0a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z" />
    </svg>
  );
}

function MarketplaceIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M3 3h2l.4 2M7 13h10l4-8H5.4M7 13L5.4 5M7 13l-2.293 2.293c-.63.63-.184 1.707.707 1.707H17m0 0a2 2 0 100 4 2 2 0 000-4zm-8 2a2 2 0 100 4 2 2 0 000-4z" />
    </svg>
  );
}

function LibraryIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
    </svg>
  );
}

function DatasetsIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4m0 5c0 2.21-3.582 4-8 4s-8-1.79-8-4" />
    </svg>
  );
}

function ChatIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
    </svg>
  );
}

function AIAppsIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M19 15l.9 2.6L22.5 18l-2.6.9L19 21l-.9-2.1-2.6-.9 2.6-.4L19 15zM5 15l.9 2.6L8.5 18l-2.6.9L5 21l-.9-2.1-2.6-.9 2.6-.4L5 15z" />
    </svg>
  );
}

function AIGatewayIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="16" y="16" width="6" height="6" rx="1" />
      <rect x="2" y="16" width="6" height="6" rx="1" />
      <rect x="9" y="2" width="6" height="6" rx="1" />
      <path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3M12 12V8" />
    </svg>
  );
}

function ObservabilityIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 19V9m5 10V5m5 14v-7m5 7V3" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M3 19h18" />
    </svg>
  );
}

function EdgeIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 12v6m0-6l-2.5 2.5M12 12l2.5 2.5" />
    </svg>
  );
}

function ClusterIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <rect x="3" y="4" width="18" height="6" rx="1.5" stroke-linecap="round" stroke-linejoin="round" />
      <rect x="3" y="14" width="18" height="6" rx="1.5" stroke-linecap="round" stroke-linejoin="round" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M7 7h.01M7 17h.01M11 7h4M11 17h4" />
    </svg>
  );
}
