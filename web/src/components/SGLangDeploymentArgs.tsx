import { t } from "../i18n";

const mtpFlags = ["algorithm", "num-steps", "eagle-topk", "num-draft-tokens"] as const;
type MTPFlag = typeof mtpFlags[number];
type MTPValues = Partial<Record<MTPFlag, string>>;

function splitArgs(value: string) {
  const mtp: MTPValues = {};
  const extra = value.replace(/(^|\s)--speculative-(algorithm|num-steps|eagle-topk|num-draft-tokens)(?:=|\s+)((?!--)[^\s]+)(?=\s|$)/g,
    (_match, space: string, flag: MTPFlag, setting: string) => {
      mtp[flag] = setting;
      return space.includes("\n") ? "" : space;
    });
  return { mtp, extra: Object.keys(mtp).length ? extra.trimStart() : extra };
}

function joinArgs(mtp: MTPValues, extra: string) {
  return [...mtpFlags.flatMap(flag => mtp[flag] ? [`--speculative-${flag} ${mtp[flag]}`] : []), extra]
    .filter(Boolean).join("\n");
}

type ArgsProps = { value: string; onInput: (value: string) => void };
const inputClass = "w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm disabled:bg-gray-50";

export function SGLangMTPParams({ value, onInput }: ArgsProps) {
  const { mtp, extra } = splitArgs(value);
  const algorithm = mtp.algorithm || "";
  const enabled = algorithm === "NEXTN" || algorithm === "EAGLE";
  function setAlgorithm(next: string) {
    if (next === "" || next === "none") {
      onInput(joinArgs({ algorithm: next }, extra));
    } else {
      onInput(joinArgs({ "num-steps": "3", "eagle-topk": "1", "num-draft-tokens": "4", ...mtp, algorithm: next }, extra));
    }
  }
  return <div class="col-span-2 grid grid-cols-3 gap-3">
    <label class="col-span-3 space-y-1 text-sm">{t("deployments.mtp")}
      <select class={inputClass} value={algorithm} onChange={event => setAlgorithm(event.currentTarget.value)}>
        <option value="">{t("deployments.enginePlanAuto")}</option>
        <option value="none">{t("deployments.mtpOff")}</option>
        <option value="NEXTN">NEXTN</option>
        <option value="EAGLE">EAGLE</option>
      </select>
    </label>
    {([
      ["num-steps", "mtpSteps"],
      ["eagle-topk", "mtpTopK"],
      ["num-draft-tokens", "mtpDraftTokens"],
    ] as const).map(([flag, label]) => <label key={flag} class="min-w-0 space-y-1 text-sm">{t(`deployments.${label}`)}
      <input class={inputClass} type="number" min={1} step={1} disabled={!enabled}
        value={mtp[flag] || ""} placeholder={algorithm === "" ? t("deployments.enginePlanAuto") : "—"}
        onInput={event => onInput(joinArgs({ ...mtp, [flag]: event.currentTarget.value }, extra))} />
    </label>)}
  </div>;
}

export function SGLangDeploymentArgs({ value, onInput }: ArgsProps) {
  const { mtp, extra } = splitArgs(value);
  return <details class="col-span-2 rounded-xl border border-gray-200 p-3">
    <summary class="cursor-pointer text-sm text-gray-600">{t("deployments.sglangArgs")}</summary>
    <div class="mt-3 space-y-2">
      <textarea aria-label={t("deployments.sglangArgs")} class="w-full rounded-lg border border-gray-200 px-3 py-2 font-mono text-xs"
        rows={4} maxLength={2048} spellcheck={false} value={extra}
        placeholder={"--chunked-prefill-size 4096\n--kv-cache-dtype bf16"}
        onInput={event => onInput(joinArgs(mtp, event.currentTarget.value))} />
      <p class="text-xs text-gray-500">{t("deployments.sglangArgsHint")}</p>
      <details class="text-xs text-gray-500">
        <summary>{t("deployments.sglangArgsSupported")}</summary>
        <p class="break-words font-mono">--chunked-prefill-size, --cuda-graph-max-bs-decode, --max-total-tokens, --mem-fraction-static,
          --kv-cache-dtype, --mamba-ssm-dtype, --mamba-radix-cache-strategy, --tool-call-parser, --reasoning-parser</p>
      </details>
    </div>
  </details>;
}
