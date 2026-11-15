import "./index.css";
import { render } from "preact";
import { useState } from "preact/hooks";
import { locale, setLocale, t } from "./i18n";

function PublicLogin() {
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  void locale.value;
  const secureOrigin = window.location.protocol === "https:";
  async function login(event: Event) {
    event.preventDefault();
    if (!secureOrigin) return;
    setBusy(true); setError("");
    try {
      const response = await fetch("/auth/login", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token: token.trim() }), credentials: "same-origin",
      });
      if (!response.ok) {
        setError(response.status === 429 ? "publicLogin.rateLimited" : response.status === 401 ? "publicLogin.invalid" : "publicLogin.failed");
        return;
      }
      setToken("");
      window.location.replace("/");
    } catch { setError("publicLogin.failed"); }
    finally { setBusy(false); }
  }
  return (
    <div class="min-h-screen bg-slate-50 text-slate-900 flex flex-col">
      <header class="flex items-center justify-between px-6 py-6 lg:px-12">
        <a href="/login" class="flex items-center gap-2.5 font-semibold text-xl"><img src="/favicon.svg" alt="" class="h-9 w-9" />CSGLite</a>
        <button type="button" onClick={() => setLocale(locale.value === "zh" ? "en" : "zh")} class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm hover:bg-slate-100" aria-label={t("publicLogin.language")}>{locale.value === "zh" ? "English" : "中文"}</button>
      </header>
      <main class="mx-auto grid w-full max-w-6xl flex-1 items-center gap-12 px-6 py-12 lg:grid-cols-2 lg:gap-24">
        <section class="hidden lg:block">
          <p class="text-xs font-semibold uppercase tracking-widest text-indigo-600">{t("publicLogin.eyebrow")}</p>
          <h1 class="mt-5 text-5xl font-semibold leading-tight tracking-tight">{t("publicLogin.headline")}</h1>
          <p class="mt-6 max-w-md text-lg leading-relaxed text-slate-500">{t("publicLogin.description")}</p>
          <div class="mt-12 flex items-center gap-4 text-sm">
            <div class="rounded-2xl border border-indigo-200 bg-indigo-50 p-5 font-medium text-indigo-700">{t("publicLogin.gateway")}</div>
            <div class="h-px flex-1 bg-indigo-200" />
            <div class="space-y-3 text-slate-600">
              <div class="rounded-xl border border-slate-200 bg-white px-5 py-3">{t("publicLogin.worker")} 01</div>
              <div class="rounded-xl border border-slate-200 bg-white px-5 py-3">{t("publicLogin.worker")} 02</div>
            </div>
          </div>
          <p class="mt-5 text-xs text-slate-400">{t("publicLogin.encrypted")}</p>
        </section>
        <section class="w-full rounded-3xl border border-slate-200 bg-white p-7 shadow-sm sm:p-10">
          <div class="mb-7 flex h-12 w-12 items-center justify-center rounded-2xl bg-indigo-50 text-indigo-600">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><rect x="4" y="10" width="16" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/></svg>
          </div>
          <h2 class="text-2xl font-semibold tracking-tight">{t("publicLogin.title")}</h2>
          <p class="mt-3 text-sm leading-6 text-slate-500">{t("publicLogin.subtitle")}</p>
          <form onSubmit={login} class="mt-8">
            {!secureOrigin && <p role="alert" class="mb-4 text-sm text-amber-700">{t("publicLogin.httpsRequired")}</p>}
            <label for="login-token" class="mb-2 block text-sm font-medium">{t("publicLogin.token")}</label>
            <input id="login-token" name="token" type="password" disabled={!secureOrigin} required maxLength={256} autoComplete="off" autoCapitalize="off" spellcheck={false} value={token} onInput={(event) => setToken(event.currentTarget.value)} placeholder={t("publicLogin.placeholder")} class="w-full rounded-xl border border-slate-300 px-4 py-3 text-sm outline-none transition focus:border-indigo-500 focus:ring-4 focus:ring-indigo-50" aria-describedby={error ? "login-error" : undefined} />
            {error && <p id="login-error" role="alert" class="mt-3 text-sm text-red-600">{t(error)}</p>}
            <button type="submit" disabled={busy || !token.trim() || !secureOrigin} class="mt-5 w-full rounded-xl bg-indigo-600 px-4 py-3 text-sm font-semibold text-white transition hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50">{t(busy ? "publicLogin.signingIn" : "publicLogin.signIn")}</button>
          </form>
          <div class="mt-8 border-t border-slate-100 pt-6">
            <p class="text-xs leading-5 text-slate-500">{t("publicLogin.cliHint")}</p>
            <code class="mt-3 block overflow-x-auto rounded-lg bg-slate-50 px-3 py-3 text-xs text-slate-700">csghub-lite cluster login-token</code>
            <p class="mt-3 text-xs leading-5 text-slate-400">{t("publicLogin.sessionHint")}</p>
          </div>
        </section>
      </main>
      <footer class="py-6 text-center text-xs text-slate-400">&copy; OpenCSG &middot; CSGLite</footer>
    </div>
  );
}

render(<PublicLogin />, document.getElementById("app")!);
