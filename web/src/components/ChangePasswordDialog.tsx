import { useState } from "preact/hooks";
import { changePassword } from "../api/client";
import { loadAuthSession } from "../auth";
import { locale, t } from "../i18n";

const inputClass =
  "mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500";

/**
 * Changes the signed-in user's password. As a dialog it is opened from the
 * account menu; with `forced` it is the whole screen a user sees after signing
 * in with a password an administrator chose, until they replace it.
 */
export function ChangePasswordDialog({ onClose, forced = false }: { onClose?: () => void; forced?: boolean }) {
  void locale.value;
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

  const submit = async (event: Event) => {
    event.preventDefault();
    if (next.length < 8) return setError(t("team.passwordTooShort"));
    if (next !== confirm) return setError(t("team.passwordMismatch"));
    if (next === current) return setError(t("auth.passwordUnchanged"));
    setBusy(true);
    setError("");
    try {
      await changePassword(current, next);
      if (forced) {
        await loadAuthSession();
        return;
      }
      setDone(true);
    } catch (err: any) {
      setError(err?.status === 400 ? t("auth.currentPasswordWrong") : err?.message || t("auth.changePasswordFailed"));
    } finally {
      setBusy(false);
    }
  };

  const form = (
    <form
      role="dialog"
      aria-modal="true"
      onSubmit={submit}
      onClick={(event) => event.stopPropagation()}
      class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-6 shadow-2xl"
    >
      <h2 class="text-lg font-semibold text-gray-900">{forced ? t("auth.forcedChangeTitle") : t("auth.changePassword")}</h2>
      {forced && <p class="mt-1 text-sm leading-6 text-gray-600">{t("auth.forcedChangeDesc")}</p>}
      {done ? (
        <p class="mt-4 rounded-lg bg-green-50 px-3 py-2 text-sm text-green-700">{t("auth.passwordChanged")}</p>
      ) : (
        <>
          <label class="mt-4 block text-sm font-medium text-gray-700">
            {t("auth.currentPassword")}
            <input type="password" autoComplete="current-password" autoFocus value={current} onInput={(e) => setCurrent((e.target as HTMLInputElement).value)} class={inputClass} />
          </label>
          <label class="mt-3 block text-sm font-medium text-gray-700">
            {t("auth.newPassword")}
            <input type="password" autoComplete="new-password" value={next} onInput={(e) => setNext((e.target as HTMLInputElement).value)} class={inputClass} />
          </label>
          <label class="mt-3 block text-sm font-medium text-gray-700">
            {t("team.passwordConfirm")}
            <input type="password" autoComplete="new-password" value={confirm} onInput={(e) => setConfirm((e.target as HTMLInputElement).value)} class={inputClass} />
          </label>
          {error && (
            <p role="alert" class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">
              {error}
            </p>
          )}
        </>
      )}
      <div class="mt-5 flex justify-end gap-2">
        {!forced && (
          <button type="button" onClick={onClose} disabled={busy} class="rounded-lg border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">
            {done ? t("dash.close") : t("team.cancel")}
          </button>
        )}
        {!done && (
          <button type="submit" disabled={busy} class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-60">
            {busy ? t("team.saving") : forced ? t("auth.forcedChangeAction") : t("team.save")}
          </button>
        )}
      </div>
    </form>
  );

  if (forced) {
    return <div class="flex min-h-screen items-center justify-center bg-gray-50 px-4">{form}</div>;
  }
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4" onClick={() => !busy && onClose?.()}>
      {form}
    </div>
  );
}
