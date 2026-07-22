import { t } from "../i18n";

// Maps the English load/conversion progress steps reported by the backend
// (see internal/convert, internal/inference, internal/imagegen) to i18n keys
// so the UI can show localized key steps instead of a generic "Converting...".
const stepPatterns: Array<[RegExp, string]> = [
  [/^Preparing Python conversion environment/i, "loadStep.prepareConvertEnv"],
  [/^Creating Python virtual environment/i, "loadStep.createVenv"],
  [/^Checking Python conversion packages/i, "loadStep.checkPackages"],
  [/^Installing Python package manager/i, "loadStep.installPip"],
  [/^Installing CPU PyTorch/i, "loadStep.installTorchMirror"],
  [/^Retrying CPU PyTorch install/i, "loadStep.retryTorch"],
  [/^Installing model conversion Python packages/i, "loadStep.installConvertDeps"],
  [/^Python conversion environment ready/i, "loadStep.envReady"],
  [/^Downloading converter/i, "loadStep.downloadConverter"],
  [/^Preparing converter/i, "loadStep.prepareConverter"],
  [/^Prepar(?:ing|ed) matching gguf-py/i, "loadStep.prepareGGUFPy"],
  [/^Converting vision encoder/i, "loadStep.convertMMProj"],
  [/^Converting with /i, "loadStep.convertGGUF"],
  [/^Retrying converter after automatic repair/i, "loadStep.retryConvert"],
  [/^Upgrading Python package/i, "loadStep.upgradePackages"],
  [/^Converting tensor/i, "loadStep.convertTensor"],
  [/^Fusing lerp tensors/i, "loadStep.convertTensor"],
  [/^Starting llama-server/i, "loadStep.startLlama"],
  [/^Loading model with llama-server/i, "loadStep.loadLlama"],
  [/^detect system/i, "loadStep.detectSystem"],
  [/^prepare image runtime/i, "loadStep.prepareImageRuntime"],
  [/^prepare ASR runtime/i, "loadStep.prepareASRRuntime"],
  [/^prepare embedding runtime/i, "loadStep.prepareEmbeddingRuntime"],
  [/^prepare pip and uv/i, "loadStep.preparePip"],
  [/^create Python venv/i, "loadStep.createVenv"],
  [/^install PyTorch/i, "loadStep.installTorch"],
  [/^(install|upgrade) Diffusers dependencies/i, "loadStep.installDiffusers"],
  [/^(install|upgrade) ASR dependencies/i, "loadStep.installASRDeps"],
  [/^(install|upgrade) embedding dependencies/i, "loadStep.installEmbeddingDeps"],
];

/** Localizes a backend load progress step; falls back to the raw step text. */
export function localizeLoadStep(step: string): string {
  const trimmed = step.trim();
  if (!trimmed) return "";
  for (const [pattern, key] of stepPatterns) {
    if (pattern.test(trimmed)) return t(key);
  }
  return trimmed;
}

/** Formats a load step with optional (current/total) percentage suffix. */
export function formatLoadStep(step: string, current?: number, total?: number): string {
  const label = localizeLoadStep(step);
  if (!label) return "";
  if (total && total > 0 && current) {
    const pct = Math.round((current / total) * 100);
    return `${label} (${current}/${total}) ${pct}%`;
  }
  return label;
}
