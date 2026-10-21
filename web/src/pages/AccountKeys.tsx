import { useEffect, useState } from "preact/hooks";
import { createLocalAPIKey, deleteLocalAPIKey, getLocalAPIKeys } from "../api/client";
import type { LocalAPIKeyInfo } from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { locale, t } from "../i18n";
import { formatLicenseDate } from "../license";

/**
 * A member's own API keys, the counterpart of OpenRouter's Keys page. The
 * server lists only the caller's keys, and each key acts with its owner's
 * role.
 */
export function AccountKeys() {
  void locale.value;
  const [keys, setKeys] = useState<LocalAPIKeyInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState("");
  const [copied, setCopied] = useState(false);
  const [deleting, setDeleting] = useState<LocalAPIKeyInfo | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      setKeys((await getLocalAPIKeys()).keys);
      setError("");
    } catch (err: any) {
      setError(err?.message || t("account.keysLoadFailed"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const create = async (event: Event) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const resp = await createLocalAPIKey(name.trim());
      setCreated(resp.api_key);
      setName("");
      await load();
    } catch (err: any) {
      setError(err?.message || t("account.keysCreateFailed"));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!deleting) return;
    setBusy(true);
    try {
      await deleteLocalAPIKey(deleting.id);
      setDeleting(null);
      await load();
    } catch (err: any) {
      setError(err?.message || t("account.keysDeleteFailed"));
      setDeleting(null);
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(created);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* the key stays selectable */
    }
  };

  const base = `${window.location.origin}/v1`;
  return (
    <div class="page-shell space-y-6">
      <div>
        <p class="text-xs font-semibold uppercase tracking-[0.28em] text-indigo-500">{t("account.eyebrow")}</p>
        <h1 class="mt-2 text-3xl font-bold tracking-tight text-gray-950">{t("account.myKeys")}</h1>
        <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-600">{t("account.keysSubtitle")}</p>
      </div>
      {created && (
        <section class="rounded-xl border border-green-200 bg-green-50 p-4">
          <p class="text-sm font-semibold text-green-900">{t("account.keyCreatedTitle")}</p>
          <p class="mt-0.5 text-xs text-green-800">{t("account.keyCreatedDesc")}</p>
          <div class="mt-3 flex gap-2">
            <input readOnly value={created} class="min-w-0 flex-1 rounded-lg border border-green-200 bg-white px-3 py-2 font-mono text-xs" onFocus={(e) => (e.target as HTMLInputElement).select()} />
            <button type="button" onClick={() => void copy()} class="rounded-lg bg-green-700 px-4 py-2 text-sm font-semibold text-white hover:bg-green-800">
              {copied ? t("team.copied") : t("account.copyKey")}
            </button>
          </div>
        </section>
      )}
      {error && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>}
      <section class="rounded-xl border border-gray-200 bg-white shadow-sm">
        <form onSubmit={create} class="flex flex-wrap items-end gap-3 border-b border-gray-100 px-5 py-4">
          <label class="block min-w-[240px] flex-1 text-sm font-medium text-gray-700">
            {t("account.keyName")}
            <input
              class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              placeholder={t("account.keyNamePlaceholder")}
              value={name}
              onInput={(e) => setName((e.target as HTMLInputElement).value)}
            />
          </label>
          <button type="submit" disabled={busy} class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-60">
            {t("account.createKey")}
          </button>
        </form>
        {loading ? (
          <p class="px-5 py-6 text-sm text-gray-500">{t("team.loading")}</p>
        ) : keys.length === 0 ? (
          <p class="px-5 py-8 text-center text-sm text-gray-500">{t("account.keysEmpty")}</p>
        ) : (
          <ul class="divide-y divide-gray-100">
            {keys.map((key) => (
              <li key={key.id} class="flex flex-wrap items-center justify-between gap-3 px-5 py-3">
                <div>
                  <p class="font-medium text-gray-900">{key.name}</p>
                  <p class="text-xs text-gray-500">
                    <span class="font-mono">{key.prefix}</span> · {t("account.keyCreatedAt", formatLicenseDate(key.created_at, locale.value))}
                    {key.last_used_at ? ` · ${t("account.keyLastUsed", formatLicenseDate(key.last_used_at, locale.value))}` : ""}
                  </p>
                </div>
                <button type="button" class="text-sm text-red-600 hover:underline" onClick={() => setDeleting(key)}>
                  {t("team.delete")}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section class="rounded-xl border border-gray-200 bg-gray-50 p-5 text-sm text-gray-700">
        <p class="font-medium text-gray-900">{t("account.usageTitle")}</p>
        <p class="mt-1 text-xs text-gray-500">{t("account.usageDesc")}</p>
        <code class="mt-2 block overflow-x-auto whitespace-pre rounded-lg bg-gray-900 px-3 py-2 font-mono text-xs text-gray-100">
          {`curl ${base}/chat/completions \\\n  -H "Authorization: Bearer <API_KEY>" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"<model>","messages":[{"role":"user","content":"hi"}]}'`}
        </code>
      </section>
      <ConfirmDialog
        open={deleting !== null}
        title={t("account.deleteKeyTitle")}
        name={deleting?.name ?? ""}
        description={t("account.deleteKeyDesc")}
        confirmLabel={t("team.delete")}
        busy={busy}
        onConfirm={() => void remove()}
        onCancel={() => setDeleting(null)}
      />
    </div>
  );
}
