import { useEffect, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { acceptInvite, getInvite } from "../api/client";
import type { TeamInviteInfo } from "../api/client";
import { loadAuthSession, loginRequired } from "../auth";
import { locale, t } from "../i18n";
import { formatLicenseDate } from "../license";

const inputClass =
  "mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500";

/** Where an invited person sets their password and joins the instance. */
export function Invite({ token }: { token: string }) {
  void locale.value;
  const { route } = useLocation();
  const [info, setInfo] = useState<TeamInviteInfo | null>(null);
  const [invalid, setInvalid] = useState(false);
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    getInvite(token)
      .then(setInfo)
      .catch(() => setInvalid(true));
  }, [token]);

  const submit = async (event: Event) => {
    event.preventDefault();
    if (password.length < 8) return setError(t("team.passwordTooShort"));
    if (password !== confirm) return setError(t("team.passwordMismatch"));
    setBusy(true);
    setError("");
    try {
      await acceptInvite(token, password);
      await loadAuthSession();
      loginRequired.value = false;
      route("/", true);
    } catch (err: any) {
      if (err?.status === 404) setInvalid(true);
      else setError(err?.message || t("invite.failed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="flex min-h-screen items-center justify-center bg-gray-50 px-4">
      <div class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-8 shadow-sm">
        <div class="mb-6 flex items-center gap-3">
          <img src="/favicon.svg" alt="CSGLite" class="h-9 w-9" />
          <h1 class="text-lg font-semibold text-gray-900">{t("invite.title")}</h1>
        </div>
        {invalid ? (
          <>
            <p class="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">{t("invite.invalid")}</p>
            <button type="button" class="mt-6 text-sm text-indigo-600 hover:underline" onClick={() => route("/", true)}>
              {t("invite.goToSignIn")}
            </button>
          </>
        ) : !info ? (
          <p class="text-sm text-gray-500">{t("team.loading")}</p>
        ) : (
          <form onSubmit={submit}>
            <p class="text-sm leading-6 text-gray-600">{t("invite.desc", info.display_name || info.username, t(`team.role.${info.role}`))}</p>
            <div class="mt-4 rounded-lg bg-gray-50 px-3 py-2 text-sm">
              <span class="text-gray-500">{t("auth.username")}: </span>
              <span class="font-medium text-gray-900">{info.username}</span>
            </div>
            <label class="mt-4 block text-sm font-medium text-gray-700">
              {t("invite.choosePassword")}
              <input type="password" autoComplete="new-password" autoFocus class={inputClass} value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
            </label>
            <label class="mt-3 block text-sm font-medium text-gray-700">
              {t("team.passwordConfirm")}
              <input type="password" autoComplete="new-password" class={inputClass} value={confirm} onInput={(e) => setConfirm((e.target as HTMLInputElement).value)} />
            </label>
            {error && <p role="alert" class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>}
            <button
              type="submit"
              disabled={busy}
              class="mt-6 w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-60"
            >
              {busy ? t("team.saving") : t("invite.accept")}
            </button>
            <p class="mt-4 text-xs text-gray-400">{t("invite.expires", formatLicenseDate(info.expires_at, locale.value))}</p>
          </form>
        )}
      </div>
    </div>
  );
}
