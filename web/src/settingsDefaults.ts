import { saveSettings } from "./api/client";
import { t } from "./i18n";

// Restoring application options must preserve the selected community and login.
export async function restoreApplicationDefaults() {
  const settings = await saveSettings({
    cloud_provider_name: "",
    llama_use_model_max_ctx: false,
    llama_num_parallel: 1,
    context_compression: "off",
  });
  return { settings, message: t("settings.resetDefaultsSuccess") };
}
