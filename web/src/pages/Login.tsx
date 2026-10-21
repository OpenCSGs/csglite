import { useState } from "preact/hooks";
import { login } from "../api/client";
import { loadAuthSession, loginRequired } from "../auth";
import { locale, t } from "../i18n";

export function Login() {
  void locale.value;
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: Event) => {
    event.preventDefault();
    if (!username.trim() || !password) {
      setError(t("auth.loginRequiredFields"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await login(username.trim(), password);
      await loadAuthSession();
      loginRequired.value = false;
    } catch (err: any) {
      setError(err?.status === 429 ? t("auth.loginThrottled") : err?.status === 401 ? t("auth.loginInvalid") : err?.message || t("auth.loginFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="flex min-h-screen items-center justify-center bg-gray-50 px-4">
      <form onSubmit={submit} class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-8 shadow-sm">
        <div class="mb-6 flex items-center gap-3">
          <img src="/favicon.svg" alt="CSGLite" class="h-9 w-9" />
          <div>
            <h1 class="text-lg font-semibold text-gray-900">{t("auth.loginTitle")}</h1>
            <p class="text-sm text-gray-500">{t("auth.loginSubtitle")}</p>
          </div>
        </div>
        <label class="block text-sm font-medium text-gray-700" for="login-username">
          {t("auth.username")}
        </label>
        <input
          id="login-username"
          type="text"
          autoComplete="username"
          autoFocus
          value={username}
          onInput={(event) => setUsername((event.target as HTMLInputElement).value)}
          class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
        />
        <label class="mt-4 block text-sm font-medium text-gray-700" for="login-password">
          {t("auth.password")}
        </label>
        <input
          id="login-password"
          type="password"
          autoComplete="current-password"
          value={password}
          onInput={(event) => setPassword((event.target as HTMLInputElement).value)}
          class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
        />
        {error && (
          <p role="alert" class="mt-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">
            {error}
          </p>
        )}
        <button
          type="submit"
          disabled={busy}
          class="mt-6 w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {busy ? t("auth.signingIn") : t("auth.signIn")}
        </button>
        <p class="mt-4 text-xs text-gray-400">{t("auth.loginHint")}</p>
      </form>
    </div>
  );
}
