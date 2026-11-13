import { signal } from "@preact/signals";
import { getCloudAuthStatus, saveSettings } from "./api/client";
import type { AppSettings, CloudAuthStatus } from "./api/client";
import { COMMUNITY_SITES, matchCommunitySite } from "./data/communitySites";
import { t } from "./i18n";

// Navigation and Settings share the server's account and community configuration.
// These signals are only a cache; credentials and community URLs stay in config.json.
export const cloudAuth = signal<CloudAuthStatus | null>(null);
export const cloudAuthError = signal("");
export const communitySettings = signal<AppSettings | null>(null);
export const savingCommunity = signal(false);
let revision = 0;
let pending: Promise<CloudAuthStatus | null> | null = null;
let communitySave: Promise<AppSettings | null> = Promise.resolve(null);

export async function waitForCommunitySelection() {
  await communitySave.catch(() => null);
}

// Persist the existing URL pair as soon as a community is chosen, even if the
// login dialog is then closed. Serialize writes so the last choice wins.
export function saveCommunitySelection(
  serverURL: string,
  gatewayURL: string,
): Promise<AppSettings> {
  savingCommunity.value = true;
  const task = communitySave
    .catch(() => null)
    .then(async () => {
      const settings = communitySettings.value;
      if (
        settings?.server_url === serverURL &&
        settings.ai_gateway_url === gatewayURL
      )
        return settings;
      const auth = await refreshCloudAuth();
      if (!auth) throw new Error(t("account.loadFailed"));
      if (auth.has_token) throw new Error(t("settings.siteSwitchSignedIn"));
      const saved = await saveSettings({
        server_url: serverURL,
        ai_gateway_url: gatewayURL,
      });
      communitySettings.value = saved;
      invalidateCloudAuth();
      void refreshCloudAuth();
      window.dispatchEvent(new Event("csghub:providers-changed"));
      return saved;
    });
  communitySave = task;
  const finish = () => {
    if (communitySave === task) savingCommunity.value = false;
  };
  void task.then(finish, finish);
  return task;
}

export function invalidateCloudAuth() {
  revision += 1;
  pending = null;
}

export function setCloudAuth(status: CloudAuthStatus) {
  invalidateCloudAuth();
  cloudAuth.value = status;
  cloudAuthError.value = "";
}

export function refreshCloudAuth(): Promise<CloudAuthStatus | null> {
  if (pending) return pending;
  const requestRevision = revision;
  const request = getCloudAuthStatus()
    .then((status) => {
      if (requestRevision !== revision) return null;
      cloudAuth.value = status;
      cloudAuthError.value = "";
      return status;
    })
    .catch(() => {
      if (requestRevision === revision)
        cloudAuthError.value = t("account.loadFailed");
      // Keep the last known account during a transport failure.
      return null;
    })
    .finally(() => {
      if (pending === request) pending = null;
    });
  pending = request;
  return request;
}

export function cloudUserLabel(status: CloudAuthStatus | null): string {
  return (status?.user?.nickname || status?.user?.username || "").trim();
}

export function communityName(settings = communitySettings.value): string {
  if (!settings) return "";
  const id = matchCommunitySite(settings.server_url, settings.ai_gateway_url, {
    serverURL: settings.default_server_url,
    gatewayURL: settings.default_ai_gateway_url,
  });
  const site = COMMUNITY_SITES.find((item) => item.id === id);
  return t(site?.nameKey || "settings.siteCustom");
}
