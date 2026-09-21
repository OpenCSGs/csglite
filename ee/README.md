# CSGLite Enterprise Edition (EE) code

Everything under this directory is licensed under the CSGLite Enterprise
Edition License in [`LICENSE`](LICENSE), not under the Apache-2.0 license that
covers the rest of the repository. The licence is published in Chinese and
English; where the two differ, the Chinese text governs.

In short, and without replacing anything the licence itself says: you may read
the source and modify it for internal research, development and testing.
Production use needs a valid CSGLite Enterprise Edition licence from OpenCSG
matching your actual usage, and the licence defines production use broadly
enough to include a pilot or a proof of concept that carries real business.

The one exception is the community grant at the top of the licence: a feature
that runs without a licence may be used in production within the limit the
software enforces for unlicensed users, which for the LAN cluster is two nodes.
That is what makes the community node cap a real entitlement rather than
something the licence forbids.
Redistribution, sublicensing and offering the Software to third parties as a
hosted service are not permitted. Replacing the logo or the UI appearance, or
building anything commercial on the Software, requires written notice to
OpenCSG first, whether or not you hold a licence.

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

## Adding a feature

One feature, one directory: `ee/<feature>/`. Nothing is shared between them by
default, and anything that turns out to be shared belongs under the Apache tree
instead, because Enterprise code may depend on Apache code and never the other
way round. `internal/httpjson` is the first of those: it holds the JSON error
envelope every HTTP surface answers with, so a second feature does not grow its
own copy the way the cluster did.

## Adding to `ee/cluster`

The package is flat on purpose, but the files are not a pile: each one holds a
single job, and a new declaration goes to the file that already owns its
subject rather than to whichever file is open.

| File | Holds |
| --- | --- |
| `manager.go` | the `Host` seam, `Options`, the `Manager` type, and its lifecycle: start, activate, deactivate, stop |
| `identity.go` | this node's UUID, private key and self-signed certificate |
| `membership.go` | `cluster.json`: members, pinned fingerprints, join tokens, admission codes, tombstones |
| `membership_ops.go` | what an operator does to membership: create, join, invite, leave, remove, rename |
| `discovery.go`, `announce.go`, `hostname.go` | finding other nodes and publishing this one |
| `directory.go` | the live view of each member: health, addresses, breakers, reservations |
| `health.go` | the polling loop that keeps that view current |
| `gossip.go` | folding a peer's member table into ours |
| `status.go` | what this node reports about itself, and the candidate list the scheduler ranks |
| `scheduler.go` | ranking candidates by predicted completion time |
| `affinity.go` | keeping one conversation on one node |
| `perf.go`, `engine_cluster.go` | measured throughput, and the engine that dispatches and fails over |
| `engine_node.go` | one remote member seen as an `inference.Engine` |
| `models.go` | the model inventory across the cluster, and copying one from a peer |
| `source.go` | the `cluster` and `node:<uuid>` source vocabulary |
| `transport.go`, `peerclient.go`, `peerrpc.go`, `addr.go` | mutual TLS, pooled clients, peer calls, address handling |
| `protocol.go` | the types that go on the wire between nodes |
| `api.go`, `api_views.go` | `/api/cluster/*` for operators, on the `adminAPI` receiver |
| `peer_handlers.go` | `/cluster/v1/*` for other nodes, on the `peerAPI` receiver |

The two HTTP surfaces hang off their own small types rather than off `Manager`,
so adding an endpoint does not grow the type that also runs discovery, gossip
and scheduling.

See `docs/agent-guidelines/ee-features.md` for the full rules and
`docs/guides/ee-license-design.md` for the design.
