import { useEffect, useRef, useState } from "preact/hooks";
import type { ProviderPool } from "../api/client";
import { serviceTestText, testModelService } from "../api/serviceTest";
import type { ServiceTestProtocol } from "../api/serviceTest";
import { t } from "../i18n";

const protocols: { value: ServiceTestProtocol; label: string }[] = [
  { value: "chat/completions", label: "OpenAI · Chat Completions" },
  { value: "responses", label: "OpenAI · Responses" },
  { value: "messages", label: "Anthropic · Messages" },
];

export function InferenceTestDialog({ service, onClose }: { service: ProviderPool; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const activeRequest = useRef<AbortController | null>(null);
  const [protocol, setProtocol] = useState<ServiceTestProtocol>(service.category === "embedding" ? "embeddings" : "chat/completions");
  const [input, setInput] = useState("");
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const [stopped, setStopped] = useState(false);
  const [result, setResult] = useState<Awaited<ReturnType<typeof testModelService>> | null>(null);
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => { activeRequest.current?.abort(); activeRequest.current = null; element?.close(); };
  }, []);

  const run = async () => {
    if (running || !input.trim()) return;
    const request = new AbortController();
    activeRequest.current = request;
    setRunning(true); setError(""); setResult(null); setStopped(false);
    try {
      const response = await testModelService(service.model, protocol, input.trim(), request.signal);
      if (activeRequest.current === request) setResult(response);
    } catch (err) {
      if (activeRequest.current === request) {
        if (request.signal.aborted) setStopped(true);
        else setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      if (activeRequest.current === request) { activeRequest.current = null; setRunning(false); }
    }
  };

  return <dialog ref={dialog} onCancel={event => { event.preventDefault(); onClose(); }} aria-labelledby="inference-test-title" class="m-auto max-h-[88dvh] w-[min(860px,94vw)] max-w-none overflow-y-auto rounded-2xl bg-white p-0 shadow-2xl backdrop:bg-gray-900/40">
    <header class="flex items-start justify-between gap-4 border-b border-gray-100 px-6 py-5">
      <div class="min-w-0"><h2 id="inference-test-title" class="text-base font-semibold text-gray-900">{t("services.test")} · {service.name}</h2><p class="mt-1 break-all font-mono text-xs text-gray-400">{service.model}</p></div>
      <button onClick={onClose} aria-label={t("services.close")} class="rounded-lg p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-700"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="m6 6 12 12M6 18 18 6" /></svg></button>
    </header>
    <form onSubmit={event => { event.preventDefault(); void run(); }}>
      <div class="flex flex-wrap items-center gap-3 px-6 pt-5">
        <label class="flex items-center gap-3 text-sm text-gray-600">{t("services.testProtocol")}
          {service.category === "embedding" ? <span class="font-medium text-gray-900">OpenAI · Embeddings</span> : <select value={protocol} disabled={running} onChange={e => { setProtocol(e.currentTarget.value as ServiceTestProtocol); setResult(null); setError(""); setStopped(false); }} class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500">{protocols.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}</select>}
        </label>
        <code class="break-all text-xs text-gray-400">POST /ai-gateway/v1/{protocol}</code>
      </div>
      <div class="grid gap-5 px-6 py-5 md:grid-cols-2">
        <label class="flex flex-col gap-2 text-sm font-medium text-gray-700">{t("services.testInput")}<textarea autoFocus rows={10} value={input} disabled={running} onInput={e => setInput(e.currentTarget.value)} placeholder={t(protocol === "embeddings" ? "services.testEmbeddingPlaceholder" : "services.testPromptPlaceholder")} class="min-h-60 w-full resize-y rounded-xl border border-gray-200 p-3 text-sm font-normal leading-6 text-gray-800 focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100 disabled:bg-gray-50" /></label>
        <div class="flex min-w-0 flex-col gap-2"><div class="flex items-center justify-between text-sm font-medium text-gray-700"><span>{t("services.testOutput")}</span>{result && <span class="text-xs font-normal text-emerald-600">HTTP {result.status} · {result.durationMS} ms</span>}</div>
          <div aria-live="polite" class="min-h-60 flex-1 overflow-auto rounded-xl bg-gray-50 p-3 text-sm leading-6 md:max-h-80">
            {error ? <p role="alert" class="whitespace-pre-wrap break-words text-red-600">{error}</p> : result ? <pre class="whitespace-pre-wrap break-words font-sans text-gray-800">{serviceTestText(result.data, protocol)}</pre> : <p class="text-gray-400">{t(running ? "services.testing" : stopped ? "services.testStopped" : "services.testEmpty")}</p>}
          </div>
        </div>
      </div>
      {result && <details class="mx-6 mb-5 text-xs text-gray-500"><summary class="cursor-pointer">{t("services.testRawResponse")}</summary><pre class="mt-2 max-h-60 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3">{JSON.stringify(result.data, null, 2)}</pre></details>}
      <footer class="flex items-center justify-between gap-4 border-t border-gray-100 px-6 py-4">
        <p class="text-xs text-gray-400">{t("services.testSingleRequest")}</p>
        {running ? <button type="button" onClick={() => activeRequest.current?.abort()} class="shrink-0 rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">{t("services.testStop")}</button> : <button type="submit" disabled={!input.trim()} class="shrink-0 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-40">{t("services.testRun")}</button>}
      </footer>
    </form>
  </dialog>;
}
