import type { ArtifactSource } from "../api/client";
import { modelOwnerIdentity } from "../modelSources";
import { t } from "../i18n";

export function ArtifactOwnerAvatar({ source, path }: { source: ArtifactSource; path: string }) {
  const identity = modelOwnerIdentity(path);
  const palettes = [
    "bg-blue-100 text-blue-700",
    "bg-indigo-100 text-indigo-700",
    "bg-violet-100 text-violet-700",
    "bg-cyan-100 text-cyan-700",
    "bg-emerald-100 text-emerald-700",
    "bg-slate-200 text-slate-700",
  ];
  const sourceLabel = source === "huggingface"
    ? t("mp.sourceHuggingFace")
    : source === "modelscope"
      ? t("mp.sourceModelScope")
      : t("mp.sourceOpenCSG");
  return (
    <span
      class={`flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-[11px] font-bold ${palettes[identity.palette]}`}
      title={`${identity.owner} · ${sourceLabel}`}
    >
      {identity.initial}
    </span>
  );
}
