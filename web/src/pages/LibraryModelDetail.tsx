import { useEffect, useRef, useState } from "preact/hooks";
import type { RoutePropsForPath } from "preact-iso";
import { getModelManifest } from "../api/client";
import type { ModelManifestResponse, ModelFileEntry } from "../api/client";
import { locale, t } from "../i18n";
import { LocalInferenceBadge } from "../components/LocalInferenceBadge";
import {
  localInferenceLabelKey,
  localInferenceModeFromSupport,
  localInferenceValueKey,
} from "../utils/localInference";
import { useRuntimeAPIOrigin } from "../utils/runtimeAPIOrigin";
import { displayLocalModelID } from "../modelIds";

type LibraryModelDetailProps = RoutePropsForPath<"/library/detail/:model">;

export function LibraryModelDetail({ model }: LibraryModelDetailProps) {
  void locale.value;

  const modelID = decodeModelParam(model);
  const [manifest, setManifest] = useState<ModelManifestResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [copiedKey, setCopiedKey] = useState("");
  const copyResetRef = useRef<number | null>(null);
  const runtimeAPIOrigin = useRuntimeAPIOrigin();

  useEffect(() => {
    setLoading(true);
    setError("");
    setManifest(null);
    getModelManifest(modelID)
      .then((data) => {
        setManifest(data);
      })
      .catch((err: any) => {
        setError(err?.message || t("lib.failedLoad"));
      })
      .finally(() => {
        setLoading(false);
      });
  }, [modelID]);

  useEffect(() => {
    return () => {
      if (copyResetRef.current !== null) {
        window.clearTimeout(copyResetRef.current);
      }
    };
  }, []);

  const manifestURL = buildManifestURL(modelID, runtimeAPIOrigin);
  const displayedModelID = manifest?.details
    ? displayLocalModelID(manifest.details)
    : modelID.replace(/^(huggingface|modelscope)\//, "");
  const manifestCurl = buildCurlCommand(manifestURL);
  const exampleFile = manifest?.files?.[0];
  const exampleCurl = exampleFile ? buildFileCurlCommand(exampleFile, runtimeAPIOrigin) : "";
  const localInferenceMode = localInferenceModeFromSupport(manifest?.local_inference);

  const handleCopy = async (key: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopiedKey(key);
      if (copyResetRef.current !== null) {
        window.clearTimeout(copyResetRef.current);
      }
      copyResetRef.current = window.setTimeout(() => {
        setCopiedKey("");
        copyResetRef.current = null;
      }, 1500);
    } catch {
      // Ignore clipboard failures so the page remains usable.
    }
  };

  return (
    <div class="page-shell">
      <div class="flex items-start gap-3 mb-4">
        <a
          href="/library"
          class="mt-0.5 w-8 h-8 flex items-center justify-center rounded-full hover:bg-gray-200 transition-colors text-gray-600"
          aria-label={t("lib.back")}
          title={t("lib.back")}
        >
          <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
        </a>
        <div class="min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <h1 class="text-2xl font-bold text-gray-900 break-all">{displayedModelID}</h1>
            {manifest?.details?.format && (
              <span
                class={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
                  manifest.details.format === "gguf" ? "bg-blue-50 text-blue-700" : "bg-purple-50 text-purple-700"
                }`}
              >
                {manifest.details.format.toUpperCase()}
              </span>
            )}
            {manifest && localInferenceMode !== "none" && <LocalInferenceBadge mode={localInferenceMode} prefix="lib" />}
          </div>
          <p class="text-gray-500 text-sm mt-1">{t("lib.detailSubtitle")}</p>
        </div>
      </div>

      {error && (
        <div class="mt-4 rounded-lg bg-red-50 px-4 py-3 text-sm text-red-700 whitespace-pre-line">
          {error}
        </div>
      )}

      {loading ? (
        <div class="mt-6 rounded-2xl bg-white py-16 text-center text-sm text-gray-400">
          {t("lib.loadingDetail")}
        </div>
      ) : manifest ? (
        <div class="mt-6 overflow-hidden rounded-2xl bg-white">
          <DetailFacts
            items={[
              fact(t("lib.fileSize"), fmtSize(manifest.details.size)),
              fact(t("lib.fileCount"), String(manifest.files.length)),
              fact(t("lib.updated"), fmtDate(manifest.details.modified_at)),
              fact(t("lib.format"), manifest.details.format?.toUpperCase() || ""),
              fact(t("lib.origin"), modelOriginLabel(manifest.details.origin)),
              fact(t("lib.pipeline"), manifest.details.pipeline_tag || ""),
              fact(
                t(localInferenceLabelKey("lib")),
                t(localInferenceValueKey(localInferenceMode, "lib")),
                localInferenceMode === "none" ? "danger" : "default",
              ),
            ]}
          />

          {(manifest.details.description || manifest.details.license) && (
            <div class="space-y-2 border-t border-gray-100 px-6 py-5">
              {manifest.details.description && (
                <p class="max-w-3xl text-sm leading-6 text-gray-600 whitespace-pre-wrap">{manifest.details.description}</p>
              )}
              {manifest.details.license && (
                <p class="text-sm text-gray-500">
                  <span class="mr-2 text-gray-400">{t("lib.licenseLabel")}</span>
                  {manifest.details.license}
                </p>
              )}
            </div>
          )}

          <div class="overflow-x-auto border-t border-gray-100">
            <table class="w-full text-sm">
              <thead>
                <tr class="text-left text-xs text-gray-400">
                  <th class="px-6 py-3 font-medium">{t("lib.files")}</th>
                  <th class="px-4 py-3 font-medium">{t("lib.fileSize")}</th>
                  <th class="px-4 py-3 font-medium">{t("lib.sha256")}</th>
                  <th class="px-6 py-3 text-right font-medium">{t("lib.operation")}</th>
                </tr>
              </thead>
              <tbody>
                {manifest.files.length === 0 ? (
                  <tr>
                    <td colSpan={4} class="px-6 py-12 text-center text-gray-400">
                      {t("lib.noFiles")}
                    </td>
                  </tr>
                ) : (
                  manifest.files.map((file) => {
                    const fileURL = absoluteURL(file.download_url, runtimeAPIOrigin);
                    return (
                      <tr key={file.path} class="border-t border-gray-100 align-middle">
                        <td class="px-6 py-3">
                          <div class="flex items-center gap-2">
                            <span class="break-all font-mono text-[13px] text-gray-900">{file.path}</span>
                            {file.lfs && (
                              <span class="shrink-0 rounded-full bg-amber-50 px-2 py-0.5 text-[11px] font-medium text-amber-700">
                                LFS
                              </span>
                            )}
                          </div>
                        </td>
                        <td class="whitespace-nowrap px-4 py-3 text-gray-600">{fmtSizeDetailed(file.size)}</td>
                        <td class="px-4 py-3">
                          <span class="font-mono text-xs text-gray-400" title={file.sha256 || undefined}>
                            {file.sha256 ? shortHash(file.sha256) : t("lib.notAvailable")}
                          </span>
                        </td>
                        <td class="px-6 py-3">
                          <div class="flex justify-end">
                            <button
                              type="button"
                              onClick={() => void handleCopy(`url:${file.path}`, fileURL)}
                              class={`inline-flex h-8 w-8 items-center justify-center rounded-full transition-colors ${
                                copiedKey === `url:${file.path}`
                                  ? "text-green-600"
                                  : "text-gray-400 hover:bg-gray-100 hover:text-gray-700"
                              }`}
                              aria-label={copiedKey === `url:${file.path}` ? t("dash.copied") : t("lib.copyUrl")}
                              title={copiedKey === `url:${file.path}` ? t("dash.copied") : t("lib.copyUrl")}
                            >
                              {copiedKey === `url:${file.path}` ? <CheckIcon /> : <LinkIcon />}
                            </button>
                          </div>
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>

          <section class="space-y-4 border-t border-gray-100 px-6 py-5">
            <div>
              <h2 class="text-sm font-semibold text-gray-900">{t("lib.downloadMethods")}</h2>
              <p class="mt-1 text-sm text-gray-500">{t("lib.downloadHint")}</p>
            </div>
            <div>
              <div class="mb-2 flex items-center justify-between gap-3">
                <span class="text-xs text-gray-400">{t("lib.manifestUrl")}</span>
                <button
                  onClick={() => void handleCopy("manifest-url", manifestURL)}
                  class={`text-xs transition-colors ${
                    copiedKey === "manifest-url" ? "text-green-600" : "text-gray-500 hover:text-indigo-600"
                  }`}
                >
                  {copiedKey === "manifest-url" ? t("dash.copied") : t("lib.copyUrl")}
                </button>
              </div>
              <div class="break-all rounded-lg bg-gray-50 px-4 py-3 font-mono text-xs text-gray-700">
                {manifestURL}
              </div>
            </div>
            <div class="grid grid-cols-1 gap-4 xl:grid-cols-2">
              <CodeBlock
                title={t("lib.manifestCurl")}
                code={manifestCurl}
                copied={copiedKey === "manifest-curl"}
                onCopy={() => void handleCopy("manifest-curl", manifestCurl)}
              />
              {exampleFile && (
                <CodeBlock
                  title={t("lib.fileCurl")}
                  code={exampleCurl}
                  copied={copiedKey === "example-curl"}
                  onCopy={() => void handleCopy("example-curl", exampleCurl)}
                />
              )}
            </div>
          </section>
        </div>
      ) : null}
    </div>
  );
}

function LinkIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M13.828 10.172a4 4 0 010 5.656l-3 3a4 4 0 01-5.656-5.656l1.5-1.5" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M10.172 13.828a4 4 0 010-5.656l3-3a4 4 0 015.656 5.656l-1.5 1.5" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
    </svg>
  );
}

function CodeBlock({
  title,
  code,
  copied,
  onCopy,
}: {
  title: string;
  code: string;
  copied: boolean;
  onCopy: () => void;
}) {
  return (
    <div>
      <div class="flex items-center justify-between mb-2">
        <span class="text-xs text-gray-400">{title}</span>
        <button
          onClick={onCopy}
          class={`text-xs transition-colors flex items-center gap-1 ${copied ? "text-green-600" : "text-gray-500 hover:text-indigo-600"}`}
        >
          {copied ? t("dash.copied") : t("lib.copyCommand")}
        </button>
      </div>
      <pre class="overflow-x-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 px-4 py-3 font-mono text-xs leading-5 text-gray-700">
        {code}
      </pre>
    </div>
  );
}

type DetailFact = {
  label: string;
  value: string;
  tone?: "default" | "danger";
};

function fact(label: string, value: string, tone: "default" | "danger" = "default"): DetailFact {
  const trimmed = value.trim();
  if (!trimmed || trimmed === t("lib.notAvailable")) {
    return { label, value: "" };
  }
  return { label, value: trimmed, tone };
}

function DetailFacts({ items }: { items: DetailFact[] }) {
  const visible = items.filter((item) => item.value);
  if (visible.length === 0) return null;
  return (
    <dl class="grid grid-cols-2 gap-x-8 gap-y-4 px-6 py-5 sm:grid-cols-4">
      {visible.map((item) => (
        <div key={item.label}>
          <dt class="text-xs text-gray-400">{item.label}</dt>
          <dd class={`mt-1 text-sm font-medium break-words ${item.tone === "danger" ? "text-red-600" : "text-gray-900"}`}>
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function shortHash(value: string): string {
  if (value.length <= 18) return value;
  return `${value.slice(0, 10)}…${value.slice(-6)}`;
}

function decodeModelParam(model: string): string {
  try {
    return decodeURIComponent(model);
  } catch {
    return model;
  }
}

function modelOriginLabel(origin?: string): string {
  if (origin === "upload") return t("lib.originUpload");
  if (origin === "marketplace") return t("lib.originMarketplace");
  return t("lib.notAvailable");
}

function absoluteURL(path: string, origin: string): string {
  if (!origin) {
    return path;
  }
  return new URL(path, origin).toString();
}

function buildCurlCommand(url: string): string {
  return `curl ${shellQuote(url)}`;
}

function buildManifestURL(modelID: string, origin: string): string {
  const parts = splitModelID(modelID);
  if (!parts) {
    return absoluteURL(`/api/models/${encodeURIComponent(modelID)}/manifest`, origin);
  }
  return absoluteURL(`/api/models/${encodeURIComponent(parts.namespace)}/${encodeURIComponent(parts.name)}/manifest`, origin);
}

function buildFileCurlCommand(file: ModelFileEntry, origin: string): string {
  const fileURL = absoluteURL(file.download_url, origin);
  const targetPath = shellQuote(file.path);
  return `mkdir -p "$(dirname -- ${targetPath})" && curl -L -C - ${shellQuote(fileURL)} -o ${targetPath}`;
}

function shellQuote(value: string): string {
  return "'" + value.replace(/'/g, `'\"'\"'`) + "'";
}

function splitModelID(modelID: string): { namespace: string; name: string } | null {
  const slash = modelID.indexOf("/");
  if (slash <= 0 || slash === modelID.length - 1) {
    return null;
  }
  return {
    namespace: modelID.slice(0, slash),
    name: modelID.slice(slash + 1),
  };
}

function fmtSize(bytes: number): string {
  if (bytes === 0) return "0 B";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(1)}GB`;
  const mb = bytes / (1024 * 1024);
  if (mb >= 1) return `${mb.toFixed(1)}MB`;
  const kb = bytes / 1024;
  return `${kb.toFixed(0)}KB`;
}

function fmtSizeDetailed(bytes: number): string {
  if (bytes === 0) return "0 B";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(gb >= 100 ? 0 : 1)} GB`;
  const mb = bytes / (1024 * 1024);
  if (mb >= 1) return `${mb.toFixed(mb >= 100 ? 0 : 1)} MB`;
  const kb = bytes / 1024;
  if (kb >= 1) return `${kb.toFixed(kb >= 100 ? 0 : 1)} KB`;
  return `${bytes} B`;
}

function fmtDate(dateStr?: string): string {
  if (!dateStr) {
    return t("lib.notAvailable");
  }
  const language = locale.value === "zh" ? "zh-CN" : "en-US";
  return new Date(dateStr).toLocaleString(language, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}
