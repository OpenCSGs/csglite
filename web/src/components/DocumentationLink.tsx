import { openExternalURL } from "../api/client";
import { communitySettings } from "../cloudAccount";
import { locale, t } from "../i18n";

const documentationURL =
  "https://opencsg.com/docs/csghub/101/function/csghub-lite/intro";

export function DocumentationLink() {
  void locale.value;
  if (communitySettings.value?.hidden_nav_items?.includes("help")) return null;
  return (
    <a
      href={documentationURL}
      target="_blank"
      rel="noopener noreferrer"
      onClick={async (event) => {
        if (!communitySettings.value?.desktop_mode) return;
        event.preventDefault();
        try {
          if (await openExternalURL(documentationURL)) return;
        } catch (error) {
          console.error(
            "failed to open documentation in the system browser",
            error,
          );
        }
        window.open(documentationURL, "_blank", "noopener,noreferrer");
      }}
      class="inline-flex shrink-0 items-center gap-1.5 rounded-md py-2 text-sm text-gray-500 transition-colors hover:text-indigo-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-indigo-500"
    >
      <svg
        class="h-4 w-4"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        stroke-width="1.5"
        aria-hidden="true"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d="M14 3H6a2 2 0 00-2 2v14a2 2 0 002 2h12a2 2 0 002-2V9M14 3v6h6M14 3l6 6M8 13h8M8 17h5"
        />
      </svg>
      {t("settings.documentation")}
      <svg
        class="h-3.5 w-3.5"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        stroke-width="1.5"
        aria-hidden="true"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d="M14 3h7v7m0-7L10 14M10 3H5a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-5"
        />
      </svg>
    </a>
  );
}
