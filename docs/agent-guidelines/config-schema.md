# Configuration Schema Discipline

- Treat `~/.csghub-lite/config.json` as a stable, user-visible schema, not as a
  convenient dumping ground for feature state.
- Before adding a top-level key, audit existing fields and writers for the same
  domain. Reuse or extend the existing representation when semantics overlap.
- Keep one source of truth for one concept. Do not split one record across
  parallel maps such as separate model and source maps; use a typed object.
- Group related feature settings under one typed section instead of adding
  unrelated top-level keys. App-specific state belongs in a generic per-app
  structure or an app-owned runtime file, not a new field for each app.
- A new persisted key requires a documented reason why existing structures
  cannot represent it, plus load/save round-trip and backward-compatibility
  tests.
- When replacing an overlapping schema, migrate legacy values on load and stop
  writing legacy keys. Do not keep two writable representations indefinitely.
- Runtime-only and derived values must use `json:"-"` or be recomputed; do not
  persist them merely because they are available in `Config`.
