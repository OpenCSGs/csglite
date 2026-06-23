import { ComponentChildren } from "preact";
import { useLocation } from "preact-iso";
import { t, locale } from "../i18n";

const navKeys = [
  { path: "/", key: "nav.dashboard", icon: DashboardIcon },
  { path: "/marketplace", key: "nav.marketplace", icon: MarketplaceIcon },
  { path: "/library", key: "nav.library", icon: LibraryIcon },
  { path: "/datasets", key: "nav.datasets", icon: DatasetsIcon },
  { path: "/chat", key: "nav.chat", icon: ChatIcon },
  { path: "/images", key: "nav.images", icon: ImagesIcon },
  { path: "/ai-apps", key: "nav.aiApps", icon: AIAppsIcon },
  { path: "/ai-gateway", key: "nav.aiGateway", icon: AIGatewayIcon },
];

function SettingsIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
    </svg>
  );
}

function HelpIcon() {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="#9CA3AF" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M8.228 9c.549-1.165 2.03-2 3.772-2 2.21 0 4 1.343 4 3 0 1.4-1.278 2.575-3.006 2.907-.542.104-.994.54-.994 1.093m0 3h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
  );
}

function PricingIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
  );
}

export function Layout({ children }: { children: ComponentChildren }) {
  const { path, route } = useLocation();
  void locale.value;

  return (
    <div class="flex h-screen overflow-hidden">
      <aside class="w-52 flex-shrink-0 border-r border-gray-200 bg-white flex flex-col">
        <div class="flex items-center gap-2 px-5 py-5">
          <img src="/favicon.svg" alt="CSGLite" class="w-8 h-8" />
          <span class="font-semibold text-base text-gray-900">CSGLite</span>
        </div>
        <nav class="flex-1 px-3 space-y-1 mt-2">
          {navKeys.map((item) => {
            const active = path === item.path || (item.path !== "/" && path.startsWith(item.path));
            return (
              <a
                key={item.path}
                href={item.path}
                onClick={(event) => {
                  event.preventDefault();
                  route(item.path);
                }}
                class={`flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                  active
                    ? "bg-indigo-50 text-indigo-700"
                    : "text-gray-600 hover:bg-gray-50 hover:text-gray-900"
                }`}
              >
                <item.icon active={active} />
                {t(item.key)}
              </a>
            );
          })}
        </nav>
        {(() => {
          const active = path === "/settings";
          return (
            <a
              href="/settings"
              onClick={(event) => {
                event.preventDefault();
                route("/settings");
              }}
              class={`flex items-center gap-3 mx-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                active
                  ? "bg-indigo-50 text-indigo-700"
                  : "text-gray-600 hover:bg-gray-50 hover:text-gray-900"
              }`}
            >
              <SettingsIcon active={active} />
              {t("nav.settings")}
            </a>
          );
        })()}
        {(() => {
          const active = path === "/pricing";
          return (
            <a
              href="/pricing"
              onClick={(event) => {
                event.preventDefault();
                route("/pricing");
              }}
              class={`flex items-center gap-3 mx-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                active
                  ? "bg-indigo-50 text-indigo-700"
                  : "text-gray-600 hover:bg-gray-50 hover:text-gray-900"
              }`}
            >
              <PricingIcon active={active} />
              {t("nav.pricing")}
            </a>
          );
        })()}
        <a
          href="https://opencsg.com/docs/csghub/101/function/csghub-lite/intro"
          target="_blank"
          rel="noopener noreferrer"
          class="flex items-center gap-3 mx-3 mb-4 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors text-gray-600 hover:bg-gray-50 hover:text-gray-900"
        >
          <HelpIcon />
          {t("nav.help")}
        </a>
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

function DashboardIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zm10 0a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zm10 0a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z" />
    </svg>
  );
}

function MarketplaceIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M3 3h2l.4 2M7 13h10l4-8H5.4M7 13L5.4 5M7 13l-2.293 2.293c-.63.63-.184 1.707.707 1.707H17m0 0a2 2 0 100 4 2 2 0 000-4zm-8 2a2 2 0 100 4 2 2 0 000-4z" />
    </svg>
  );
}

function LibraryIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
    </svg>
  );
}

function DatasetsIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4m0 5c0 2.21-3.582 4-8 4s-8-1.79-8-4" />
    </svg>
  );
}

function ChatIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
    </svg>
  );
}

function ImagesIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
    </svg>
  );
}

function AIAppsIcon({ active }: { active: boolean }) {
  return (
    <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke={active ? "currentColor" : "#9CA3AF"} stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M19 15l.9 2.6L22.5 18l-2.6.9L19 21l-.9-2.1-2.6-.9 2.6-.4L19 15zM5 15l.9 2.6L8.5 18l-2.6.9L5 21l-.9-2.1-2.6-.9 2.6-.4L5 15z" />
    </svg>
  );
}

function AIGatewayIcon({ active }: { active: boolean }) {
  const color = active ? "currentColor" : "#9CA3AF";
  return (
    <svg class="w-5 h-5" viewBox="0 0 20 20" fill={color} aria-hidden="true">
      <path d="M11.248 18.25q-.825 0-1.568-.314a4.3 4.3 0 0 1-1.32-.874 4 4 0 0 1-1.304.214 4 4 0 0 1-2.046-.544 4.27 4.27 0 0 1-1.518-1.485 4 4 0 0 1-.56-2.095q0-.48.131-1.04A4.4 4.4 0 0 1 2.04 10.71a4.07 4.07 0 0 1 .017-3.4 4.2 4.2 0 0 1 1.056-1.418 3.8 3.8 0 0 1 1.6-.842 3.9 3.9 0 0 1 .76-1.683q.593-.759 1.451-1.188a4.04 4.04 0 0 1 1.832-.429q.825 0 1.567.313.742.314 1.32.875a4 4 0 0 1 1.304-.215q1.106 0 2.046.545a4.14 4.14 0 0 1 1.501 1.485q.578.941.578 2.095 0 .48-.132 1.04.66.61 1.023 1.419.363.792.363 1.666 0 .892-.38 1.717a4.3 4.3 0 0 1-1.072 1.435 3.8 3.8 0 0 1-1.584.825 3.8 3.8 0 0 1-.775 1.683 4.06 4.06 0 0 1-1.436 1.188 4.04 4.04 0 0 1-1.832.429m-4.076-2.062q.825 0 1.435-.347l3.103-1.782a.36.36 0 0 0 .164-.313v-1.42L7.881 14.62a.67.67 0 0 1-.726 0l-3.118-1.798a.5.5 0 0 1-.017.115v.198q0 .841.396 1.551.413.693 1.139 1.089a3.2 3.2 0 0 0 1.617.412m.165-2.69a.4.4 0 0 0 .181.05q.083 0 .165-.05l1.238-.71-3.977-2.31a.7.7 0 0 1-.363-.643v-3.58q-.825.362-1.32 1.122a2.9 2.9 0 0 0-.495 1.65q0 .809.413 1.55.412.743 1.072 1.123zm3.91 3.663q.875 0 1.585-.396a2.96 2.96 0 0 0 1.534-2.64v-3.564a.32.32 0 0 0-.165-.297l-1.254-.726v4.604a.7.7 0 0 1-.363.643l-3.119 1.799a3 3 0 0 0 1.783.577m.627-6.039V8.878L10.01 7.822 8.129 8.878v2.244l1.881 1.056zM7.057 5.859a.7.7 0 0 1 .363-.644l3.119-1.798a3 3 0 0 0-1.782-.578q-.874 0-1.584.396A2.96 2.96 0 0 0 6.05 4.324a3.07 3.07 0 0 0-.396 1.551v3.547q0 .199.165.314l1.237.726zm8.383 7.887q.825-.364 1.303-1.123.495-.758.495-1.65a3.15 3.15 0 0 0-.412-1.55q-.413-.743-1.073-1.123l-3.086-1.782q-.099-.065-.181-.049a.3.3 0 0 0-.165.05l-1.238.692 3.993 2.327a.6.6 0 0 1 .264.264.64.64 0 0 1 .1.363zm-3.317-8.382a.63.63 0 0 1 .726 0l3.135 1.831v-.297q0-.792-.396-1.501a2.86 2.86 0 0 0-1.105-1.155q-.71-.43-1.65-.43-.825 0-1.436.347L8.294 5.941a.36.36 0 0 0-.165.314v1.418z" />
    </svg>
  );
}
