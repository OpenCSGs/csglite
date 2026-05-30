import { useEffect, useState } from "preact/hooks";
import { signal, computed } from "@preact/signals";
import {
  getMarketplaceModels,
  getMarketplaceDatasets,
  getTags,
  getDatasetTags,
} from "../api/client";
import type { MarketplaceModel, MarketplaceDataset } from "../api/client";
import { t, locale } from "../i18n";
import { startDownload, getDownloadTask } from "../downloads";
import {
  MarketplaceModelDetailDialog,
  getMarketplaceModelFormats,
} from "../components/MarketplaceModelDetailDialog";

type Tab = "models" | "datasets";
type ViewMode = "grid" | "list";
type ModelFrameworkFilter = "" | "gguf" | "safetensors";
type FilterOption<T extends string> = {
  value: T;
  label: string;
};
const modelParamsMinLimit = 0;
const modelParamsMaxLimit = 1000;
const modelParamsSliderMinLimit = 1;
const modelParamsSliderMaxLimit = 100;
const modelParamsQuickRanges = [
  { key: "under-1", label: "mp.modelSizeUnder1B", min: modelParamsMinLimit, max: 0.99999 },
  { key: "2", label: "mp.modelSizeB", min: 2, max: 2 },
  { key: "3", label: "mp.modelSizeB", min: 3, max: 3 },
  { key: "6", label: "mp.modelSizeB", min: 6, max: 6 },
  { key: "12", label: "mp.modelSizeB", min: 12, max: 12 },
  { key: "32", label: "mp.modelSizeB", min: 32, max: 32 },
  { key: "over-100", label: "mp.modelSizeOver100B", min: modelParamsSliderMaxLimit, max: modelParamsMaxLimit },
];
const modelFrameworkOptions: FilterOption<ModelFrameworkFilter>[] = [
  { value: "", label: "mp.allModelTypes" },
  { value: "gguf", label: "mp.modelTypeGGUF" },
  { value: "safetensors", label: "mp.modelTypeSafeTensors" },
];
const activeTab = signal<Tab>("models");
const searchQuery = signal("");
const sortBy = signal("trending");
const frameworkFilter = signal<ModelFrameworkFilter>("");
const modelParamsMin = signal(modelParamsMinLimit);
const modelParamsMax = signal(modelParamsMaxLimit);
const viewMode = signal<ViewMode>("grid");
const page = signal(1);
const perPage = 16;

const models = signal<MarketplaceModel[]>([]);
const datasets = signal<MarketplaceDataset[]>([]);
const total = signal(0);
const loading = signal(false);

const localModelNames = signal<Set<string>>(new Set());
const localDatasetNames = signal<Set<string>>(new Set());

function loadLocalModels() {
  getTags().then((m) => {
    localModelNames.value = new Set(m.map((x) => x.name));
  }).catch(() => {});
}

function loadLocalDatasets() {
  getDatasetTags().then((d) => {
    localDatasetNames.value = new Set(d.map((x) => x.name));
  }).catch(() => {});
}

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / perPage)));

async function loadData() {
  loading.value = true;
  try {
    if (activeTab.value === "models") {
      const modelParamsRangeActive = modelParamsMin.value > modelParamsMinLimit || modelParamsMax.value < modelParamsMaxLimit;
      const res = await getMarketplaceModels({
        search: searchQuery.value,
        sort: sortBy.value,
        framework: frameworkFilter.value || undefined,
        modelParamsMin: modelParamsMin.value > modelParamsMinLimit ? String(modelParamsMin.value) : undefined,
        modelParamsMax: modelParamsRangeActive ? formatModelParamsMax(modelParamsMax.value) : undefined,
        page: page.value,
        per: perPage,
      });
      models.value = res.data || [];
      total.value = res.total;
    } else {
      const res = await getMarketplaceDatasets({
        search: searchQuery.value,
        sort: sortBy.value,
        page: page.value,
        per: perPage,
      });
      datasets.value = res.data || [];
      total.value = res.total;
    }
  } catch {
    /* ignore */
  }
  loading.value = false;
}

export function Marketplace() {
  void locale.value;
  const [selectedModelPath, setSelectedModelPath] = useState("");
  const [filtersOpen, setFiltersOpen] = useState(false);

  useEffect(() => {
    loadLocalModels();
    loadLocalDatasets();
  }, []);

  useEffect(() => {
    page.value = 1;
    loadData();
  }, [activeTab.value, sortBy.value, frameworkFilter.value, modelParamsMin.value, modelParamsMax.value]);

  useEffect(() => {
    if (activeTab.value !== "models") {
      setSelectedModelPath("");
      setFiltersOpen(false);
    }
  }, [activeTab.value]);

  const handleSearch = (e: Event) => {
    e.preventDefault();
    page.value = 1;
    loadData();
  };

  const handleDownload = (modelPath: string) => {
    startDownload("model", modelPath, () => {
      loadLocalModels();
    });
  };

  const handleDatasetDownload = (datasetPath: string) => {
    startDownload("dataset", datasetPath, () => {
      loadLocalDatasets();
    });
  };
  const hasModelFilters = frameworkFilter.value !== ""
    || modelParamsMin.value !== modelParamsMinLimit
    || modelParamsMax.value !== modelParamsMaxLimit;
  const clearModelFilters = () => {
    frameworkFilter.value = "";
    modelParamsMin.value = modelParamsMinLimit;
    modelParamsMax.value = modelParamsMaxLimit;
  };

  return (
    <div class="p-8 max-w-5xl mx-auto">
      <h1 class="text-2xl font-bold text-gray-900">{t("mp.title")}</h1>
      <p class="text-gray-500 text-sm mt-1 mb-6">{t("mp.subtitle")}</p>

      {/* Tabs + Search + View Toggle */}
      <div class="flex items-center gap-3 mb-6 flex-wrap">
        <div class="flex bg-gray-100 rounded-lg p-0.5">
          <TabButton label={t("mp.models")} active={activeTab.value === "models"} onClick={() => (activeTab.value = "models")} />
          <TabButton label={t("mp.datasets")} active={activeTab.value === "datasets"} onClick={() => (activeTab.value = "datasets")} />
        </div>
        <form onSubmit={handleSearch} class="flex-1 min-w-[200px]">
          <div class="relative">
            <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <input
              type="text"
              placeholder={t("mp.search")}
              class="w-full pl-10 pr-4 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
              value={searchQuery.value}
              onInput={(e) => (searchQuery.value = (e.target as HTMLInputElement).value)}
            />
          </div>
        </form>
        <div class="relative">
          <select
            class="appearance-none border border-gray-200 rounded-lg pl-8 pr-3 py-2 text-sm text-gray-600 focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={sortBy.value}
            onChange={(e) => (sortBy.value = (e.target as HTMLSelectElement).value)}
          >
            <option value="trending">{t("mp.trending")}</option>
            <option value="recently_update">{t("mp.recentlyUpdated")}</option>
            <option value="most_download">{t("mp.mostDownloads")}</option>
            <option value="most_favorite">{t("mp.mostLikes")}</option>
          </select>
          <svg class="absolute left-2.5 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3 4h13M3 8h9m-9 4h6m4 0l4-4m0 0l4 4m-4-4v12" />
          </svg>
        </div>
        {activeTab.value === "models" && (
          <div class="relative">
            <button
              type="button"
              onClick={() => setFiltersOpen((open) => !open)}
              class={`inline-flex items-center gap-1.5 px-3 py-2 text-sm border rounded-lg transition-colors ${
                filtersOpen || hasModelFilters
                  ? "bg-indigo-50 text-indigo-700 border-indigo-200"
                  : "bg-white text-gray-600 border-gray-200 hover:bg-gray-50"
              }`}
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z" />
              </svg>
              {t("mp.filter")}
              {hasModelFilters && <span class="w-1.5 h-1.5 rounded-full bg-indigo-500" />}
            </button>
            {filtersOpen && (
              <div class="absolute right-0 z-20 mt-2 w-96 max-w-[calc(100vw-2rem)] rounded-2xl border border-gray-200 bg-white p-4 shadow-xl">
                <div class="flex items-center justify-between gap-3 mb-4">
                  <div>
                    <div class="text-sm font-semibold text-gray-900">{t("mp.filter")}</div>
                    <div class="text-xs text-gray-500 mt-0.5">
                      {hasModelFilters ? t("mp.activeFilters") : t("mp.noActiveFilters")}
                    </div>
                  </div>
                  {hasModelFilters && (
                    <button
                      type="button"
                      onClick={clearModelFilters}
                      class="px-2.5 py-1.5 text-xs font-medium text-gray-500 hover:text-gray-900 hover:bg-gray-50 border border-gray-200 rounded-full transition-colors"
                    >
                      {t("mp.clearFilters")}
                    </button>
                  )}
                </div>
                <div class="space-y-5">
                  <FilterPillGroup
                    label={t("mp.modelType")}
                    value={frameworkFilter.value}
                    options={modelFrameworkOptions.map((option) => ({ ...option, label: t(option.label) }))}
                    onChange={(value) => (frameworkFilter.value = value)}
                  />
                  <ModelParamsRangeSlider
                    min={modelParamsMin.value}
                    max={modelParamsMax.value}
                    onMinChange={(value) => {
                      modelParamsMin.value = Math.min(value, modelParamsMax.value);
                    }}
                    onMaxChange={(value) => {
                      modelParamsMax.value = Math.max(value, modelParamsMin.value);
                    }}
                    onRangeChange={(min, max) => {
                      modelParamsMin.value = min;
                      modelParamsMax.value = max;
                    }}
                  />
                </div>
              </div>
            )}
          </div>
        )}
        <div class="flex border border-gray-200 rounded-lg overflow-hidden">
          <button
            onClick={() => (viewMode.value = "grid")}
            class={`p-2 ${viewMode.value === "grid" ? "bg-indigo-50 text-indigo-600" : "text-gray-400 hover:bg-gray-50"}`}
          >
            <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 16 16">
              <path d="M1 2.5A1.5 1.5 0 012.5 1h3A1.5 1.5 0 017 2.5v3A1.5 1.5 0 015.5 7h-3A1.5 1.5 0 011 5.5v-3zm8 0A1.5 1.5 0 0110.5 1h3A1.5 1.5 0 0115 2.5v3A1.5 1.5 0 0113.5 7h-3A1.5 1.5 0 019 5.5v-3zm-8 8A1.5 1.5 0 012.5 9h3A1.5 1.5 0 017 10.5v3A1.5 1.5 0 015.5 15h-3A1.5 1.5 0 011 13.5v-3zm8 0A1.5 1.5 0 0110.5 9h3a1.5 1.5 0 011.5 1.5v3a1.5 1.5 0 01-1.5 1.5h-3A1.5 1.5 0 019 13.5v-3z" />
            </svg>
          </button>
          <button
            onClick={() => (viewMode.value = "list")}
            class={`p-2 ${viewMode.value === "list" ? "bg-indigo-50 text-indigo-600" : "text-gray-400 hover:bg-gray-50"}`}
          >
            <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 16 16">
              <path fill-rule="evenodd" d="M2.5 12a.5.5 0 01.5-.5h10a.5.5 0 010 1H3a.5.5 0 01-.5-.5zm0-4a.5.5 0 01.5-.5h10a.5.5 0 010 1H3a.5.5 0 01-.5-.5zm0-4a.5.5 0 01.5-.5h10a.5.5 0 010 1H3a.5.5 0 01-.5-.5z" />
            </svg>
          </button>
        </div>
      </div>

      {/* Content */}
      {loading.value ? (
        <div class="text-center py-16 text-gray-400">{t("mp.loading")}</div>
      ) : activeTab.value === "models" ? (
        viewMode.value === "grid" ? (
          <div class="grid grid-cols-2 gap-4">
            {models.value.map((m) => (
              <ModelGridCard
                key={m.id}
                model={m}
                pulling={getDownloadTask("model", m.path)}
                isLocal={localModelNames.value.has(m.path)}
                onDownload={handleDownload}
                onOpenDetail={setSelectedModelPath}
              />
            ))}
            {models.value.length === 0 && <p class="col-span-2 text-center py-16 text-gray-400">{t("mp.noModels")}</p>}
          </div>
        ) : (
          <div class="space-y-0 divide-y divide-gray-100">
            {models.value.map((m) => (
              <ModelCard
                key={m.id}
                model={m}
                pulling={getDownloadTask("model", m.path)}
                isLocal={localModelNames.value.has(m.path)}
                onDownload={handleDownload}
                onOpenDetail={setSelectedModelPath}
              />
            ))}
            {models.value.length === 0 && <p class="text-center py-16 text-gray-400">{t("mp.noModels")}</p>}
          </div>
        )
      ) : viewMode.value === "grid" ? (
        <div class="grid grid-cols-2 gap-4">
          {datasets.value.map((d) => (
            <DatasetGridCard key={d.id} dataset={d} pulling={getDownloadTask("dataset", d.path)} isLocal={localDatasetNames.value.has(d.path)} onDownload={handleDatasetDownload} />
          ))}
          {datasets.value.length === 0 && <p class="col-span-2 text-center py-16 text-gray-400">{t("mp.noDatasets")}</p>}
        </div>
      ) : (
        <div class="space-y-0 divide-y divide-gray-100">
          {datasets.value.map((d) => (
            <DatasetCard key={d.id} dataset={d} pulling={getDownloadTask("dataset", d.path)} isLocal={localDatasetNames.value.has(d.path)} onDownload={handleDatasetDownload} />
          ))}
          {datasets.value.length === 0 && <p class="text-center py-16 text-gray-400">{t("mp.noDatasets")}</p>}
        </div>
      )}

      {/* Pagination */}
      {totalPages.value > 1 && (
        <div class="flex items-center justify-center gap-2 mt-8">
          <button
            disabled={page.value <= 1}
            onClick={() => { page.value--; loadData(); }}
            class="px-3 py-1.5 text-sm border border-gray-200 rounded-lg disabled:opacity-40 hover:bg-gray-50"
          >
            {t("mp.prev")}
          </button>
          <span class="text-sm text-gray-500">
            {t("mp.page", page.value, totalPages.value)}
          </span>
          <button
            disabled={page.value >= totalPages.value}
            onClick={() => { page.value++; loadData(); }}
            class="px-3 py-1.5 text-sm border border-gray-200 rounded-lg disabled:opacity-40 hover:bg-gray-50"
          >
            {t("mp.next")}
          </button>
        </div>
      )}

      {selectedModelPath && (
        <MarketplaceModelDetailDialog
          modelPath={selectedModelPath}
          isLocal={localModelNames.value.has(selectedModelPath)}
          onClose={() => setSelectedModelPath("")}
        />
      )}

    </div>
  );
}

function TabButton({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      class={`px-4 py-1.5 text-sm font-medium rounded-md transition-colors ${
        active ? "bg-white text-gray-900 shadow-sm" : "text-gray-500 hover:text-gray-700"
      }`}
    >
      {label}
    </button>
  );
}

function FilterPillGroup<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: FilterOption<T>[];
  onChange: (value: T) => void;
}) {
  return (
    <div class="flex items-center gap-2 flex-wrap">
      <span class="w-16 text-xs text-gray-500">{label}</span>
      <div class="flex items-center gap-1.5 flex-wrap">
        {options.map((option) => {
          const active = option.value === value;
          return (
            <button
              key={option.value || "all"}
              type="button"
              onClick={() => onChange(option.value)}
              class={`px-2.5 py-1 text-xs font-medium rounded-full border transition-colors ${
                active
                  ? "bg-indigo-50 text-indigo-700 border-indigo-200 shadow-sm"
                  : "bg-white text-gray-600 border-gray-200 hover:border-gray-300 hover:text-gray-900"
              }`}
            >
              {option.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function ModelParamsRangeSlider({
  min,
  max,
  onMinChange,
  onMaxChange,
  onRangeChange,
}: {
  min: number;
  max: number;
  onMinChange: (value: number) => void;
  onMaxChange: (value: number) => void;
  onRangeChange: (min: number, max: number) => void;
}) {
  const sliderMin = clampSliderModelParams(min);
  const sliderMax = clampSliderModelParams(max);
  const minPercent = ((sliderMin - modelParamsSliderMinLimit) / (modelParamsSliderMaxLimit - modelParamsSliderMinLimit)) * 100;
  const maxPercent = ((sliderMax - modelParamsSliderMinLimit) / (modelParamsSliderMaxLimit - modelParamsSliderMinLimit)) * 100;
  const rangeLabel = modelParamsRangeLabel(min, max);

  return (
    <div>
      <style>{`
        .marketplace-range-input {
          pointer-events: none;
        }
        .marketplace-range-input::-webkit-slider-runnable-track {
          background: transparent;
        }
        .marketplace-range-input::-webkit-slider-thumb {
          pointer-events: auto;
        }
        .marketplace-range-input::-moz-range-track {
          background: transparent;
        }
        .marketplace-range-input::-moz-range-thumb {
          pointer-events: auto;
        }
      `}</style>
      <div class="flex items-center justify-between gap-3 mb-3">
        <div>
          <div class="text-xs font-medium text-gray-500">{t("mp.modelSizeRange")}</div>
          <div class="text-sm font-semibold text-gray-900 mt-0.5">{rangeLabel}</div>
        </div>
        <div class="text-xs text-gray-400">{t("mp.modelSizeSliderHint", modelParamsSliderMinLimit, modelParamsSliderMaxLimit)}</div>
      </div>
      <div class="relative h-8">
        <div class="absolute left-0 right-0 top-1/2 h-1 -translate-y-1/2 rounded-full bg-gray-200" />
        <div
          class="absolute top-1/2 h-1 -translate-y-1/2 rounded-full bg-indigo-500"
          style={{ left: `${minPercent}%`, right: `${100 - maxPercent}%` }}
        />
        <input
          type="range"
          min={modelParamsSliderMinLimit}
          max={modelParamsSliderMaxLimit}
          step={1}
          value={sliderMin}
          onInput={(e) => onMinChange(Number((e.target as HTMLInputElement).value))}
          aria-label={t("mp.minimum")}
          class="marketplace-range-input absolute inset-x-0 top-1/2 z-20 w-full -translate-y-1/2 appearance-none bg-transparent accent-indigo-600"
        />
        <input
          type="range"
          min={modelParamsSliderMinLimit}
          max={modelParamsSliderMaxLimit}
          step={1}
          value={sliderMax}
          onInput={(e) => onMaxChange(Number((e.target as HTMLInputElement).value))}
          aria-label={t("mp.maximum")}
          class="marketplace-range-input absolute inset-x-0 top-1/2 z-30 w-full -translate-y-1/2 appearance-none bg-transparent accent-indigo-600"
        />
      </div>
      <div class="flex items-center justify-between gap-3 mt-2">
        <label class="flex items-center gap-2 text-xs text-gray-500">
          {t("mp.minimum")}
          <input
            type="number"
            min={modelParamsSliderMinLimit}
            max={modelParamsSliderMaxLimit}
            value={sliderMin}
            onInput={(e) => onMinChange(clampSliderModelParams(Number((e.target as HTMLInputElement).value)))}
            class="w-20 px-2 py-1 border border-gray-200 rounded-lg text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </label>
        <label class="flex items-center gap-2 text-xs text-gray-500">
          {t("mp.maximum")}
          <input
            type="number"
            min={modelParamsSliderMinLimit}
            max={modelParamsSliderMaxLimit}
            value={sliderMax}
            onInput={(e) => onMaxChange(clampSliderModelParams(Number((e.target as HTMLInputElement).value)))}
            class="w-20 px-2 py-1 border border-gray-200 rounded-lg text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </label>
      </div>
      <div class="mt-3">
        <div class="text-xs font-medium text-gray-500 mb-2">{t("mp.quickSizes")}</div>
        <div class="flex flex-wrap gap-1.5">
          {modelParamsQuickRanges.map((range) => {
            const active = min === range.min && max === range.max;
            return (
              <button
                key={range.key}
                type="button"
                onClick={() => onRangeChange(range.min, range.max)}
                class={`px-2.5 py-1 text-xs font-medium rounded-full border transition-colors ${
                  active
                    ? "bg-indigo-50 text-indigo-700 border-indigo-200 shadow-sm"
                    : "bg-white text-gray-600 border-gray-200 hover:border-gray-300 hover:text-gray-900"
                }`}
              >
                {range.label === "mp.modelSizeB" ? t(range.label, range.min) : t(range.label)}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function clampSliderModelParams(value: number): number {
  if (!Number.isFinite(value)) {
    return modelParamsSliderMinLimit;
  }
  return Math.max(modelParamsSliderMinLimit, Math.min(modelParamsSliderMaxLimit, Math.round(value)));
}

function modelParamsRangeLabel(min: number, max: number): string {
  if (min === modelParamsMinLimit && max === modelParamsMaxLimit) {
    return t("mp.modelSizeAny");
  }
  if (min === modelParamsMinLimit && max === 0.99999) {
    return t("mp.modelSizeUnder1B");
  }
  if (min === modelParamsSliderMaxLimit && max === modelParamsMaxLimit) {
    return t("mp.modelSizeOver100B");
  }
  if (min === max && Number.isInteger(min)) {
    return t("mp.modelSizeB", min);
  }
  return t("mp.modelSizeRangeValue", min, max);
}

function formatModelParamsMax(value: number): string {
  if (value >= modelParamsMaxLimit) {
    return String(modelParamsMaxLimit);
  }
  return Number.isInteger(value) ? `${value}.99999` : String(value);
}

function ModelCard({
  model,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  model: MarketplaceModel;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (path: string) => void;
  onOpenDetail: (path: string) => void;
}) {
  void locale.value;
  const tags = model.tags?.filter((t) => t.category === "task" || t.category === "license").slice(0, 3) || [];

  return (
    <div class="flex items-center justify-between py-4">
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          <button
            onClick={() => onOpenDetail(model.path)}
            class="font-medium text-gray-900 hover:text-indigo-600 transition-colors text-left break-all"
            title={t("mp.viewDetails")}
          >
            {model.path}
          </button>
          <ModelFormatBadges model={model} />
          {isLocal && (
            <span class="px-1.5 py-0.5 text-xs bg-indigo-50 text-indigo-600 rounded font-medium">{t("mp.downloaded")}</span>
          )}
        </div>
        {model.description && (
          <p class="text-sm text-gray-500 mt-1 line-clamp-1">{model.description}</p>
        )}
        <div class="flex items-center gap-3 mt-2 text-xs text-gray-400">
          {tags.map((tg) => (
            <span key={tg.name} class="bg-gray-100 text-gray-600 px-2 py-0.5 rounded">
              {tg.show_name || tg.name}
            </span>
          ))}
          <span>&middot;</span>
          <span>{new Date(model.updated_at).toLocaleDateString()}</span>
          <span>&middot;</span>
          <span class="flex items-center gap-1">
            <DownloadIcon /> {model.downloads}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {model.likes}
          </span>
        </div>
      </div>
      <div class="ml-4 flex-shrink-0 w-28 flex items-center justify-end">
        {(isLocal || pulling?.status === "success") && !pulling?.status?.startsWith("downloading") ? (
          <span class="inline-flex items-center gap-1.5 px-4 py-1.5 text-sm text-indigo-600 font-medium">
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
            {t("mp.downloaded")}
          </span>
        ) : pulling ? (
          <div>
            {pulling.status === "success" ? (
              <div class="flex items-center justify-end gap-1.5 text-indigo-600">
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
                <span class="text-sm font-medium">{t("mp.done")}</span>
              </div>
            ) : pulling.status.startsWith("error") ? (
              <div class="flex items-center justify-end gap-1.5 text-red-500" title={pulling.status}>
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
                <span class="text-sm font-medium">{t("mp.failed")}</span>
              </div>
            ) : (
              <div>
                <div class="flex items-center justify-between mb-1">
                  <span class="text-xs text-indigo-600 font-medium">
                    {pulling.percent > 0 ? `${pulling.percent}%` : t("mp.pulling")}
                  </span>
                </div>
                <div class="w-full h-1.5 bg-gray-200 rounded-full overflow-hidden">
                  <div
                    class="h-full bg-indigo-500 rounded-full transition-all duration-300"
                    style={{ width: `${Math.max(pulling.percent, 3)}%` }}
                  />
                </div>
              </div>
            )}
          </div>
        ) : (
          <button
            onClick={() => onDownload(model.path)}
            class="flex items-center justify-center gap-1.5 w-full px-4 py-1.5 text-sm border border-gray-200 rounded-lg hover:bg-gray-50 text-gray-700 transition-colors"
          >
            <DownloadIcon /> {t("mp.download")}
          </button>
        )}
      </div>
    </div>
  );
}

function ModelGridCard({
  model,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  model: MarketplaceModel;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (path: string) => void;
  onOpenDetail: (path: string) => void;
}) {
  void locale.value;
  const tags = model.tags?.filter((t) => t.category === "task" || t.category === "license").slice(0, 2) || [];

  return (
    <div class="border border-gray-200 rounded-xl bg-white p-5 flex flex-col justify-between">
      <div>
        <div class="flex items-center gap-2 mb-2">
          <div class="w-6 h-6 rounded-md bg-indigo-100 flex items-center justify-center flex-shrink-0">
            <svg class="w-3.5 h-3.5 text-indigo-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
            </svg>
          </div>
          <button
            onClick={() => onOpenDetail(model.path)}
            class="min-w-0 flex-1 font-medium text-gray-900 text-sm truncate text-left hover:text-indigo-600 transition-colors"
            title={t("mp.viewDetails")}
          >
            {model.path}
          </button>
          <ModelFormatBadges model={model} />
        </div>
        <p class="text-sm text-gray-500 line-clamp-2 mb-3 min-h-[2.5rem]">
          {model.description || ""}
        </p>
        <div class="flex items-center gap-2 flex-wrap text-xs text-gray-400">
          {tags.map((tg) => (
            <span key={tg.name} class="bg-indigo-50 text-indigo-600 px-2 py-0.5 rounded">
              {tg.show_name || tg.name}
            </span>
          ))}
          <span class="flex items-center gap-1">
            <DownloadIcon /> {model.downloads}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {model.likes}
          </span>
        </div>
      </div>
      <div class="flex items-center justify-between mt-4 pt-3 border-t border-gray-100 text-xs text-gray-400">
        <span class="flex items-center gap-1">
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          {t("mp.updatedAt", new Date(model.updated_at).toLocaleDateString())}
        </span>
        <div class="flex-shrink-0">
          {(isLocal || pulling?.status === "success") && !pulling?.status?.startsWith("downloading") ? (
            <span class="inline-flex items-center gap-1 text-indigo-600 font-medium">
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
              </svg>
              {t("mp.downloaded")}
            </span>
          ) : pulling ? (
            pulling.status === "success" ? (
              <span class="text-indigo-600 font-medium">{t("mp.done")}</span>
            ) : pulling.status.startsWith("error") ? (
              <span class="text-red-500 font-medium">{t("mp.failed")}</span>
            ) : (
              <span class="text-indigo-600 font-medium">
                {pulling.percent > 0 ? `${pulling.percent}%` : t("mp.pulling")}
              </span>
            )
          ) : (
            <button
              onClick={() => onDownload(model.path)}
              class="flex items-center gap-1 text-indigo-600 hover:text-indigo-700 font-medium"
            >
              <DownloadIcon /> {t("mp.download")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function DatasetGridCard({
  dataset,
  pulling,
  isLocal,
  onDownload,
}: {
  dataset: MarketplaceDataset;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (path: string) => void;
}) {
  void locale.value;
  const tags = dataset.tags?.filter((t) => t.category === "task" || t.category === "license").slice(0, 2) || [];

  return (
    <div class="border border-gray-200 rounded-xl bg-white p-5 flex flex-col justify-between">
      <div>
        <div class="flex items-center gap-2 mb-2">
          <div class="w-6 h-6 rounded-md bg-purple-100 flex items-center justify-center flex-shrink-0">
            <svg class="w-3.5 h-3.5 text-purple-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4m0 5c0 2.21-3.582 4-8 4s-8-1.79-8-4" />
            </svg>
          </div>
          <span class="font-medium text-gray-900 text-sm truncate">{dataset.path}</span>
        </div>
        <p class="text-sm text-gray-500 line-clamp-2 mb-3 min-h-[2.5rem]">
          {dataset.description || ""}
        </p>
        <div class="flex items-center gap-2 flex-wrap text-xs text-gray-400">
          {tags.map((tg) => (
            <span key={tg.name} class="bg-purple-50 text-purple-600 px-2 py-0.5 rounded">
              {tg.show_name || tg.name}
            </span>
          ))}
          <span class="flex items-center gap-1">
            <DownloadIcon /> {dataset.downloads}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {dataset.likes}
          </span>
        </div>
      </div>
      <div class="flex items-center justify-between mt-4 pt-3 border-t border-gray-100 text-xs text-gray-400">
        <span class="flex items-center gap-1">
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          {t("mp.updatedAt", new Date(dataset.updated_at).toLocaleDateString())}
        </span>
        <div class="flex-shrink-0">
          {(isLocal || pulling?.status === "success") && !pulling?.status?.startsWith("downloading") ? (
            <span class="inline-flex items-center gap-1 text-purple-600 font-medium">
              <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
              </svg>
              {t("mp.downloaded")}
            </span>
          ) : pulling ? (
            pulling.status === "success" ? (
              <span class="text-purple-600 font-medium">{t("mp.done")}</span>
            ) : pulling.status.startsWith("error") ? (
              <span class="text-red-500 font-medium">{t("mp.failed")}</span>
            ) : (
              <span class="text-purple-600 font-medium">
                {pulling.percent > 0 ? `${pulling.percent}%` : t("mp.pulling")}
              </span>
            )
          ) : (
            <button
              onClick={() => onDownload(dataset.path)}
              class="flex items-center gap-1 text-indigo-600 hover:text-indigo-700 font-medium"
            >
              <DownloadIcon /> {t("mp.download")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function DatasetCard({
  dataset,
  pulling,
  isLocal,
  onDownload,
}: {
  dataset: MarketplaceDataset;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (path: string) => void;
}) {
  void locale.value;
  const tags = dataset.tags?.filter((t) => t.category === "task" || t.category === "license").slice(0, 3) || [];

  return (
    <div class="flex items-center justify-between py-4">
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2">
          <span class="font-medium text-gray-900">{dataset.path}</span>
          {isLocal && (
            <span class="px-1.5 py-0.5 text-xs bg-purple-50 text-purple-600 rounded font-medium">{t("mp.downloaded")}</span>
          )}
        </div>
        {dataset.description && (
          <p class="text-sm text-gray-500 mt-1 line-clamp-1">{dataset.description}</p>
        )}
        <div class="flex items-center gap-3 mt-2 text-xs text-gray-400">
          {tags.map((tg) => (
            <span key={tg.name} class="bg-gray-100 text-gray-600 px-2 py-0.5 rounded">
              {tg.show_name || tg.name}
            </span>
          ))}
          <span>&middot;</span>
          <span>{new Date(dataset.updated_at).toLocaleDateString()}</span>
          <span>&middot;</span>
          <span class="flex items-center gap-1">
            <DownloadIcon /> {dataset.downloads}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {dataset.likes}
          </span>
        </div>
      </div>
      <div class="ml-4 flex-shrink-0 w-28 flex items-center justify-end">
        {(isLocal || pulling?.status === "success") && !pulling?.status?.startsWith("downloading") ? (
          <span class="inline-flex items-center gap-1.5 px-4 py-1.5 text-sm text-purple-600 font-medium">
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
            {t("mp.downloaded")}
          </span>
        ) : pulling ? (
          <div>
            {pulling.status === "success" ? (
              <div class="flex items-center justify-end gap-1.5 text-purple-600">
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
                <span class="text-sm font-medium">{t("mp.done")}</span>
              </div>
            ) : pulling.status.startsWith("error") ? (
              <div class="flex items-center justify-end gap-1.5 text-red-500" title={pulling.status}>
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
                <span class="text-sm font-medium">{t("mp.failed")}</span>
              </div>
            ) : (
              <div>
                <div class="flex items-center justify-between mb-1">
                  <span class="text-xs text-purple-600 font-medium">
                    {pulling.percent > 0 ? `${pulling.percent}%` : t("mp.pulling")}
                  </span>
                </div>
                <div class="w-full h-1.5 bg-gray-200 rounded-full overflow-hidden">
                  <div
                    class="h-full bg-purple-500 rounded-full transition-all duration-300"
                    style={{ width: `${Math.max(pulling.percent, 3)}%` }}
                  />
                </div>
              </div>
            )}
          </div>
        ) : (
          <button
            onClick={() => onDownload(dataset.path)}
            class="flex items-center justify-center gap-1.5 w-full px-4 py-1.5 text-sm border border-gray-200 rounded-lg hover:bg-gray-50 text-gray-700 transition-colors"
          >
            <DownloadIcon /> {t("mp.download")}
          </button>
        )}
      </div>
    </div>
  );
}

function DownloadIcon() {
  return (
    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
    </svg>
  );
}

function StarIcon() {
  return (
    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.197-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.784-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
    </svg>
  );
}

function ModelFormatBadges({ model }: { model: MarketplaceModel }) {
  const formatTags = getMarketplaceModelFormats(model);
  if (formatTags.length === 0) {
    return null;
  }

  return (
    <>
      {formatTags.map((formatTag) => {
        const formatName = formatTag.name.trim().toLowerCase();
        const label = formatTag.show_name?.trim()
          || (formatName === "safetensors" ? "SafeTensors" : formatName === "gguf" ? "GGUF" : formatTag.name);
        const tone = formatName === "gguf"
          ? "bg-blue-50 text-blue-700"
          : formatName === "safetensors"
            ? "bg-emerald-50 text-emerald-700"
            : "bg-gray-100 text-gray-700";

        return (
          <span key={`format:${formatTag.name}`} class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}>
            {label}
          </span>
        );
      })}
    </>
  );
}
