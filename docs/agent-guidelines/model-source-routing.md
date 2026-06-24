# Model Source Routing

- Treat `source` as part of every model inference contract. When frontend code
  calls `/api/*` or `/v1/*` model endpoints, pass the selected model source
  through with the model id whenever local and cloud models share the UI path.
- Do not let cloud models silently fall back to local runtimes. Backend handlers
  must explicitly branch on `source=cloud` and proxy to the configured AI
  Gateway or return a clear cloud credential/provider error.
- Keep parity across chat, image, embedding, ASR, and future model modalities:
  if one path accepts cloud models, the corresponding upload/streaming path must
  preserve `source` too.
- Add or update regression tests for source-sensitive routes. Tests should cover
  at least one cloud request and assert it does not try to load a local model.
- If a local HTTP request shape changes to carry `source`, update
  `openapi/local-api.json` and embedded static OpenAPI in the same task.
