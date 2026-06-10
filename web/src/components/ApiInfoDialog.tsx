import { signal } from "@preact/signals";
import { t } from "../i18n";
import { apiExampleModeFromPipelineTag, buildApiExamples } from "../utils/apiExamples";

export function ApiInfoDialog({
  model,
  pipelineTag,
  isVision,
  isEmbedding,
  isASR,
  onClose,
}: {
  model: string;
  pipelineTag?: string;
  isVision?: boolean;
  isEmbedding?: boolean;
  isASR?: boolean;
  onClose: () => void;
}) {
  const baseUrl = `${location.protocol}//${location.host}`;
  const mode = isASR
    ? "asr"
    : isEmbedding
      ? "embedding"
      : apiExampleModeFromPipelineTag(pipelineTag, isVision);
  const examples = buildApiExamples(baseUrl, model, mode);
  const isTextToImage = mode === "text-to-image";
  const isImageToImage = mode === "image-to-image";

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div class="bg-white rounded-2xl shadow-2xl max-w-2xl w-full mx-4 max-h-[85vh] flex flex-col">
        <div class="flex items-center justify-between px-6 py-4 border-b border-gray-100">
          <div>
            <h3 class="text-lg font-bold text-gray-900">{t("dash.apiTitle")}</h3>
            <p class="text-sm text-gray-500 mt-0.5">
              {t("dash.apiModel")}: <span class="font-mono text-indigo-600">{model}</span>
              {isVision && <span class="ml-2 px-1.5 py-0.5 text-xs bg-purple-50 text-purple-700 rounded">Vision</span>}
              {isEmbedding && <span class="ml-2 px-1.5 py-0.5 text-xs bg-emerald-50 text-emerald-700 rounded">Embedding</span>}
              {isASR && <span class="ml-2 px-1.5 py-0.5 text-xs bg-cyan-50 text-cyan-700 rounded">ASR</span>}
              {isTextToImage && <span class="ml-2 px-1.5 py-0.5 text-xs bg-fuchsia-50 text-fuchsia-700 rounded">Text-to-Image</span>}
              {isImageToImage && <span class="ml-2 px-1.5 py-0.5 text-xs bg-orange-50 text-orange-700 rounded">Image-to-Image</span>}
            </p>
          </div>
        </div>
        <div class="flex-1 overflow-auto px-6 py-4 space-y-5">
          <CodeBlock title={t("dash.apiCurl")} code={examples.curl} />
          <CodeBlock title={t("dash.apiPython")} code={examples.python} />
          <CodeBlock title={t("dash.apiJs")} code={examples.javascript} />
        </div>
      </div>
    </div>
  );
}

function CodeBlock({ title, code }: { title: string; code: string }) {
  const copied = signal(false);
  const handleCopy = () => {
    navigator.clipboard.writeText(code).then(() => {
      copied.value = true;
      setTimeout(() => { copied.value = false; }, 2000);
    }).catch(() => {});
  };

  return (
    <div>
      <div class="flex items-center justify-between mb-1.5">
        <span class="text-sm font-medium text-gray-700">{title}</span>
        <button onClick={handleCopy} class={`text-xs transition-colors flex items-center gap-1 ${copied.value ? "text-green-600" : "text-gray-400 hover:text-indigo-600"}`}>
          {copied.value ? (
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          ) : (
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
            </svg>
          )}
          {copied.value ? t("dash.copied") : t("dash.copy")}
        </button>
      </div>
      <pre class="bg-gray-900 text-gray-100 rounded-lg p-4 text-xs leading-5 overflow-x-auto font-mono whitespace-pre-wrap break-all">
        {code}
      </pre>
    </div>
  );
}
