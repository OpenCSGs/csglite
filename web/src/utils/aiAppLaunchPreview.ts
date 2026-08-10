function cliLaunchAppName(appID: string): string {
  switch (appID) {
    case "claude-code":
      return "claude";
    case "open-code":
      return "opencode";
    case "open-code-review":
      return "ocr";
    case "codex":
      return "codex";
    case "codex-app":
      return "codex-app";
    case "zcode":
      return "zcode";
    case "pi":
      return "pi";
    case "openclaw":
      return "openclaw";
    default:
      return "";
  }
}

export function parseAIAppModelKey(key: string): { source: string; model: string } {
  for (const nestedSourcePrefix of ["provider:", "pool:"]) {
    if (!key.startsWith(nestedSourcePrefix)) continue;
    const next = key.indexOf(":", nestedSourcePrefix.length);
    if (next > nestedSourcePrefix.length) {
      return { source: key.slice(0, next), model: key.slice(next + 1) };
    }
  }
  const first = key.indexOf(":");
  if (first > 0) {
    return { source: key.slice(0, first), model: key.slice(first + 1) };
  }
  return { source: "", model: key };
}

export function aiAppLaunchPreview(appID: string, modelID: string, source: string, providerName: string): string {
  const launchName = cliLaunchAppName(appID);
  if (!launchName) return "";

  const normalizedSource = source.trim().toLocaleLowerCase();
  if (normalizedSource.startsWith("pool:")) {
    const pool = providerName.trim() || source.trim().slice("pool:".length);
    const launchWithPool = `csghub-lite launch ${launchName} --pool "${pool}"`;
    return [
      `csghub-lite launch ${launchName}`,
      launchWithPool,
      `${launchWithPool} -- --help`,
    ].join("\n");
  }

  const provider = providerDisplayName(source, providerName);
  const modelArg = modelID ? `"${modelID}"` : '"<model-id>"';
  const providerArg = provider ? `"${provider}"` : '"<provider-name>"';
  const launchWithModel = `csghub-lite launch ${launchName} --model ${modelArg} --provider ${providerArg}`;
  return [
    `csghub-lite launch ${launchName}`,
    launchWithModel,
    `${launchWithModel} -- --help`,
  ].join("\n");
}

function providerDisplayName(source: string, providerName: string): string {
  const value = source.trim();
  const normalized = value.toLocaleLowerCase();
  if (!normalized || normalized === "local") return "local";
  if (providerName.trim()) return providerName.trim();
  if (normalized === "cloud") return "csghub";
  return normalized.startsWith("provider:") ? value.slice("provider:".length) : value;
}
