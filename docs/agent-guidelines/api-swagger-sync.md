# API Swagger Sync

- Any change to local HTTP APIs must update Swagger/OpenAPI in the same task.
- This applies to new, removed, or renamed `/api/*` and `/v1/*` routes, and to
  request/response shape changes on existing endpoints.
- Update `openapi/local-api.json` whenever the route surface or schema changes.
- Keep the embedded docs copy in sync when needed for local preview or committed
  static assets.
- Run `go test ./internal/server` after API doc changes so route/doc drift is
  caught by `openapi_sync_test.go`.

## Compatibility

- Treat local HTTP APIs as user-facing contracts. Preserve backward
  compatibility for existing routes, query parameters, request bodies, response
  fields, status codes, and error shapes unless the user explicitly approves a
  breaking change.
- Prefer additive changes for shipped APIs: add optional fields, new endpoints,
  or new enum values instead of renaming/removing fields or changing existing
  meanings.
- When extending an existing API, keep old request shapes working and avoid
  making clients depend on newly added response fields. Add compatibility tests
  for legacy payloads whenever request parsing or response schemas change.
- If a breaking API change is unavoidable, document the migration path in the
  same task and update OpenAPI, tests, and release notes accordingly.

```go
// If you add a route in internal/server/routes.go:
mux.HandleFunc("GET /api/models/{namespace}/{name}/manifest", s.handleModelManifest)

// You must also document it in:
// openapi/local-api.json
```
