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

type LibraryModelDetailProps = RoutePropsForPath<"/library/detail/:model">;

export function LibraryModelDetail({ model }: LibraryModelDetailProps) {
  void locale.value;

  const modelID = decodeModelParam(model);
  const [manifest, setManifest] = useState<ModelManifestResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [copiedKey, setCopiedKey] = useState("");
  const copyResetRef = useRef<number | null>(null);

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

  const manifestURL = buildManifestURL(modelID);
  const manifestCurl = buildCurlCommand(manifestURL);
  const exampleFile = manifest?.files?.[0];
  const exampleCurl = exampleFile ? buildFileCurlCommand(exampleFile) : "";
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
    <div class="p-8 max-w-5xl mx-auto">
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
            <h1 class="text-2xl font-bold text-gray-900 break-all">{modelID}</h1>
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
        <div class="mt-4 flex items-start gap-2 bg-red-50 border border-red-200 text-red-700 text-sm px-4 py-3 rounded-lg">
          <svg class="w-4 h-4 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span class="whitespace-pre-line flex-1">{error}</span>
        </div>
      )}

      {loading ? (
        <div class="bg-white rounded-xl border border-gray-200 mt-6 px-6 py-12 text-center text-gray-400">
          {t("lib.loadingDetail")}
        </div>
      ) : manifest ? (
        <div class="space-y-6 mt-6">
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
            <SummaryTile label={t("lib.format")} value={manifest.details.format?.toUpperCase() || t("lib.notAvailable")} />
            <SummaryTile
              label={t(localInferenceLabelKey("lib"))}
              value={t(localInferenceValueKey(localInferenceMode, "lib"))}
              tone={localInferenceMode === "none" ? "danger" : "default"}
            />
            <SummaryTile label={t("lib.fileSize")} value={fmtSize(manifest.details.size)} />
            <SummaryTile label={t("lib.fileCount")} value={String(manifest.files.length)} />
            <SummaryTile label={t("lib.updated")} value={fmtDate(manifest.details.modified_at)} />
            <SummaryTile label={t("lib.pipeline")} value={manifest.details.pipeline_tag || t("lib.notAvailable")} />
          </div>

          {(manifest.details.description || manifest.details.license) && (
            <section class="bg-white rounded-xl border border-gray-200 p-5 space-y-3">
              {manifest.details.description && (
                <div>
                  <h2 class="text-sm font-semibold text-gray-900 mb-1">{t("lib.description")}</h2>
                  <p class="text-sm text-gray-600 whitespace-pre-wrap">{manifest.details.description}</p>
                </div>
              )}
              {manifest.details.license && (
                <div class="text-sm text-gray-600">
                  <span class="font-semibold text-gray-900 mr-2">{t("lib.licenseLabel")}</span>
                  <span>{manifest.details.license}</span>
                </div>
              )}
            </section>
          )}

          <section class="bg-white rounded-xl border border-gray-200 p-5 space-y-4">
            <div class="flex items-center justify-between gap-3 flex-wrap">
              <div>
                <h2 class="text-lg font-semibold text-gray-900">{t("lib.downloadMethods")}</h2>
                <p class="text-sm text-gray-500 mt-1">{t("lib.downloadHint")}</p>
              </div>
            </div>

            <div>
              <div class="flex items-center justify-between gap-3 flex-wrap mb-2">
                <span class="text-sm font-medium text-gray-700">{t("lib.manifestUrl")}</span>
                <button
                  onClick={() => void handleCopy("manifest-url", manifestURL)}
                  class={`text-xs transition-colors flex items-center gap-1 ${
                    copiedKey === "manifest-url" ? "text-green-600" : "text-gray-500 hover:text-indigo-600"
                  }`}
                >
                  {copiedKey === "manifest-url" ? t("dash.copied") : t("lib.copyUrl")}
                </button>
              </div>
              <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-700 font-mono break-all">
                {manifestURL}
              </div>
            </div>

            <div class="grid grid-cols-1 xl:grid-cols-2 gap-4">
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

          <section class="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div class="px-5 py-4 border-b border-gray-100">
              <h2 class="text-lg font-semibold text-gray-900">{t("lib.files")}</h2>
            </div>
            <div class="overflow-x-auto">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-gray-100 text-left text-gray-500 bg-gray-50">
                    <th class="px-4 py-3 font-medium">{t("lib.path")}</th>
                    <th class="px-4 py-3 font-medium">{t("lib.fileSize")}</th>
                    <th class="px-4 py-3 font-medium">{t("lib.sha256")}</th>
                    <th class="px-4 py-3 font-medium text-right">{t("lib.operation")}</th>
                  </tr>
                </thead>
                <tbody>
                  {manifest.files.length === 0 ? (
                    <tr>
                      <td colSpan={4} class="text-center py-12 text-gray-400">
                        {t("lib.noFiles")}
                      </td>
                    </tr>
                  ) : (
                    manifest.files.map((file) => {
                      const fileURL = absoluteURL(file.download_url);
                      const curlCommand = buildFileCurlCommand(file);
                      return (
                        <tr key={file.path} class="border-b border-gray-50 hover:bg-gray-50/50 align-top">
                          <td class="px-4 py-3">
                            <div class="font-mono text-gray-900 break-all">{file.path}</div>
                            {file.lfs && (
                              <span class="inline-flex mt-2 items-center px-2 py-0.5 rounded text-xs font-medium bg-amber-50 text-amber-700">
                                LFS
                              </span>
                            )}
                          </td>
                          <td class="px-4 py-3 text-gray-600 whitespace-nowrap">{fmtSizeDetailed(file.size)}</td>
                          <td class="px-4 py-3">
                            <div class="font-mono text-xs text-gray-500 break-all">
                              {file.sha256 || t("lib.notAvailable")}
                            </div>
                          </td>
                          <td class="px-4 py-3">
                            <div class="flex items-center justify-end gap-3 flex-wrap">
                              <button
                                onClick={() => void handleCopy(`url:${file.path}`, fileURL)}
                                class={`text-sm transition-colors ${
                                  copiedKey === `url:${file.path}` ? "text-green-600" : "text-gray-500 hover:text-indigo-600"
                                }`}
                              >
                                {copiedKey === `url:${file.path}` ? t("dash.copied") : t("lib.copyUrl")}
                              </button>
                              <button
                                onClick={() => void handleCopy(`curl:${file.path}`, curlCommand)}
                                class={`text-sm transition-colors ${
                                  copiedKey === `curl:${file.path}` ? "text-green-600" : "text-gray-500 hover:text-indigo-600"
                                }`}
                              >
                                {copiedKey === `curl:${file.path}` ? t("dash.copied") : t("lib.copyCurl")}
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
          </section>
        </div>
      ) : null}
    </div>
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
        <span class="text-sm font-medium text-gray-700">{title}</span>
        <button
          onClick={onCopy}
          class={`text-xs transition-colors flex items-center gap-1 ${copied ? "text-green-600" : "text-gray-500 hover:text-indigo-600"}`}
        >
          {copied ? t("dash.copied") : t("lib.copyCommand")}
        </button>
      </div>
      <pre class="bg-gray-900 text-gray-100 rounded-lg p-4 text-xs leading-5 overflow-x-auto font-mono whitespace-pre-wrap break-all">
        {code}
      </pre>
    </div>
  );
}

function SummaryTile({ label, value, tone = "default" }: { label: string; value: string; tone?: "default" | "danger" }) {
  const danger = tone === "danger";
  return (
    <div class={`rounded-xl border px-4 py-3 ${danger ? "border-red-200 bg-red-50" : "border-gray-200 bg-white"}`}>
      <div class={`text-xs font-medium uppercase tracking-wide ${danger ? "text-red-500" : "text-gray-400"}`}>{label}</div>
      <div class={`mt-1 text-sm font-semibold break-words ${danger ? "text-red-700" : "text-gray-900"}`}>{value}</div>
    </div>
  );
}

function decodeModelParam(model: string): string {
  try {
    return decodeURIComponent(model);
  } catch {
    return model;
  }
}

function absoluteURL(path: string): string {
  if (typeof window === "undefined") {
    return path;
  }
  return new URL(path, window.location.origin).toString();
}

function buildCurlCommand(url: string): string {
  return `curl ${shellQuote(url)}`;
}

function buildManifestURL(modelID: string): string {
  const parts = splitModelID(modelID);
  if (!parts) {
    return absoluteURL(`/api/models/${encodeURIComponent(modelID)}/manifest`);
  }
  return absoluteURL(`/api/models/${encodeURIComponent(parts.namespace)}/${encodeURIComponent(parts.name)}/manifest`);
}

function buildFileCurlCommand(file: ModelFileEntry): string {
  const fileURL = absoluteURL(file.download_url);
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
