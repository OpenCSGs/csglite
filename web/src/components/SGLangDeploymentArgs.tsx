import { t } from "../i18n";

export function SGLangDeploymentArgs({ value, onInput }: {
  value: string; onInput: (value: string) => void;
}) {
  return <label class="col-span-2 space-y-1 text-sm">
    {t("deployments.sglangArgs")}
    <textarea class="w-full rounded-lg border border-gray-200 px-3 py-2 font-mono text-xs"
      rows={4} maxLength={2048} spellcheck={false} value={value}
      placeholder={"--chunked-prefill-size 4096\n--kv-cache-dtype bf16"}
      onInput={event => onInput(event.currentTarget.value)} />
    <p class="text-xs text-gray-500">{t("deployments.sglangArgsHint")}</p>
    <details class="text-xs text-gray-500">
      <summary>{t("deployments.sglangArgsSupported")}</summary>
      <p class="break-words font-mono">--chunked-prefill-size, --cuda-graph-max-bs-decode, --max-total-tokens, --mem-fraction-static,
        --kv-cache-dtype, --mamba-ssm-dtype, --mamba-radix-cache-strategy, --speculative-algorithm, --speculative-num-steps,
        --speculative-eagle-topk, --speculative-num-draft-tokens, --tool-call-parser, --reasoning-parser</p>
    </details>
  </label>;
}
