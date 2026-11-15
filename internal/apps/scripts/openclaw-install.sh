#!/usr/bin/env bash
set -euo pipefail

PACKAGE="openclaw@latest"
REQUIRED_NODE_VERSION="22.16.0"

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

log() {
  printf '%s\n' "$*"
}

version_ge() {
  python3 - "$1" "$2" <<'PY'
import sys
from itertools import zip_longest

def parse(value):
    value = value.lstrip("v").split("-", 1)[0]
    return [int(part) for part in value.split(".") if part.isdigit()]

left = parse(sys.argv[1])
right = parse(sys.argv[2])
for a, b in zip_longest(left, right, fillvalue=0):
    if a > b:
        sys.exit(0)
    if a < b:
        sys.exit(1)
sys.exit(0)
PY
}

node_version() {
  node -v 2>/dev/null || true
}

has_required_node() {
  local current
  current="$(node_version)"
  [[ -n "$current" ]] && version_ge "$current" "$REQUIRED_NODE_VERSION"
}

resolve_brew() {
  if command -v brew >/dev/null 2>&1; then
    command -v brew
    return 0
  fi
  if [[ -x "/opt/homebrew/bin/brew" ]]; then
    printf '%s\n' "/opt/homebrew/bin/brew"
    return 0
  fi
  if [[ -x "/usr/local/bin/brew" ]]; then
    printf '%s\n' "/usr/local/bin/brew"
    return 0
  fi
  return 1
}

load_nvm() {
  if [[ -s "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ]]; then
    # shellcheck source=/dev/null
    . "${NVM_DIR:-$HOME/.nvm}/nvm.sh"
    return 0
  fi
  return 1
}

ensure_node() {
  emit_progress 15 ensuring_node
  if has_required_node; then
    log "INFO: using Node.js $(node_version)"
    return 0
  fi

  if load_nvm && command -v nvm >/dev/null 2>&1; then
    log "INFO: installing Node.js 22 with nvm"
    nvm install 22 >/dev/null
    nvm use 22 >/dev/null
    hash -r
  fi
  if has_required_node; then
    log "INFO: using Node.js $(node_version)"
    return 0
  fi

  local brew_bin=""
  brew_bin="$(resolve_brew || true)"
  if [[ -n "$brew_bin" ]]; then
    log "INFO: installing Node.js 22 with Homebrew"
    "$brew_bin" install node@22
    "$brew_bin" link node@22 --overwrite --force >/dev/null 2>&1 || true
    export PATH="$("$brew_bin" --prefix node@22)/bin:$PATH"
    hash -r
  fi
  if has_required_node; then
    log "INFO: using Node.js $(node_version)"
    return 0
  fi

  if [[ "$(uname -s)" == "Linux" ]]; then
    if command -v apt-get >/dev/null 2>&1; then
      log "INFO: installing Node.js 22 from NodeSource"
      if [[ "$(id -u)" -eq 0 ]]; then
        curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
        apt-get install -y nodejs
      elif command -v sudo >/dev/null 2>&1; then
        curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
        sudo apt-get install -y nodejs
      fi
    elif command -v dnf >/dev/null 2>&1; then
      log "INFO: installing Node.js 22 with dnf"
      if [[ "$(id -u)" -eq 0 ]]; then
        dnf install -y nodejs
      elif command -v sudo >/dev/null 2>&1; then
        sudo dnf install -y nodejs
      fi
    elif command -v yum >/dev/null 2>&1; then
      log "INFO: installing Node.js 22 with yum"
      if [[ "$(id -u)" -eq 0 ]]; then
        yum install -y nodejs
      elif command -v sudo >/dev/null 2>&1; then
        sudo yum install -y nodejs
      fi
    fi
    hash -r
  fi

  if ! has_required_node; then
    log "ERROR: OpenClaw requires Node.js >= ${REQUIRED_NODE_VERSION}."
    log "ERROR: install Node.js 22+ manually, or provide Homebrew/nvm/apt access, then retry."
    exit 1
  fi

  log "INFO: using Node.js $(node_version)"
}

ensure_npm() {
  if command -v npm >/dev/null 2>&1; then
    return 0
  fi
  log "ERROR: npm is required to install OpenClaw."
  exit 1
}

create_launcher() {
  local actual_bin=""
  local node_bin=""
  local launcher_dir=""
  local launcher_path=""

  actual_bin="$(command -v openclaw || true)"
  node_bin="$(dirname "$(command -v node)")"
  if [[ -z "${actual_bin}" || -z "${node_bin}" ]]; then
    return 0
  fi

  if [[ ":${PATH}:" == *":${HOME}/bin:"* ]]; then
    launcher_dir="${HOME}/bin"
  else
    launcher_dir="${HOME}/.local/bin"
  fi
  launcher_path="${launcher_dir}/openclaw"
  mkdir -p "${launcher_dir}"
  if [[ "${actual_bin}" == "${launcher_path}" ]]; then
    return 0
  fi

  cat > "${launcher_path}" <<EOF
#!/usr/bin/env bash
export PATH="${node_bin}:\$PATH"
exec "${actual_bin}" "\$@"
EOF
  chmod +x "${launcher_path}"
  hash -r
  log "INFO: created launcher: ${launcher_path}"
}

prewarm_openclaw_runtime() {
  local registry="$1"
  if ! command -v openclaw >/dev/null 2>&1; then
    return 0
  fi

  emit_progress 70 prewarming_runtime
  log "INFO: prewarming OpenClaw runtime dependencies for csghub-lite profile"
  NPM_CONFIG_REGISTRY="${registry}" OPENCLAW_DISABLE_BONJOUR=1 openclaw --profile csghub-lite onboard \
    --non-interactive \
    --auth-choice custom-api-key \
    --custom-provider-id opencsg \
    --custom-compatibility openai \
    --custom-base-url http://127.0.0.1:11435/ai-gateway/v1 \
    --custom-model-id Qwen/Qwen3-0.6B \
    --custom-api-key csghub-lite \
    --accept-risk \
    --skip-channels \
    --skip-search \
    --skip-ui \
    --skip-skills \
    --skip-daemon \
    --skip-health
}

wait_for_openclaw_dependency_installs() {
  local timeout_seconds="${1:-600}"
  if ! command -v pgrep >/dev/null 2>&1; then
    return 0
  fi

  local deadline=$((SECONDS + timeout_seconds))
  while ((SECONDS < deadline)); do
    if ! pgrep -f 'npm install .*(@openai/codex|@mariozechner/pi|@anthropic-ai/sdk)' >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  log "WARN: OpenClaw dependency npm install is still running after ${timeout_seconds}s"
}

wait_for_tcp_port() {
  local port="$1"
  local timeout_seconds="$2"
  python3 - "$port" "$timeout_seconds" <<'PY'
import socket
import sys
import time

port = int(sys.argv[1])
deadline = time.time() + int(sys.argv[2])
while time.time() < deadline:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(0.5)
        try:
            sock.connect(("127.0.0.1", port))
            sys.exit(0)
        except OSError:
            time.sleep(0.5)
sys.exit(1)
PY
}

prewarm_openclaw_gateway_runtime() {
  local registry="$1"
  if ! command -v openclaw >/dev/null 2>&1; then
    return 0
  fi

  emit_progress 75 prewarming_gateway
  log "INFO: prewarming OpenClaw gateway runtime dependencies for csghub-lite profile"
  local gateway_log=""
  gateway_log="$(mktemp "${TMPDIR:-/tmp}/openclaw-gateway-prewarm.XXXXXX.log")"
  NPM_CONFIG_REGISTRY="${registry}" OPENCLAW_DISABLE_BONJOUR=1 openclaw --profile csghub-lite gateway run --force >"${gateway_log}" 2>&1 &
  local gateway_pid="$!"

  if wait_for_tcp_port 18789 180; then
    log "INFO: OpenClaw gateway prewarm completed"
  else
    log "WARN: OpenClaw gateway prewarm did not become ready within 180s"
  fi

  if kill -0 "${gateway_pid}" >/dev/null 2>&1; then
    kill "${gateway_pid}" >/dev/null 2>&1 || true
    wait "${gateway_pid}" >/dev/null 2>&1 || true
  fi
  if [[ -s "${gateway_log}" ]]; then
    sed 's/^/openclaw-gateway-prewarm: /' "${gateway_log}" || true
  fi
  rm -f "${gateway_log}"
}

choose_registry() {
  local seen=""
  local registry
  local registries=()
  if [[ -n "${NPM_CONFIG_REGISTRY:-}" ]]; then
    registries+=("${NPM_CONFIG_REGISTRY}")
  fi
  registries+=("https://registry.npmmirror.com" "https://registry.npmjs.org")

  for registry in "${registries[@]}"; do
    [[ "$seen" == *"|$registry|"* ]] && continue
    seen="${seen}|${registry}|"
    printf 'INFO: checking npm registry %s\n' "${registry}" >&2
    if npm view "${PACKAGE}" version --registry "${registry}" >/dev/null 2>&1; then
      printf '%s\n' "${registry}"
      return 0
    fi
  done
  return 1
}

emit_progress 5 preflight
ensure_node
ensure_npm

registry="$(choose_registry || true)"
if [[ -z "${registry}" ]]; then
  log "ERROR: unable to reach a working npm registry for ${PACKAGE}"
  exit 1
fi

log "INFO: using npm registry ${registry}"
emit_progress 35 installing
npm install -g "${PACKAGE}" --registry "${registry}"
create_launcher
prewarm_openclaw_runtime "${registry}"
wait_for_openclaw_dependency_installs 600
prewarm_openclaw_gateway_runtime "${registry}"

emit_progress 80 verifying
if command -v openclaw >/dev/null 2>&1; then
  log "INFO: installed binary: $(command -v openclaw)"
  openclaw --version || true
fi

emit_progress 100 complete
log "INFO: OpenClaw installation complete"
