# Enterprise (EE) Feature Gating

CSGLite is open core: one repository, one binary, and a signed license decides
which features are active. Licenses are issued by the CSGHub license issuer
(starhub-server); CSGLite only verifies them. Design:
`docs/guides/ee-license-design.md`.

## Rules

- `internal/license/features.go` is the single source of truth for gated
  features. Never gate on a raw string; reference a `FeatureDefinition`.
- Only a definition with `Gated: true` consults the license. Everything else
  is always enabled, in every edition and in development builds. Moving a
  feature to EE means setting `Gated: true` on its catalog entry and wrapping
  its routes; adding a catalog entry alone changes nothing.
  `TestUngatedFeaturesIgnoreTheLicense` asserts the shipped catalog gates
  nothing; update it deliberately when the first feature is gated.
- Boolean keys are `feature.lite.<name>`, integer limits are
  `quota.lite.<name>`. `ValidateCatalog` and `TestCatalogIsValid` enforce this.
- The CSGHub issuer accepts any well-formed `feature.lite.*` / `quota.lite.*`
  key without registration, so a new catalog key needs no starhub-server
  change. Registering it there (`common/types/feature_registry.go` plus
  `common/i18n/*/features.json`) is optional and only adds a display name in
  the issuer UI.
- Gate HTTP routes only in `internal/server/routes.go` by wrapping the handler
  with `s.requireFeature(license.FeatureX)`. Do not check the license inside
  handlers. Background jobs and non-HTTP entry points call
  `s.license.Enabled(def)` / `s.license.Limit(def)` at their entry.
- A refused route answers 403 with `code: "feature_not_licensed"`, `feature`
  and `edition_required`. Keep that shape; the web UI switches on it.
- Features already shipped in the Community edition are never moved behind
  the license. Only new features may be gated.
- Do not add a Community-only and an Enterprise-only implementation of the
  same feature; use a `quota.lite.*` limit instead of a code fork.
- The wire format (gob + RSA-SHA256 PKCS#1 v1.5 + LICENSE KEY PEM) mirrors
  starhub-server's `builder/rsa`. `TestDecodeCSGHubGoldenVector` pins it; if
  it fails, fix the decoder, never the vector.
- New EE implementation code goes under `ee/` with the enterprise license
  header once that directory exists; the gating framework itself stays
  Apache-2.0.
- A PR that gates a feature must, in the same change: set `Gated: true` on
  the catalog entry, wrap the route(s), update `openapi/local-api.json`, add the web UI lock
  state, and prefix the release note with `[EE]`.
