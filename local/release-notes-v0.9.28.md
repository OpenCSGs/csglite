## What's New

- Fixed "File Size" and "Date Time" sorting in the Models and Datasets library lists (#67): rows for queued, in-progress, and failed downloads now reorder together with downloaded entries when toggling the column headers.
- Fixed `Error 400: Unrecognized request arguments (repetition_penalty, top_k)` when chatting through strict third-party OpenAI-compatible providers (#68): these non-standard sampling arguments are no longer sent to third-party gateways.
- Added pagination to the Models and Datasets library lists (#69): first/prev/next/last controls with page numbers, a 10/20/50/All page-size selector, and a total item count.
