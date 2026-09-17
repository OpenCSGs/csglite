# CSGLite Enterprise Edition (EE) code

Everything under this directory is licensed under the CSGLite Enterprise
Edition License in [`LICENSE`](LICENSE), not under the Apache-2.0 license that
covers the rest of the repository. In short: you may read, modify and run it
for development and testing; production use requires a valid CSGLite Enterprise
license issued by OpenCSG; redistribution is not permitted.

## What belongs here

- Implementation code for features whose catalog entry in
  `internal/license/features.go` is marked `Gated: true`.
- Nothing else. The license verification framework itself
  (`internal/license`, the `/api/license*` handlers, the CLI) stays under
  Apache-2.0 so the Community edition can always verify a license.

## File header

Every source file in this directory starts with:

```go
// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.
```

See `docs/agent-guidelines/ee-features.md` for the full rules and
`docs/guides/ee-license-design.md` for the design.
