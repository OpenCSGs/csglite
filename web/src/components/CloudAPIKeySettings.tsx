import { useState } from "preact/hooks";
import { cloudAuth, communityName, communitySettings, updateManualCloudAPIKey } from "../cloudAccount";
import { t } from "../i18n";

export function CloudAPIKeySettings() {
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const auth = cloudAuth.value;
  const manual = auth?.has_api_key && auth.api_key_source === "manual";
  const provider = communitySettings.value?.cloud_provider_name || communityName();

  async function save(value: string | null) {
    if (busy) return;
    if (value !== null && !value.trim()) { setError(t("chat.cloudApiKeyEmpty")); return; }
    setBusy(true);
    setError("");
    try {
      await updateManualCloudAPIKey(value?.trim() ?? null);
      setKey("");
    } catch {
      setError(t("access.apiKeySaveFailed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form class="space-y-4 p-6" onSubmit={(event) => { event.preventDefault(); void save(key); }}>
      <p class="text-sm text-gray-500">{t("settings.cloudAPIKeyDesc", provider)}</p>
      <p role="status" class="rounded-lg bg-gray-50 p-3 text-sm text-gray-700">
        {t(auth?.has_api_key ? manual ? "settings.cloudAPIKeyManualStatus" : "settings.cloudAPIKeyBuiltinStatus" : "settings.cloudAPIKeyMissingStatus", auth?.api_key_prefix ? ` (${auth.api_key_prefix})` : "")}
      </p>
      <label class="block text-sm font-medium text-gray-700">
        {t("chat.cloudApiKeyLabel")}
        <input type="password" autoComplete="off" spellcheck={false} value={key} disabled={busy}
          onInput={(event) => setKey(event.currentTarget.value)}
          class="mt-2 w-full rounded-lg border border-gray-200 px-3 py-2.5 focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100" />
      </label>
      <p class="text-xs text-gray-500">{t("settings.cloudAPIKeyStatusHint")}</p>
      {error && <p role="alert" class="text-sm text-red-600">{error}</p>}
      <div class="flex flex-wrap justify-end gap-3">
        {manual && <button type="button" disabled={busy} onClick={() => void save(null)} class="rounded-lg border border-red-200 px-3 py-2 text-sm text-red-600 disabled:opacity-50">{t("settings.clearAPIKey")}</button>}
        <button type="submit" disabled={busy || !key.trim()} class="rounded-lg bg-indigo-600 px-3 py-2 text-sm text-white disabled:opacity-50">{t(busy ? "chat.cloudApiKeySaving" : "chat.cloudApiKeySave")}</button>
      </div>
    </form>
  );
}
