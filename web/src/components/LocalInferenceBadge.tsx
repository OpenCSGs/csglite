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
      class={`inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium ${
        supported ? "bg-green-50 text-green-700" : "bg-gray-100 text-gray-500"
      }`}
    >
      {t(localInferenceBadgeKey(mode, prefix))}
    </span>
  );
}
