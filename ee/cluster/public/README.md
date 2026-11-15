# Public cluster tunnel

The transport is integrated by [`../public_network.go`](../public_network.go)
with the existing cluster identity, join-token admission, membership store,
license quota and scheduler. The server exposes a separate public inference
listener; worker processes only establish outbound TLS connections.

Deployment, CLI configuration and model sharing are described in the
[public cluster guide](../../../docs/guides/public-cluster-requirements.md).

`Gateway` accepts an authenticated WebSocket on `/cluster/v1/connect`. A worker
opens the connection, publishes an initial status and then serves local HTTP
inference over yamux streams opened by the gateway. The worker opens no TCP
listener and verifies the gateway's TLS certificate. A private CA can be supplied
through its WebSocket dialer.

## Host responsibilities

- The gateway's required `Authorize` callback checks approved membership,
  node credentials, revocation and licensing on every connection. It must not
  treat requests from loopback as trusted.
- `ValidateStatus` validates application telemetry without side effects.
  `Snapshot` returns the current session's copied status, excluding disconnected,
  draining and stale sessions. The scheduler will consume these snapshots.
- Before `RoundTrip`, the host checks the requested model and reserves a
  concurrency slot. `RoundTrip` performs one attempt and never retries an
  uncertain execution. Its caller owns streaming responses and deadlines.
- The worker's required `AllowModel` callback checks the owner's current sharing
  choices on every request. Its handler must execute on this machine only.
- The host reuses persisted node certificates and membership, signs a fresh
  audience-bound nonce for each connection, and owns jittered reconnection. `HandshakeError` preserves HTTP rejection status so
  revoked identities can stop retrying rather than automatically re-enrolling.
- Removing a member requires persisted membership removal and `Disconnect`.
  The application also rotates the join token so an old invitation cannot
  re-admit the removed node. Other admitted identities remain valid.

Both ends enforce an inference-only method/path whitelist, a 32 MiB request body
limit and local execution. Authorization, cookies and node credentials are not
forwarded to the model handler. Background Responses and stored continuation
are rejected. Control messages are limited to 1 MiB.

Application heartbeats, rather than WebSocket/yamux keepalives, determine status
freshness. A newly authenticated session replaces an older session with the same
node identity; old heartbeats and old teardown cannot overwrite the replacement.
Canceling one request closes its logical connection without closing the tunnel.

## Verification

The tests use a TLS gateway and real outbound worker connections, with a local
fake inference handler. They cover streaming, request cancellation, credential
stripping, per-request sharing checks, source/path restrictions, heartbeat
expiry and session replacement. Application-level tests also cover token enrollment, the existing scheduler,
sharing persistence, process restart, quota refusal and revocation. Separate
public-host validation exercised real Qwen2 0.5B and Qwen2.5 1.5B models, streamed
responses and a client outside the deployment network. Worker-side gateway
reachability follows accepted tunnel sessions, including disconnects and reconnection.

```sh
go test -race ./ee/cluster/... ./ee/edge
go test ./internal/server
```

`ee/tunnel` shares the existing Lite WebSocket adapter and yamux settings with
`ee/edge`. The CSGHub wire protocol, credentials and model approval remain
separate; the new transport does not depend on the CSGHub server package.
