import { useEffect, useRef, useState } from "preact/hooks";
import type { ProviderPool, DecisionQuestionType } from "../api/client";
import { serviceTestText, testModelService, serviceTestProtocols } from "../api/serviceTest";
import type { ServiceTestProtocol } from "../api/serviceTest";
import { canSendDecision } from "../utils/decision";
import { t } from "../i18n";

const protocols: Record<ServiceTestProtocol, string> = {
  "chat/completions": "OpenAI · Chat Completions",
  responses: "OpenAI · Responses",
  messages: "Anthropic · Messages",
  embeddings: "OpenAI · Embeddings",
  decisions: "OpenAI · Decisions",
  "images/generations": "OpenAI · Images Generations",
  "images/edits": "OpenAI · Images Edits",
  "audio/transcriptions": "OpenAI · Audio Transcriptions",
  "audio/speech": "OpenAI · Audio Speech",
};

export function InferenceTestDialog({ service: initialService, services = [initialService], onClose }: { service: ProviderPool; services?: ProviderPool[]; onClose: () => void }) {
  const [serviceID, setServiceID] = useState(initialService.id);
  const service = services.find(candidate => candidate.id === serviceID) || initialService;
  const dialog = useRef<HTMLDialogElement>(null);
  const activeRequest = useRef<AbortController | null>(null);
  const supported=serviceTestProtocols(service);
  const [protocol, setProtocol] = useState<ServiceTestProtocol>(supported[0]||"chat/completions");
  const [file,setFile]=useState<File>();
  const [voice,setVoice]=useState("");
  const [question,setQuestion]=useState(t("services.testDecisionQuestionDefault"));
  const [questionType,setQuestionType]=useState<DecisionQuestionType>("predicate");
  const [choices,setChoices]=useState("");
  const [audioURL,setAudioURL]=useState("");
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

  useEffect(()=>{
    if(!result?.blob){setAudioURL("");return}
    const url=URL.createObjectURL(result.blob);setAudioURL(url);return ()=>URL.revokeObjectURL(url);
  },[result]);
  const fileRequired=protocol==="audio/transcriptions"||protocol==="images/edits";
  const valid=supported.length>0 && (protocol==="audio/transcriptions"?!!file:!!input.trim()) && (!fileRequired||!!file)
    &&(protocol!=="decisions"||canSendDecision(questionType,question,choices));
  const run = async () => {
    if (running || !valid) return;
    const request = new AbortController();
    activeRequest.current = request;
    setRunning(true); setError(""); setResult(null); setStopped(false);
    try {
      const response = await testModelService(service.model, protocol, input.trim(), request.signal, {file,voice,question,questionType,choices});
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
      {services.length > 1 && <label class="mx-6 mt-5 flex flex-wrap items-center gap-3 text-sm text-gray-600">{t("services.modelID")}
        <select value={service.id} disabled={running} onChange={event => {
          const next = services.find(candidate => candidate.id === event.currentTarget.value);
          if (!next) return;
          setServiceID(next.id); setProtocol(serviceTestProtocols(next)[0] || "chat/completions");
          setResult(null); setError(""); setStopped(false); setFile(undefined); setInput(""); setVoice("");
        }} class="min-w-0 max-w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500">
          {services.map(candidate => <option key={candidate.id} value={candidate.id}>{candidate.model}</option>)}
        </select>
      </label>}
      <div class="flex flex-wrap items-center gap-3 px-6 pt-5">
        <label class="flex items-center gap-3 text-sm text-gray-600">{t("services.testProtocol")}
          <select value={protocol} disabled={running||!supported.length} onChange={e=>{setProtocol(e.currentTarget.value as ServiceTestProtocol);setResult(null);setError("");setStopped(false);setFile(undefined)}} class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 focus:outline-none focus:ring-2 focus:ring-indigo-500">
            {supported.map(protocol=><option key={protocol} value={protocol}>{protocols[protocol]}</option>)}
          </select>
        </label>
        <code class="break-all text-xs text-gray-400">POST /ai-gateway/v1/{protocol}</code>
      </div>
      {!supported.length && <p role="status" class="px-6 pt-4 text-sm text-amber-700">{t("services.testNoProtocol")}</p>}
      {protocol==="decisions"&&<div class="grid gap-3 px-6 pt-4 sm:grid-cols-2">
        <label class="text-sm text-gray-600">{t("services.testQuestionType")}<select value={questionType} disabled={running} onChange={e=>setQuestionType(e.currentTarget.value as DecisionQuestionType)} class="mt-1 w-full rounded-lg border p-2">{(["predicate","choice","score"] as const).map(kind=><option value={kind}>{t(`services.testQuestion.${kind}`)}</option>)}</select></label>
        <label class="text-sm text-gray-600">{t("services.testQuestion")}<input value={question} disabled={running} onInput={e=>setQuestion(e.currentTarget.value)} class="mt-1 w-full rounded-lg border p-2" /></label>
        {questionType!=="predicate"&&<label class="sm:col-span-2 text-sm text-gray-600">{t(questionType==="choice"?"services.testChoices":"services.testLevels")}<textarea rows={3} value={choices} disabled={running} onInput={e=>setChoices(e.currentTarget.value)} class="mt-1 w-full rounded-lg border p-2" /></label>}
      </div>}
      {(fileRequired||(service.vision&&["chat/completions","responses","messages"].includes(protocol)))&&<label class="mx-6 mt-4 block text-sm text-gray-600">{t(protocol==="audio/transcriptions"?"services.testAudioFile":fileRequired?"services.testImageFile":"services.testOptionalImage")}<input type="file" accept={protocol==="audio/transcriptions"?"audio/*":"image/*"} disabled={running} onChange={e=>setFile(e.currentTarget.files?.[0])} class="mt-2 block w-full text-sm" /></label>}
      {protocol==="audio/speech"&&<label class="mx-6 mt-4 block text-sm text-gray-600">{t("services.testVoice")}<input value={voice} disabled={running} onInput={e=>setVoice(e.currentTarget.value)} placeholder={t("services.testVoiceDefault")} class="ml-3 rounded-lg border p-2" /></label>}
      <div class="grid gap-5 px-6 py-5 md:grid-cols-2">
        <label class="flex flex-col gap-2 text-sm font-medium text-gray-700">{t("services.testInput")}{protocol==="audio/transcriptions" ? <p class="min-h-60 rounded-xl border border-dashed border-gray-200 p-4 text-sm font-normal text-gray-500">{t("services.testTranscriptionHint")}</p> : <textarea autoFocus rows={10} value={input} disabled={running} onInput={e => setInput(e.currentTarget.value)} placeholder={t(protocol === "embeddings" ? "services.testEmbeddingPlaceholder" : protocol==="decisions"?"services.testDecisionInput": protocol.startsWith("images/")?"services.testImagePrompt":"services.testPromptPlaceholder")} class="min-h-60 w-full resize-y rounded-xl border border-gray-200 p-3 text-sm font-normal leading-6 text-gray-800 focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100 disabled:bg-gray-50" />}</label>
        <div class="flex min-w-0 flex-col gap-2"><div class="flex items-center justify-between text-sm font-medium text-gray-700"><span>{t("services.testOutput")}</span>{result && <span class="text-xs font-normal text-emerald-600">HTTP {result.status} · {result.durationMS} ms</span>}</div>
          <div aria-live="polite" class="min-h-60 flex-1 overflow-auto rounded-xl bg-gray-50 p-3 text-sm leading-6 md:max-h-80">
            {error ? <p role="alert" class="whitespace-pre-wrap break-words text-red-600">{error}</p> : result ? audioURL ? <div class="space-y-3"><audio controls src={audioURL} class="w-full" /><a download="speech.wav" href={audioURL} class="text-xs text-indigo-600">{t("services.testDownloadAudio")}</a></div> : protocol.startsWith("images/")&&result.data?.data ? <div class="space-y-3">{result.data.data.map((item:any,index:number)=><img key={index} alt={t("services.testGeneratedImage")} src={item.b64_json?`data:image/png;base64,${item.b64_json}`:item.url} class="max-w-full rounded-lg" />)}</div> : <pre class="whitespace-pre-wrap break-words font-sans text-gray-800">{serviceTestText(result.data, protocol)}</pre> : <p class="text-gray-400">{t(running ? "services.testing" : stopped ? "services.testStopped" : "services.testEmpty")}</p>}
          </div>
        </div>
      </div>
      {result && !result.blob && <details class="mx-6 mb-5 text-xs text-gray-500"><summary class="cursor-pointer">{t("services.testRawResponse")}</summary><pre class="mt-2 max-h-60 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3">{JSON.stringify(result.data, null, 2)}</pre></details>}
      <footer class="flex items-center justify-between gap-4 border-t border-gray-100 px-6 py-4">
        <p class="text-xs text-gray-400">{t("services.testSingleRequest")}</p>
        {running ? <button type="button" onClick={() => activeRequest.current?.abort()} class="shrink-0 rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">{t("services.testStop")}</button> : <button type="submit" disabled={!valid} class="shrink-0 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-40">{t("services.testRun")}</button>}
      </footer>
    </form>
  </dialog>;
}
