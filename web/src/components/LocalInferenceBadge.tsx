import { t } from "../i18n";
import { localInferenceBadgeKey, supportsLocalInference, type LocalInferenceMode } from "../utils/localInference";

export function LocalInferenceBadge({
  mode,
  prefix,
}: {
  mode: LocalInferenceMode;
  prefix: "mp" | "lib";
}) {
  const supported = supportsLocalInference(mode);
  return (
    <span
      class={`inline-flex items-center rounded-full border px-2.5 py-1 text-xs font-semibold ${
        supported
          ? "border-green-100 bg-green-50 text-green-700"
          : "border-red-200 bg-red-50 text-red-700"
      }`}
    >
      {t(localInferenceBadgeKey(mode, prefix))}
    </span>
  );
}
