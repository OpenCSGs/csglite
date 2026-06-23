import { useEffect } from "preact/hooks";
import { signal } from "@preact/signals";
import { getTags } from "../api/client";
import type { ModelInfo, ModelTokenPrice } from "../api/client";
import { t, locale, type Locale } from "../i18n";

const allModels = signal<ModelInfo[]>([]);
const loading = signal(true);
const error = signal("");
const modelSearch = signal("");

function loadPricing(refresh = false) {
  loading.value = true;
  error.value = "";
  getTags(refresh ? { refresh: true } : undefined)
    .then((models) => {
      allModels.value = models;
      loading.value = false;
    })
    .catch((err) => {
      error.value = err.message || t("pricing.loadFailed");
      loading.value = false;
    });
}

export function Pricing() {
  void locale.value;

  useEffect(() => {
    loadPricing();
  }, []);

  const models = filterModels(allModels.value, modelSearch.value);

  const localModels = models.filter(isLocalModel);
  const cloudModels = models.filter((m) => m.source === "cloud");
  const thirdPartyModels = models.filter((m) => m.source && m.source !== "local" && m.source !== "cloud");
  const sortedCloudModels = sortCloudModels(cloudModels);
  const pricedCloudCount = cloudModels.filter(hasPricing).length;

  return (
    <div class="p-8 max-w-5xl mx-auto">
      <div class="flex items-start justify-between gap-4 mb-6">
        <div>
          <h1 class="text-2xl font-bold text-gray-900">{t("pricing.title")}</h1>
          <p class="text-gray-500 text-sm mt-1">{t("pricing.subtitle")}</p>
        </div>
        <button
          type="button"
          onClick={() => loadPricing(true)}
          disabled={loading.value}
          class="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-gray-200 bg-white text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 disabled:cursor-not-allowed"
        >
          <svg class={`w-4 h-4 ${loading.value ? "animate-spin" : ""}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          {loading.value ? t("pricing.refreshing") : t("pricing.refresh")}
        </button>
      </div>

      {error.value && (
        <div class="mb-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error.value}
        </div>
      )}

      <div class="mb-6">
        <div class="relative">
          <svg class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M10.5 18a7.5 7.5 0 110-15 7.5 7.5 0 010 15z" />
          </svg>
          <input
            type="search"
            value={modelSearch.value}
            onInput={(event) => {
              modelSearch.value = event.currentTarget.value;
            }}
            placeholder={t("pricing.searchPlaceholder")}
            aria-label={t("pricing.searchPlaceholder")}
            class="w-full rounded-xl border border-gray-200 bg-white py-2.5 pl-9 pr-3 text-sm text-gray-900 outline-none transition focus:border-indigo-300 focus:ring-2 focus:ring-indigo-100"
          />
        </div>
      </div>

      <div class="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
        <SummaryCard
          label={t("pricing.gateway")}
          value={`${pricedCloudCount}/${cloudModels.length}`}
          hint={t("pricing.gatewayHint")}
          tone="indigo"
        />
        <SummaryCard
          label={t("pricing.local")}
          value={String(localModels.length)}
          hint={t("pricing.localHint")}
          tone="emerald"
        />
        <SummaryCard
          label={t("pricing.thirdParty")}
          value={String(thirdPartyModels.length)}
          hint={t("pricing.thirdPartyHint")}
          tone="slate"
        />
      </div>

      <section class="bg-white rounded-xl border border-gray-200 overflow-hidden mb-6">
        <div class="px-5 py-4 border-b border-gray-100 flex items-center justify-between gap-3">
          <div>
            <h2 class="text-lg font-semibold text-gray-900">{t("pricing.gateway")}</h2>
            <p class="text-sm text-gray-500 mt-1">{t("pricing.gatewaySubtitle")}</p>
          </div>
          <span class="text-xs font-medium text-indigo-700 bg-indigo-50 px-2.5 py-1 rounded-full">
            {t("pricing.perMillion")}
          </span>
        </div>

        {loading.value ? (
          <PricingSkeleton />
        ) : sortedCloudModels.length === 0 ? (
          <EmptyState text={t("pricing.noCloud")} />
        ) : (
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-100">
              <thead class="bg-gray-50">
                <tr>
                  <th class="px-5 py-3 text-left text-xs font-semibold text-gray-500 uppercase tracking-wide">{t("pricing.model")}</th>
                  <th class="px-5 py-3 text-left text-xs font-semibold text-gray-500 uppercase tracking-wide">{t("pricing.type")}</th>
                  <th class="px-5 py-3 text-right text-xs font-semibold text-gray-500 uppercase tracking-wide">{t("pricing.input")}</th>
                  <th class="px-5 py-3 text-right text-xs font-semibold text-gray-500 uppercase tracking-wide">{t("pricing.output")}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                {sortedCloudModels.map((model) => (
                  <tr key={model.model} class="hover:bg-gray-50/70 transition-colors">
                    <td class="px-5 py-4 min-w-[280px]">
                      <div class="font-medium text-gray-900">{model.display_name || model.label || model.model}</div>
                      <div class="text-xs text-gray-500 mt-1 break-all">{model.model}</div>
                    </td>
                    <td class="px-5 py-4 whitespace-nowrap">
                      <div class="flex flex-col items-start gap-1.5">
                        <span class={modelTypeClass(model.llm_type)}>{modelTypeLabel(model.llm_type)}</span>
                        <span class="text-xs text-gray-500">{model.owned_by || t("pricing.unknownOwner")}</span>
                      </div>
                    </td>
                    <td class="px-5 py-4 text-right whitespace-nowrap">
                      <PriceCell price={model.pricing?.input_token_price} />
                    </td>
                    <td class="px-5 py-4 text-right whitespace-nowrap">
                      <PriceCell price={model.pricing?.output_token_price} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <ModelGroupCard
          title={t("pricing.local")}
          description={t("pricing.localDescription")}
          models={localModels}
          emptyText={t("pricing.noLocal")}
          priceText={t("pricing.free")}
        />
        <ModelGroupCard
          title={t("pricing.thirdParty")}
          description={t("pricing.thirdPartyDescription")}
          models={thirdPartyModels}
          emptyText={t("pricing.noThirdParty")}
          priceText={t("pricing.thirdPartyNote")}
        />
      </div>
    </div>
  );
}

function SummaryCard({ label, value, hint, tone }: { label: string; value: string; hint: string; tone: "indigo" | "emerald" | "slate" }) {
  const toneClass = {
    indigo: "bg-indigo-50 text-indigo-700",
    emerald: "bg-emerald-50 text-emerald-700",
    slate: "bg-slate-50 text-slate-700",
  }[tone];

  return (
    <div class="bg-white rounded-xl border border-gray-200 p-5">
      <div class={`inline-flex px-2.5 py-1 rounded-full text-xs font-semibold ${toneClass}`}>{label}</div>
      <div class="mt-4 text-3xl font-bold text-gray-900">{value}</div>
      <p class="mt-1 text-sm text-gray-500">{hint}</p>
    </div>
  );
}

function PriceCell({ price }: { price?: ModelTokenPrice }) {
  if (!price) {
    return <span class="text-sm text-gray-400">{t("pricing.notAvailable")}</span>;
  }
  return (
    <div>
      <div class="text-sm font-semibold text-gray-900">{formatTokenPrice(price, locale.value)}</div>
      <div class="text-xs text-gray-400">{t("pricing.perMillionShort")}</div>
    </div>
  );
}

function ModelGroupCard({ title, description, models, emptyText, priceText }: { title: string; description: string; models: ModelInfo[]; emptyText: string; priceText: string }) {
  return (
    <section class="bg-white rounded-xl border border-gray-200 p-5">
      <div class="flex items-start justify-between gap-3 mb-4">
        <div>
          <h2 class="text-lg font-semibold text-gray-900">{title}</h2>
          <p class="text-sm text-gray-500 mt-1">{description}</p>
        </div>
        <span class="text-xs font-medium text-gray-500 bg-gray-50 px-2.5 py-1 rounded-full">{models.length}</span>
      </div>
      {models.length === 0 ? (
        <EmptyState text={emptyText} compact />
      ) : (
        <div class="divide-y divide-gray-100">
          {models.slice(0, 5).map((model) => (
            <div key={`${model.source || "local"}-${model.model}`} class="py-3 flex items-center justify-between gap-4">
              <div class="min-w-0">
                <div class="text-sm font-medium text-gray-900 truncate">{model.display_name || model.label || model.model}</div>
                <div class="text-xs text-gray-500 truncate">{model.model}</div>
              </div>
              <div class="text-sm font-medium text-gray-700 whitespace-nowrap">{priceText}</div>
            </div>
          ))}
          {models.length > 5 && (
            <div class="pt-3 text-xs text-gray-400">{t("pricing.moreModels", models.length - 5)}</div>
          )}
        </div>
      )}
    </section>
  );
}

function PricingSkeleton() {
  return (
    <div class="p-5 space-y-3">
      {[0, 1, 2, 3].map((item) => (
        <div key={item} class="animate-pulse flex items-center gap-4">
          <div class="h-10 flex-1 bg-gray-100 rounded-lg" />
          <div class="h-10 w-24 bg-gray-100 rounded-lg" />
          <div class="h-10 w-24 bg-gray-100 rounded-lg" />
        </div>
      ))}
    </div>
  );
}

function EmptyState({ text, compact = false }: { text: string; compact?: boolean }) {
  return (
    <div class={`${compact ? "py-8" : "py-14"} text-center text-sm text-gray-400`}>
      {text}
    </div>
  );
}

function isLocalModel(model: ModelInfo): boolean {
  return model.source === "local" || model.format === "gguf" || model.format === "safetensors";
}

function hasPricing(model: ModelInfo): boolean {
  return Boolean(model.pricing?.input_token_price || model.pricing?.output_token_price);
}

function filterModels(models: ModelInfo[], query: string): ModelInfo[] {
  const term = query.trim().toLowerCase();
  if (!term) return models;
  return models.filter((model) => {
    const searchable = [
      model.model,
      model.display_name,
      model.label,
      model.owned_by,
      model.source,
      model.llm_type,
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return searchable.includes(term);
  });
}

function sortCloudModels(models: ModelInfo[]): ModelInfo[] {
  return [...models].sort((a, b) => {
    const aPriced = hasPricing(a) ? 0 : 1;
    const bPriced = hasPricing(b) ? 0 : 1;
    if (aPriced !== bPriced) return aPriced - bPriced;

    const aOpenCSG = (a.owned_by || "").toLowerCase() === "opencsg" ? 0 : 1;
    const bOpenCSG = (b.owned_by || "").toLowerCase() === "opencsg" ? 0 : 1;
    if (aOpenCSG !== bOpenCSG) return aOpenCSG - bOpenCSG;

    return (a.display_name || a.model).localeCompare(b.display_name || b.model);
  });
}

function modelTypeLabel(llmType?: string): string {
  switch ((llmType || "").toLowerCase()) {
    case "serverless":
      return t("pricing.typeServerless");
    case "external_llm":
      return t("pricing.typeExternal");
    case "inference":
      return t("pricing.typeInference");
    default:
      return t("pricing.typeCloud");
  }
}

function modelTypeClass(llmType?: string): string {
  const base = "inline-flex px-2 py-0.5 rounded-full text-xs font-medium";
  switch ((llmType || "").toLowerCase()) {
    case "serverless":
      return `${base} bg-indigo-50 text-indigo-700`;
    case "external_llm":
      return `${base} bg-blue-50 text-blue-700`;
    case "inference":
      return `${base} bg-emerald-50 text-emerald-700`;
    default:
      return `${base} bg-gray-100 text-gray-600`;
  }
}

function formatTokenPrice(price: ModelTokenPrice, currentLocale: Locale): string {
  const number = price.price_per_million.toLocaleString(currentLocale === "zh" ? "zh-CN" : "en-US", {
    maximumFractionDigits: 6,
  });
  return `${price.currency || ""}${number}`;
}
