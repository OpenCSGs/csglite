## Changes

- Show live download speed (bytes per second) in the CLI `pull` progress, Library and Datasets download rows, Marketplace download actions, and cluster model sync / managed deployment progress (#302).
- Fix missing streaming output when tool-calling clients (such as OpenClaw) chat with cloud or third-party provider models through `/v1/chat/completions`: responses now stream incrementally over SSE instead of arriving in one chunk after the full completion (#59).
