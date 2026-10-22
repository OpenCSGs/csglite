import { useState } from "preact/hooks";
import { updateLocalAPIKey } from "../api/client";
import type { Budget, BudgetSpend, KeyLimits, LocalAPIKeyInfo } from "../api/client";
import { locale, t } from "../i18n";

const fieldClass =
  "mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 disabled:bg-gray-50";

/** Editable form of KeyLimits: numbers and lists kept as typed text. */
export interface LimitsDraft {
  models: string;
  expiresAt: string;
  rpm: string;
  tpm: string;
  budget: BudgetDraft;
}

export interface BudgetDraft {
  tokens: string;
  cost: string;
  currency: string;
  period: "" | "day" | "month";
}

export function budgetDraft(budget?: Budget | null): BudgetDraft {
  return {
    tokens: budget?.tokens ? String(budget.tokens) : "",
    cost: budget?.cost ? String(budget.cost) : "",
    currency: budget?.currency || "CNY",
    period: budget?.period || "month",
  };
}

export function limitsDraft(limits?: KeyLimits | null): LimitsDraft {
  return {
    models: (limits?.models || []).join("\n"),
    expiresAt: limits?.expires_at ? limits.expires_at.slice(0, 10) : "",
    rpm: limits?.rpm ? String(limits.rpm) : "",
    tpm: limits?.tpm ? String(limits.tpm) : "",
    budget: budgetDraft(limits?.budget),
  };
}

function wholeNumber(value: string): number {
  const n = Math.floor(Number(value.trim()));
  return Number.isFinite(n) && n > 0 ? n : 0;
}

/** The budget a draft describes, or null when it caps nothing. */
export function draftBudget(draft: BudgetDraft): Budget | null {
  const tokens = wholeNumber(draft.tokens);
  const cost = Number(draft.cost.trim());
  const budget: Budget = { period: draft.period };
  if (tokens > 0) budget.tokens = tokens;
  if (Number.isFinite(cost) && cost > 0) {
    budget.cost = cost;
    budget.currency = draft.currency.trim().toUpperCase();
  }
  return budget.tokens || budget.cost ? budget : null;
}

/** The limits a draft describes; `{}` means none. A date expires at the end of that local day. */
export function draftLimits(draft: LimitsDraft): KeyLimits {
  const limits: KeyLimits = {};
  const models = draft.models
    .split(/[\n,]/)
    .map((m) => m.trim())
    .filter(Boolean);
  if (models.length) limits.models = models;
  if (draft.expiresAt) limits.expires_at = new Date(`${draft.expiresAt}T23:59:59`).toISOString();
  const rpm = wholeNumber(draft.rpm);
  const tpm = wholeNumber(draft.tpm);
  if (rpm) limits.rpm = rpm;
  if (tpm) limits.tpm = tpm;
  const budget = draftBudget(draft.budget);
  if (budget) limits.budget = budget;
  return limits;
}

function compact(n: number): string {
  return new Intl.NumberFormat(locale.value === "zh" ? "zh-CN" : "en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

function money(n: number, currency?: string): string {
  try {
    return new Intl.NumberFormat(locale.value === "zh" ? "zh-CN" : "en-US", { style: "currency", currency: currency || "CNY", maximumFractionDigits: 2 }).format(n);
  } catch {
    return `${currency || ""} ${n.toFixed(2)}`;
  }
}

/** "1.2K / 10K tokens · ¥3.20 / ¥50.00 · per month" */
export function budgetText(budget: Budget, spend?: BudgetSpend): string {
  const parts: string[] = [];
  if (budget.tokens) parts.push(t("limits.tokensOf", compact(spend?.tokens ?? 0), compact(budget.tokens)));
  if (budget.cost) parts.push(`${money(spend?.cost ?? 0, budget.currency)} / ${money(budget.cost, budget.currency)}`);
  parts.push(t(`limits.period.${budget.period || "total"}`));
  return parts.join(" · ");
}

/** Largest share of a cap used, 0–1. */
export function budgetUsed(budget: Budget, spend?: BudgetSpend): number {
  if (!spend) return 0;
  let used = 0;
  if (budget.tokens) used = Math.max(used, spend.tokens / budget.tokens);
  if (budget.cost) used = Math.max(used, spend.cost / budget.cost);
  return Math.min(used, 1);
}

/** A budget as a line of text over a thin bar. */
export function BudgetMeter({ budget, spend }: { budget: Budget; spend?: BudgetSpend }) {
  const used = budgetUsed(budget, spend);
  const tone = spend?.exhausted ? "bg-red-500" : spend?.warning ? "bg-amber-500" : "bg-indigo-500";
  return (
    <div class="min-w-[10rem]">
      <p class={`text-xs ${spend?.exhausted ? "text-red-600" : spend?.warning ? "text-amber-700" : "text-gray-500"}`}>{budgetText(budget, spend)}</p>
      <div class="mt-1 h-1 w-full overflow-hidden rounded-full bg-gray-100">
        <div class={`h-full ${tone}`} style={{ width: `${Math.round(used * 100)}%` }} />
      </div>
    </div>
  );
}

const chip = "rounded-full px-2.5 py-1";

/** The limits on a key as chips, for a key list. */
export function KeyLimitChips({ apiKey }: { apiKey: LocalAPIKeyInfo }) {
  const limits = apiKey.limits;
  return (
    <>
      {apiKey.status === "expired" && <span class={`${chip} bg-red-50 text-red-700`}>{t("limits.statusExpired")}</span>}
      {apiKey.status === "exhausted" && <span class={`${chip} bg-red-50 text-red-700`}>{t("limits.statusExhausted")}</span>}
      {apiKey.spend?.warning && apiKey.status === "active" && <span class={`${chip} bg-amber-50 text-amber-700`}>{t("limits.statusWarning")}</span>}
      {limits?.models?.length ? (
        <span class={`${chip} bg-gray-50 text-gray-600`} title={limits.models.join("\n")}>
          {limits.models.length === 1 ? t("limits.oneModel", limits.models[0]) : t("limits.models", limits.models.length)}
        </span>
      ) : null}
      {limits?.expires_at && apiKey.status !== "expired" && (
        <span class={`${chip} bg-gray-50 text-gray-600`}>{t("limits.expires", new Date(limits.expires_at).toLocaleDateString(locale.value === "zh" ? "zh-CN" : "en-US"))}</span>
      )}
      {limits?.rpm ? <span class={`${chip} bg-gray-50 text-gray-600`}>{t("limits.rpmChip", limits.rpm)}</span> : null}
      {limits?.tpm ? <span class={`${chip} bg-gray-50 text-gray-600`}>{t("limits.tpmChip", compact(limits.tpm))}</span> : null}
      {limits?.budget && <span class={`${chip} bg-gray-50 text-gray-600`}>{budgetText(limits.budget, apiKey.spend)}</span>}
    </>
  );
}

/** Token and cost caps with their period. */
export function BudgetFields({ draft, onChange, disabled }: { draft: BudgetDraft; onChange: (draft: BudgetDraft) => void; disabled?: boolean }) {
  return (
    <div class="grid grid-cols-2 gap-3">
      <label class="block text-sm font-medium text-gray-700">
        {t("limits.tokenBudget")}
        <input
          class={fieldClass}
          inputMode="numeric"
          value={draft.tokens}
          disabled={disabled}
          placeholder={t("limits.unlimited")}
          onInput={(e) => onChange({ ...draft, tokens: (e.target as HTMLInputElement).value })}
        />
      </label>
      <label class="block text-sm font-medium text-gray-700">
        {t("limits.period")}
        <select
          class={fieldClass}
          value={draft.period}
          disabled={disabled}
          onChange={(e) => onChange({ ...draft, period: (e.target as HTMLSelectElement).value as BudgetDraft["period"] })}
        >
          <option value="day">{t("limits.period.day")}</option>
          <option value="month">{t("limits.period.month")}</option>
          <option value="">{t("limits.period.total")}</option>
        </select>
      </label>
      <label class="block text-sm font-medium text-gray-700">
        {t("limits.costBudget")}
        <input
          class={fieldClass}
          inputMode="decimal"
          value={draft.cost}
          disabled={disabled}
          placeholder={t("limits.unlimited")}
          onInput={(e) => onChange({ ...draft, cost: (e.target as HTMLInputElement).value })}
        />
      </label>
      <label class="block text-sm font-medium text-gray-700">
        {t("limits.currency")}
        <select
          class={fieldClass}
          value={draft.currency}
          disabled={disabled}
          onChange={(e) => onChange({ ...draft, currency: (e.target as HTMLSelectElement).value })}
        >
          <option value="CNY">CNY</option>
          <option value="USD">USD</option>
        </select>
      </label>
      <p class="col-span-2 text-xs text-gray-500">{t("limits.costHint")}</p>
    </div>
  );
}

/** Every key limit. */
export function LimitsFields({ draft, onChange, disabled }: { draft: LimitsDraft; onChange: (draft: LimitsDraft) => void; disabled?: boolean }) {
  return (
    <div class="space-y-4">
      <label class="block text-sm font-medium text-gray-700">
        {t("limits.allowedModels")}
        <textarea
          class={`${fieldClass} font-mono`}
          rows={3}
          value={draft.models}
          disabled={disabled}
          placeholder={t("limits.allowedModelsPlaceholder")}
          onInput={(e) => onChange({ ...draft, models: (e.target as HTMLTextAreaElement).value })}
        />
        <span class="mt-1 block text-xs font-normal text-gray-500">{t("limits.allowedModelsHint")}</span>
      </label>
      <div class="grid grid-cols-3 gap-3">
        <label class="block text-sm font-medium text-gray-700">
          {t("limits.expiresAt")}
          <input
            type="date"
            class={fieldClass}
            value={draft.expiresAt}
            disabled={disabled}
            onInput={(e) => onChange({ ...draft, expiresAt: (e.target as HTMLInputElement).value })}
          />
        </label>
        <label class="block text-sm font-medium text-gray-700">
          RPM
          <input
            class={fieldClass}
            inputMode="numeric"
            value={draft.rpm}
            disabled={disabled}
            placeholder={t("limits.unlimited")}
            onInput={(e) => onChange({ ...draft, rpm: (e.target as HTMLInputElement).value })}
          />
        </label>
        <label class="block text-sm font-medium text-gray-700">
          TPM
          <input
            class={fieldClass}
            inputMode="numeric"
            value={draft.tpm}
            disabled={disabled}
            placeholder={t("limits.unlimited")}
            onInput={(e) => onChange({ ...draft, tpm: (e.target as HTMLInputElement).value })}
          />
        </label>
      </div>
      <BudgetFields draft={draft.budget} onChange={(budget) => onChange({ ...draft, budget })} disabled={disabled} />
    </div>
  );
}

/** An administrator edits the limits of an existing key. */
export function KeyLimitsDialog({ apiKey, onClose, onSaved }: { apiKey: LocalAPIKeyInfo; onClose: () => void; onSaved: () => void }) {
  const [draft, setDraft] = useState(() => limitsDraft(apiKey.limits));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await updateLocalAPIKey(apiKey.id, { limits: draftLimits(draft) });
      onSaved();
      onClose();
    } catch (err: any) {
      setError(err?.message || t("limits.saveFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4" onClick={() => !busy && onClose()}>
      <div role="dialog" aria-modal="true" class="w-full max-w-lg rounded-2xl bg-white shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div class="border-b border-gray-100 px-6 py-5">
          <h2 class="text-lg font-semibold text-gray-900">{t("limits.editTitle", apiKey.name)}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("limits.editDesc")}</p>
        </div>
        <div class="max-h-[70vh] overflow-y-auto px-6 py-5">
          <LimitsFields draft={draft} onChange={setDraft} disabled={busy} />
          {error && <p class="mt-4 text-sm text-red-600">{error}</p>}
        </div>
        <div class="flex justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button type="button" onClick={onClose} disabled={busy} class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60">
            {t("upgrade.cancel")}
          </button>
          <button type="button" onClick={() => void save()} disabled={busy} class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-700 disabled:opacity-60">
            {busy ? "..." : t("limits.save")}
          </button>
        </div>
      </div>
    </div>
  );
}
