# Xiaozhi Docker App

csghub-lite can install and run Xiaozhi from the **AI Apps** page. The managed
deployment uses Docker Compose and images mirrored to the OpenCSG Alibaba Cloud
registry.

## Requirements

- Docker 24 or newer with Docker Compose v2
- An `amd64` host, or Apple Silicon with Docker Desktop's amd64 emulation
- At least 4 CPU cores, 8 GB memory, and 50 GB free disk space
- Port `8080` available on the host

The supplied Xiaozhi images are currently Linux `amd64` images. csghub-lite
enables Docker Desktop emulation on Apple Silicon, which is functional but may
be slower than native execution. Other ARM hosts remain unsupported.

The mirrored repository is
`opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi`. The published
immutable tags are `backend-20260718`, `frontend-20260718`,
`pgvector-pg16-20260718`, `redis-7-alpine-20260718`, `mailpit-20260718`, and
`manticore-10.1.0-20260718`. Managed Compose files pin their registry digests,
so moving a tag cannot silently change an installed deployment.

If Docker, Compose, or the Docker daemon is unavailable, the Xiaozhi card shows
the missing prerequisite and keeps installation disabled. Install or start
Docker, then refresh AI Apps.

## Managed files

All Xiaozhi runtime files are stored below the configured csghub-lite storage
root:

```text
<storage-root>/apps/xiaozhi/
├── compose.yml
├── .env
├── config/
├── storage/
├── postgres/
├── redis/
├── mailpit/
├── manticore/
└── logs/
```

The default storage root is `~/.csghub-lite`. The generated `.env`,
`compose.yml`, and AI provider configuration are user-private files. Database
passwords are generated during installation and are not copied from the
deployment bundle.

The managed local deployment creates its first administrator automatically:

- Email: `csglite@opencsg.com`
- Password: `csglite`

Set `XIAOZHI_ADMIN_EMAIL` and `XIAOZHI_ADMIN_PASSWORD` in `.env` before the
first start to change these defaults. Existing custom values are never
overwritten. The web port is bound to `127.0.0.1`, so the default credentials
are not exposed to the local network. Change the password after signing in if
the machine is shared. The bootstrap step also creates a private `Xiaozhi`
Workspace owned by the administrator when the account has no existing
Workspace, preventing the first login from redirecting to missing content.

Stopping Xiaozhi keeps its containers and data. Uninstalling removes the
managed Compose stack and deployment descriptor, but deliberately preserves
the bind-mounted application and database data. Back up the entire `xiaozhi`
directory before manually deleting it.

## Models

Before starting Xiaozhi, csghub-lite selects available models independently for
these task groups:

- language model
- speech recognition
- embedding
- image generation

The detail drawer lets you override each recommendation. Model source is kept
with each selection. A model is never substituted into an incompatible task,
and an ambiguous model ID shared by multiple sources must be resolved before
launch.

Xiaozhi connects back to the local OpenAI-compatible API through
`host.docker.internal`. Local model runtimes are loaded lazily when Xiaozhi
sends its first request. Reranking and text-to-speech are not configured by
this integration because csghub-lite does not currently expose compatible
local endpoints for them.

## Access and maintenance

After the stack is healthy, open Xiaozhi from AI Apps or visit:

```text
http://localhost:8080
```

Installation and Compose output is written to
`<storage-root>/apps/xiaozhi/logs/install.log`. Internal Mailpit and Manticore
ports are not published to the host.
