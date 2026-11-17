import { useId } from "preact/hooks";
import { t } from "../i18n";
import { contextWindowApplies } from "../utils/modelRuntime";
import type { ModelRuntimeKind } from "../utils/modelRuntime";

/** Library and cluster forms share type gating and an uncapped token input. */
export function ContextWindowField({ kind, value, onInput, label, min = 512, required = false, placeholder, hint, inputClass, disabled = false }: {
  kind: ModelRuntimeKind;
  value: string | number;
  onInput: (value: string) => void;
  label?: string;
  min?: number;
  required?: boolean;
  placeholder?: string;
  hint?: string;
  inputClass?: string;
  disabled?: boolean;
}) {
  const id = useId();
  if (!contextWindowApplies(kind)) return null;
  return <div class="space-y-1 text-sm">
    <label for={id} class="block">{label || t("deployments.context")}</label>
    <input id={id} type="number" min={min} step={1} required={required} disabled={disabled} value={value} placeholder={placeholder}
      aria-describedby={hint ? `${id}-hint` : undefined}
      class={inputClass || "w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"}
      onInput={event => onInput(event.currentTarget.value)} />
    {hint && <p id={`${id}-hint`} class="mt-1 text-xs text-gray-400">{hint}</p>}
  </div>;
}
