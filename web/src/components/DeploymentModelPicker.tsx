import { useEffect, useRef, useState } from "preact/hooks";
import { getMarketplaceModels } from "../api/client";
import type { ArtifactSource, MarketplaceModel } from "../api/client";
import { deploymentModelKind } from "../deploymentModels";
import { t } from "../i18n";

export function DeploymentModelPicker({ value, source, disabled, onInput, onSelect }: {
  value: string; source: ArtifactSource; disabled: boolean;
  onInput: (value: string) => void; onSelect: (model: MarketplaceModel) => void;
}) {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [models, setModels] = useState<MarketplaceModel[]>([]);
  const [active, setActive] = useState(-1);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    let current = true;
    setModels([]); setActive(-1); setError("");
    const query = value.trim();
    if (!open || query.length < 2) { setLoading(false); return; }
    setLoading(true);
    const timer = setTimeout(() => {
      void getMarketplaceModels({ search: query, artifactSource: source, per: 8 }).then(result => {
        if (current) setModels(result.data || []);
      }).catch(e => { if (current) setError((e as Error).message); })
        .finally(() => { if (current) setLoading(false); });
    }, 300);
    return () => { current = false; clearTimeout(timer); };
  }, [value, source, open]);
  const choose = (model: MarketplaceModel) => { onSelect(model); input.current?.focus(); setOpen(false); setActive(-1); };
  return <div class="relative col-span-2" onFocusOut={event => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOpen(false);
  }}>
    <label class="block space-y-1 text-sm" for="deployment-repository">{t("deployments.repository")}</label>
    <input ref={input} id="deployment-repository" role="combobox" aria-autocomplete="list"
      aria-expanded={open} aria-controls="deployment-model-options"
      aria-activedescendant={active >= 0 ? `deployment-model-option-${active}` : undefined}
      class="mt-1 w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm focus:ring-2 focus:ring-indigo-500"
      required autoComplete="off" disabled={disabled} value={value} placeholder={t("deployments.searchPlaceholder")}
      onFocus={() => setOpen(true)} onInput={event => { onInput(event.currentTarget.value); setOpen(true); }}
      onKeyDown={event => {
        if (event.key === "Escape" && open) { event.preventDefault(); event.stopPropagation(); setOpen(false); return; }
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault(); setOpen(true);
          setActive(i => models.length ? (i + (event.key === "ArrowDown" ? 1 : models.length - 1) + models.length) % models.length : -1);
        }
        if (event.key === "Enter" && open) {
          event.preventDefault(); if (models[active]) choose(models[active]);
        }
      }} />
    {open && <div class="absolute z-10 mt-1 max-h-64 w-full overflow-auto rounded-xl border border-gray-200 bg-white shadow-lg">
      {value.trim().length < 2 ? <p class="p-3 text-xs text-gray-500">{t("deployments.searchHint")}</p>
        : loading ? <p role="status" class="p-3 text-sm text-gray-500">{t("deployments.searching")}</p>
        : error ? <p role="alert" class="p-3 text-xs text-red-600">{t("deployments.searchFailed")} {error}</p>
        : !models.length && <p class="p-3 text-xs text-gray-500">{t("deployments.searchEmpty")}</p>}
      <ul id="deployment-model-options" role="listbox" aria-label={t("deployments.repository")}>
        {models.map((model, index) => <li key={`${source}/${model.path}`} id={`deployment-model-option-${index}`}
          role="option" aria-selected={active === index}
          class={`cursor-pointer px-3 py-2 ${active === index ? "bg-indigo-50" : "hover:bg-gray-50"}`}
          onMouseDown={event => event.preventDefault()} onClick={() => choose(model)}>
          <div class="flex items-center justify-between gap-2"><span class="break-all text-sm font-medium text-gray-900">{model.path}</span>
            <span class="shrink-0 rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600">{t(`deployments.${deploymentModelKind(model)}`)}</span></div>
          {model.description && <p class="mt-1 line-clamp-1 text-xs text-gray-500">{model.description}</p>}
        </li>)}
      </ul>
    </div>}
  </div>;
}
