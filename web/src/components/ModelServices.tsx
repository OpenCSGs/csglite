import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { createPortal } from "preact/compat";
import { t } from "../i18n";
import { createProviderPool, deleteProviderPool, getProviderManageTags, getProviderPools, getProviderSelectedTags, updateProviderPool } from "../api/client";
import type { ModelInfo, ProviderPool, ProviderPoolMember, ThirdPartyProvider } from "../api/client";
import { ApiInfoDialog } from "./ApiInfoDialog";
import { useRuntimeAPIOrigin } from "../utils/runtimeAPIOrigin";
import { serviceConfigured, candidateCategory } from "../utils/modelServices";
import { InferenceTestDialog } from "./InferenceTestDialog";

const inputClass = "w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500";
const buttonClass = "rounded-lg border border-gray-200 bg-white px-3 py-2 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50";
const primaryClass = "rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50";
const policies = ["fixed", "failover", "weighted", "priority_weight"];
const blankService = (): ProviderPool => ({ id: "", name: "", model: "", enabled: true, policy: "failover", category: "chat", members: [] });

export function ModelServices({ providers, catalogRequest, onChanged }: {
  providers: (ThirdPartyProvider & { builtIn?: boolean })[];
  catalogRequest: { source: string; serial: number };
  onChanged: () => Promise<void>;
}) {
  const origin = useRuntimeAPIOrigin();
  const [services, setServices] = useState<ProviderPool[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [draft, setDraft] = useState<ProviderPool | null>(null);
  const [source, setSource] = useState("local");
  const [catalog, setCatalog] = useState<ModelInfo[]>([]);
  const [nodeNames, setNodeNames] = useState<Record<string, string>>({});
  const [catalogSearch, setCatalogSearch] = useState("");
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [catalogVersion, setCatalogVersion] = useState(0);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [apiService, setAPIService] = useState<ProviderPool | null>(null);
  const [deleting, setDeleting] = useState<ProviderPool | null>(null);
  const [testing, setTesting] = useState<ProviderPool | null>(null);
  const editor = useRef<HTMLDialogElement>(null);
  const deleteDialog = useRef<HTMLDialogElement>(null);
  const refresh = async () => { setServices(await getProviderPools()); };
  useEffect(() => { void refresh().catch(e => setError(e.message)).finally(() => setLoading(false)); }, []);
  useEffect(() => {
    if (catalogRequest.serial) { setDraft(blankService()); setSource(catalogRequest.source); setConfirmed(false); setFormError(""); }
  }, [catalogRequest.serial]);
  useEffect(() => { if (draft) editor.current?.showModal(); else editor.current?.close(); }, [!!draft]);
  useEffect(() => { if (deleting) deleteDialog.current?.showModal(); else deleteDialog.current?.close(); }, [!!deleting]);
  useEffect(() => {
    if (!draft) return;
    let active = true;
    setCatalog([]); setCatalogError(""); setCatalogLoading(true);
    const request = source === "local" || source === "cluster" ? getProviderSelectedTags("local") : getProviderManageTags(source === "cloud" ? "csghub" : source.slice(9));
    void request.then(models => {
      if (!active) return;
      setNodeNames(current => ({ ...current, ...Object.fromEntries(models.flatMap(m => (m.nodes || []).map(n => [`node:${n.uuid}`, n.name]))) }));
      setCatalog(source === "cluster" ? models.filter(m => m.nodes?.length || m.source === "cluster").flatMap(m => [
        { ...m, source: "cluster" },
        ...(m.nodes || []).filter(n => n.online).map(n => ({ ...m, source: `node:${n.uuid}`, display_name: `${m.display_name || m.model} · ${n.name}` })),
      ]) : models.filter(m => source !== "local" || !m.source || m.source === "local"));
    }).catch(e => { if (active) setCatalogError(e.message); }).finally(() => { if (active) setCatalogLoading(false); });
    return () => { active = false; };
  }, [!!draft, source, catalogVersion]);
  const sourceName = (value: string) => value === "local" ? t("services.local") : value === "cluster" ? t("services.cluster") : nodeNames[value] || providers.find(p => (p.builtIn ? "cloud" : `provider:${p.id}`) === value)?.name || value;
  const edit = (pool = blankService()) => { setDraft({ ...pool, members: pool.members.map(m => ({ ...m })) }); setFormError(""); setConfirmed(pool.members.length > 1); setCatalogSearch(""); };
  const patch = (value: Partial<ProviderPool>) => setDraft(current => current && ({ ...current, ...value }));
  const patchMember = (index: number, value: Partial<ProviderPoolMember>) => { if (draft) patch({ members: draft.members.map((m, i) => i === index ? { ...m, ...value } : m) }); };
  const add = (model: ModelInfo) => {
    if (!draft) return;
    const memberSource = source === "cluster" ? model.source || "cluster" : source;
    if (draft.members.some(m => m.source === memberSource && m.model === model.model)) return;
    const alias = model.model;
    patch({ name: draft.name || model.display_name || model.model, model: draft.model || alias,
      category: draft.members.length ? draft.category : candidateCategory(model),
      members: [...draft.members, { id: crypto.randomUUID(), source: memberSource, model: model.model, priority: draft.members.length, weight: 100 }] });
    setConfirmed(false);
  };
  const changed = async () => { await refresh(); await onChanged(); };
  const save = async () => {
    if (!draft) return;
    setSaving(true); setFormError("");
    try {
      if (!draft.name.trim() || !draft.model.trim() || !draft.members.length) throw new Error(t("services.required"));
      if (draft.policy === "fixed" && draft.members.length !== 1) throw new Error(t("services.fixedHint"));
      if (draft.members.length > 1 && !confirmed) throw new Error(t("services.confirmCompatibility"));
      const payload = { name: draft.name.trim(), model: draft.model.trim(), enabled: draft.enabled, category: draft.category || "chat", policy: draft.policy, members: draft.members };
      if (draft.id) await updateProviderPool(draft.id, payload); else await createProviderPool(payload);
      await changed(); setDraft(null);
    } catch (e: any) { setFormError(e.message); } finally { setSaving(false); }
  };
  const toggle = async (pool: ProviderPool) => {
    setError(""); setSaving(true);
    try { await updateProviderPool(pool.id, { enabled: !pool.enabled }); await changed(); } catch (e: any) { setError(e.message); } finally { setSaving(false); }
  };
  const filtered = services.filter(pool => `${pool.name} ${pool.model} ${pool.members.map(m => `${sourceName(m.source)} ${m.model}`).join(" ")}`.toLowerCase().includes(search.toLowerCase()));
  return <section class="rounded-2xl border border-gray-200 bg-white shadow-sm">
    <div class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 p-5">
      <div><h2 class="font-semibold text-gray-900">{t("services.title")}</h2><p class="mt-1 max-w-2xl text-sm leading-6 text-gray-500">{t("services.description")}</p></div>
      <button class={primaryClass} onClick={() => edit()}>{t("services.create")}</button>
    </div>
    <div class="p-5"><input class={inputClass} value={search} onInput={e => setSearch(e.currentTarget.value)} placeholder={t("services.search")} aria-label={t("services.search")} /></div>
    {error && <p role="alert" class="px-5 pb-4 text-sm text-red-600">{error}</p>}
    {loading ? <p class="p-8 text-center text-sm text-gray-500">{t("services.loading")}</p> : !filtered.length ? <div class="p-10 text-center"><p class="font-medium text-gray-700">{t(services.length ? "services.noMatches" : "services.empty")}</p><p class="mt-2 text-sm text-gray-500">{t("services.emptyHint")}</p></div> : <div class="overflow-x-auto px-5 pb-5"><table class="w-full text-left text-sm">
      <thead class="border-b text-xs text-gray-400"><tr>{["modelID", "sources", "policy", "state", "actions"].map(key => <th class="px-3 py-3" key={key}>{t(`services.${key}`)}</th>)}</tr></thead>
      <tbody>{filtered.map(pool => <tr key={pool.id} class="border-b border-gray-100 align-top last:border-0">
        <td class="px-3 py-4"><code class="break-all text-sm font-medium text-gray-900">{pool.model}</code></td>
        <td class="min-w-48 px-3 py-4"><details><summary class="cursor-pointer text-indigo-600">{t("services.sourceCount", pool.members.length)}</summary><div class="mt-3 space-y-2">{pool.members.map(m => <div key={m.id} class="max-w-xs rounded-lg bg-gray-50 p-2 text-xs"><p class="font-medium text-gray-700">{sourceName(m.source)}</p><p class="break-all text-gray-500">{m.model}</p><p class="mt-1 text-gray-400">{t(`services.${pool.source_states?.[m.id] || "configured"}`)}</p></div>)}</div></details></td>
        <td class="whitespace-nowrap px-3 py-4 text-gray-600">{t(`services.${pool.policy || "priority_weight"}`)}</td>
        <td class="px-3 py-4"><span class={`inline-block whitespace-nowrap rounded-full px-2 py-1 text-xs ${serviceConfigured(pool) ? "bg-indigo-50 text-indigo-700" : "bg-gray-100 text-gray-500"}`}>{t(!pool.enabled ? "services.disabled" : serviceConfigured(pool) ? "services.configured" : "services.unavailable")}</span><p class="mt-2 max-w-xs text-xs text-gray-400">{t("services.notTested")}</p></td>
        <td class="px-3 py-4"><div class="flex items-center gap-2 whitespace-nowrap">
          <button class={buttonClass} onClick={() => setAPIService(pool)}>API</button>
          <button class={buttonClass} disabled={saving} onClick={() => void toggle(pool)}>{t(pool.enabled ? "services.disable" : "services.enable")}</button>
          <ModelServiceMoreActions pool={pool} busy={saving} onEdit={() => edit(pool)} onTest={() => setTesting(pool)} onDelete={() => setDeleting(pool)} />
        </div></td>
      </tr>)}</tbody></table></div>}
    <dialog ref={editor} onCancel={e => { e.preventDefault(); if (!saving) setDraft(null); }} class="m-auto max-h-[90vh] w-[min(960px,95vw)] overflow-hidden rounded-2xl p-0 shadow-2xl backdrop:bg-gray-900/40">
      {draft && <div class="flex max-h-[90vh] flex-col"><div class="flex shrink-0 items-start justify-between border-b px-6 py-5"><div><h3 class="font-semibold text-gray-900">{t(draft.id ? "services.edit" : "services.create")}</h3><p class="mt-1 text-xs text-gray-500">{t("services.editorHint")}</p></div><button class={buttonClass} disabled={saving} onClick={() => setDraft(null)}>{t("services.close")}</button></div>
        <fieldset disabled={saving} class="min-h-0 space-y-5 overflow-y-auto p-6">
          <label class="block text-sm text-gray-700">{t("services.publishTo")}<select class={`${inputClass} mt-1`} value={draft.id} onChange={e => edit(services.find(p => p.id === e.currentTarget.value) || blankService())}><option value="">{t("services.newService")}</option>{services.map(pool => <option key={pool.id} value={pool.id}>{pool.name} · {pool.model}</option>)}</select></label>
          <div class="grid gap-4 sm:grid-cols-2"><label class="text-sm text-gray-700">{t("services.name")}<input class={`${inputClass} mt-1`} value={draft.name} onInput={e => patch({ name: e.currentTarget.value })} /></label><label class="text-sm text-gray-700">{t("services.alias")}<input class={`${inputClass} mt-1 font-mono`} value={draft.model} onInput={e => patch({ model: e.currentTarget.value })} placeholder="qwen2.5" /><span class="mt-1 block text-xs text-gray-400">{t("services.aliasHint")}</span></label></div>
          <div class="grid gap-4 sm:grid-cols-2"><label class="text-sm text-gray-700">{t("services.policy")}<select class={`${inputClass} mt-1`} value={draft.policy} onChange={e => patch({ policy: e.currentTarget.value })}>{policies.map(policy => <option key={policy} value={policy}>{t(`services.${policy}`)}</option>)}</select><span class="mt-1 block text-xs text-gray-400">{t(`services.${draft.policy}Hint`)}</span></label><label class="text-sm text-gray-700">{t("services.capability")}<select class={`${inputClass} mt-1`} value={draft.category || "chat"} onChange={e => { patch({ category: e.currentTarget.value }); setConfirmed(false); }}><option value="chat">{t("services.chat")}</option><option value="embedding">{t("services.embedding")}</option></select></label></div>
          <div class="rounded-xl border border-gray-200 p-4"><div class="mb-3 flex items-center justify-between"><h4 class="text-sm font-medium">{t("services.sources")}</h4><span class="text-xs text-gray-400">{draft.members.length}</span></div>
            {!draft.members.length && <p class="text-sm text-gray-400">{t("services.selectSources")}</p>}
            <div class="space-y-3">{draft.members.map((member, index) => <div key={member.id} class="rounded-lg bg-gray-50 p-3"><div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="text-xs font-medium text-indigo-600">{sourceName(member.source)}</p><p class="break-all text-sm text-gray-700">{member.model}</p></div><div class="flex shrink-0 gap-3"><label class="flex items-center gap-1 text-xs"><input type="checkbox" checked={!member.disabled} onChange={e => patchMember(index, { disabled: !e.currentTarget.checked })} />{t("services.enabled")}</label><button class="text-xs text-red-600" onClick={() => patch({ members: draft.members.filter(m => m.id !== member.id) })}>{t("services.remove")}</button></div></div>
              <details class="mt-2"><summary class="cursor-pointer text-xs text-gray-500">{t("services.limits")}</summary><div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-5">{(["priority", "weight", "requests_per_minute", "tokens_per_minute", "max_concurrent"] as const).map(field => <label class="text-xs text-gray-500" key={field}>{t(`services.${field}`)}<input type="number" min={field === "weight" ? 1 : 0} class={`${inputClass} mt-1`} value={member[field] ?? (field === "weight" ? 100 : 0)} onInput={e => patchMember(index, { [field]: Math.max(field === "weight" ? 1 : 0, Number(e.currentTarget.value) || 0) })} /></label>)}</div><p class="mt-2 text-xs text-gray-400">{t("services.limitsHint")}</p></details>
            </div>)}</div>
            {draft.members.length > 1 && <label class="mt-4 flex gap-2 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-900"><input class="mt-1 shrink-0" type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.currentTarget.checked)} />{t("services.confirmCompatibility")}</label>}
          </div>
          <div class="rounded-xl border border-indigo-100 bg-indigo-50/30 p-4"><h4 class="text-sm font-medium text-gray-800">{t("services.candidates")}</h4><p class="mb-3 mt-1 text-xs text-gray-500">{t("services.candidatesHint")}</p><div class="flex flex-wrap gap-2"><select aria-label={t("services.source")} class={`${inputClass} sm:w-48`} value={source} onChange={e => setSource(e.currentTarget.value)}><option value="local">{t("services.local")}</option><option value="cluster">{t("services.cluster")}</option>{providers.map(p => <option key={p.id} value={p.builtIn ? "cloud" : `provider:${p.id}`}>{p.name}{!p.enabled ? ` · ${t("services.disabled")}` : ""}</option>)}</select><input class={`${inputClass} min-w-32 flex-1`} aria-label={t("services.searchCandidates")} placeholder={t("services.searchCandidates")} value={catalogSearch} onInput={e => setCatalogSearch(e.currentTarget.value)} /><button class={buttonClass} disabled={catalogLoading} onClick={() => setCatalogVersion(v => v + 1)}>{t("services.refresh")}</button></div>
            {catalogError && <p class="mt-3 text-sm text-red-600">{catalogError}</p>}
            <div class="mt-3 max-h-60 overflow-y-auto">{catalogLoading ? <p class="py-5 text-center text-xs text-gray-400">{t("services.loading")}</p> : !catalog.length ? <p class="py-5 text-center text-xs text-gray-400">{t("services.noCandidates")}</p> : catalog.filter(m => `${m.model} ${m.display_name || ""}`.toLowerCase().includes(catalogSearch.toLowerCase())).map(model => {
              const candidateSource = source === "cluster" ? model.source || "cluster" : source;
              const added = draft.members.some(m => m.source === candidateSource && m.model === model.model);
              const bound = services.filter(p => p.members.some(m => m.source === candidateSource && m.model === model.model));
              const incompatible = candidateCategory(model) === "unsupported" || (draft.members.length > 0 && candidateCategory(model) !== (draft.category || "chat"));
              return <div key={`${candidateSource}:${model.model}`} class="flex items-center justify-between gap-3 border-b border-indigo-100 py-3 last:border-0"><div class="min-w-0"><p class="break-all text-sm text-gray-700">{model.display_name || model.model}</p><p class="break-all font-mono text-xs text-gray-400">{model.model}</p>{bound.length > 0 && <p class="mt-1 text-xs text-indigo-500">{t("services.publishedIn", bound.map(p => p.name).join(", "))}</p>}</div><button class={buttonClass} disabled={added || incompatible} onClick={() => add(model)}>{t(added ? "services.added" : incompatible ? "services.incompatible" : "services.add")}</button></div>;
            })}</div>
          </div>
          {formError && <p role="alert" class="text-sm text-red-600">{formError}</p>}
        </fieldset><div class="flex shrink-0 items-center justify-between border-t px-6 py-4"><label class="flex items-center gap-2 text-sm text-gray-600"><input type="checkbox" checked={draft.enabled} disabled={saving} onChange={e => patch({ enabled: e.currentTarget.checked })} />{t("services.enabled")}</label><button class={primaryClass} disabled={saving} onClick={() => void save()}>{t(saving ? "services.saving" : "services.save")}</button></div>
      </div>}
    </dialog>
    <dialog ref={deleteDialog} onCancel={e => { e.preventDefault(); if (!saving) setDeleting(null); }} class="m-auto w-[min(440px,90vw)] rounded-2xl p-6 shadow-2xl backdrop:bg-gray-900/40"><h3 class="font-semibold text-gray-900">{t("services.deleteTitle", deleting?.name || "")}</h3><p class="mt-3 text-sm leading-6 text-gray-500">{t("services.deleteHint")}</p><div class="mt-6 flex justify-end gap-3"><button class={buttonClass} disabled={saving} onClick={() => setDeleting(null)}>{t("services.cancel")}</button><button class="rounded-lg bg-red-600 px-4 py-2 text-sm text-white disabled:opacity-50" disabled={saving} onClick={async () => { if (!deleting) return; setSaving(true); try { await deleteProviderPool(deleting.id); await changed(); setDeleting(null); } catch (e: any) { setError(e.message); setDeleting(null); } finally { setSaving(false); } }}>{t("services.delete")}</button></div></dialog>
    {testing && <InferenceTestDialog service={testing} onClose={() => setTesting(null)} />}
    {apiService && <ApiInfoDialog baseUrl={`${origin.replace(/\/+$/, "")}/ai-gateway`} model={apiService.model} pipelineTag={apiService.category === "embedding" ? "feature-extraction" : "text-generation"} isEmbedding={apiService.category === "embedding"} onClose={() => setAPIService(null)} />}
  </section>;
}

function ModelServiceMoreActions({ pool, busy, onEdit, onTest, onDelete }: {
  pool: ProviderPool; busy: boolean; onEdit: () => void; onTest: () => void; onDelete: () => void;
}) {
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const menuID = `model-actions-${pool.id}`;
  useLayoutEffect(() => {
    if (!position) return;
    menu.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus({ preventScroll: true });
    const outside = (event: PointerEvent) => {
      if (!menu.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) setPosition(null);
    };
    const dismiss = () => setPosition(null);
    document.addEventListener("pointerdown", outside);
    window.addEventListener("resize", dismiss);
    window.addEventListener("scroll", dismiss, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      window.removeEventListener("resize", dismiss);
      window.removeEventListener("scroll", dismiss, true);
    };
  }, [!!position]);
  const act = (action: () => void) => { setPosition(null); action(); };
  const menuButton = "block w-full rounded-lg px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-50 focus:bg-indigo-50 focus:outline-none disabled:opacity-40";
  return <>
    <button ref={trigger} class={buttonClass} aria-haspopup="menu" aria-expanded={!!position} aria-controls={position ? menuID : undefined} onClick={() => {
      if (position) { setPosition(null); return; }
      const rect = trigger.current!.getBoundingClientRect();
      setPosition({ top: rect.bottom + 150 < window.innerHeight ? rect.bottom + 6 : Math.max(8, rect.top - 140), left: Math.max(8, Math.min(rect.right - 160, window.innerWidth - 168)) });
    }}>{t("services.more")}<span aria-hidden="true" class="ml-1 text-gray-400">⌄</span></button>
    {position && createPortal(<div ref={menu} id={menuID} role="menu" aria-label={t("services.more")} style={position} class="fixed z-50 w-40 rounded-xl border border-gray-200 bg-white p-1.5 shadow-lg" onKeyDown={event => {
      if (event.key === "Escape") { event.preventDefault(); setPosition(null); trigger.current?.focus(); }
      if (event.key === "Tab") setPosition(null);
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        const buttons = Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') || []);
        const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
        const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (current + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
        buttons[next]?.focus();
      }
    }}>
      <button role="menuitem" class={menuButton} disabled={busy} onClick={() => act(onEdit)}>{t("services.edit")}</button>
      <button role="menuitem" class={menuButton} disabled={!serviceConfigured(pool)} onClick={() => act(onTest)}>{t("services.test")}</button>
      <div class="my-1 border-t border-gray-100" />
      <button role="menuitem" class={`${menuButton} text-red-600`} disabled={busy} onClick={() => act(onDelete)}>{t("services.delete")}</button>
    </div>, document.body)}
  </>;
}
