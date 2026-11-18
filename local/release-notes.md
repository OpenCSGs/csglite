## Changes

- Fix missing streaming output when tool-calling clients (such as OpenClaw) chat with cloud or third-party provider models through `/v1/chat/completions`: responses now stream incrementally over SSE instead of arriving in one chunk after the full completion (#59).
