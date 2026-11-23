"""Container-only supervisor. Uses the Python already provided by SGLang.

The launcher itself uses Python 3.9 syntax; SGLang's newer Python requirement
is confined to its optional container, not CSGLite's shared Python runtime.
"""
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener

from lite_update import select_binary

PROVIDER_ID = "csglite-docker-sglang"
STOPPING = False
HTTP = build_opener(ProxyHandler({}))
# Native managed mode is the default: CSGLite starts SGLang per model through
# its own API, so the container supervisor must not start a second process.
# Set SGLANG_LEGACY_FIXED_MODEL=1 for the original fixed-model behavior.
LEGACY_ENV = "SGLANG_LEGACY_FIXED_MODEL"


def app_home():
    return Path.home() / ".csghub-lite"


def read_json(path, default):
    if not path.exists():
        return default
    return json.loads(path.read_text(encoding="utf-8"))


def write_json(path, value):
    temporary = path.with_name(path.name + ".sglang-new")
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
    os.replace(str(temporary), str(path))


def configure_cluster(home):
    """Provision the existing cluster schema from container environment.

    Join credentials stay in the inherited environment. Public workers expose
    management only on container loopback and connect outbound to the Master.
    """
    path = home / "config.json"
    config = read_json(path, {})
    if not isinstance(config, dict):
        raise ValueError("config.json must contain an object")
    cluster = config.get("cluster", {})
    if cluster is None:
        cluster = {}
    if not isinstance(cluster, dict):
        raise ValueError("config.json cluster must contain an object")
    cluster = dict(cluster)
    fields = {"MODE": "mode", "GATEWAY_URL": "gateway_url",
              "TLS_CA_FILE": "tls_ca_file", "PUBLIC_LISTEN_ADDR": "public_listen_addr",
              "TLS_CERT_FILE": "tls_cert_file", "TLS_KEY_FILE": "tls_key_file"}
    changed = False
    for suffix, field in fields.items():
        name = "CSGHUB_LITE_CLUSTER_" + suffix
        if name not in os.environ:
            continue
        value = os.environ[name].strip()
        if field == "mode":
            value = value.lower()
        if value:
            cluster[field] = value
        else:
            cluster.pop(field, None)
        changed = True
    mode = str(cluster.get("mode", "")).strip().lower()
    if mode not in ("", "lan", "gateway", "worker"):
        raise ValueError("CSGHUB_LITE_CLUSTER_MODE must be lan, gateway or worker")
    if mode in ("gateway", "worker") and (
            os.getenv("CSGHUB_LITE_CLUSTER_SECRET", "").strip() or cluster.get("secret")):
        raise ValueError("Public cluster mode uses join tokens; unset the LAN cluster secret")
    if changed:
        config["cluster"] = cluster
        write_json(path, config)
    return "127.0.0.1:11435" if mode in ("gateway", "worker") else "0.0.0.0:11435"


def configure_provider(home, base_url, model):
    """Reuse CSGLite's provider and model-selection schemas; preserve others."""
    home.mkdir(parents=True, exist_ok=True)
    path = home / "providers.json"
    providers = read_json(path, [])
    if not isinstance(providers, list):
        raise ValueError("providers.json must contain an array")
    provider = {"id": PROVIDER_ID, "name": "SGLang", "provider": "openai",
                "base_url": base_url, "api_key": os.getenv("SGLANG_API_KEY", ""),
                "enabled": True}
    providers = [p for p in providers if p.get("id") != PROVIDER_ID] + [provider]
    selections_path = home / "provider_model_allowlist.json"
    selections = read_json(selections_path, {"version": 1, "providers": {}})
    if not isinstance(selections, dict) or not isinstance(selections.get("providers", {}), dict):
        raise ValueError("provider_model_allowlist.json must contain a providers object")
    if selections.get("version", 1) != 1:
        raise ValueError("unsupported provider model selection schema")
    selections.setdefault("providers", {})[PROVIDER_ID] = [
        {"model": model, "original_model": model, "pipeline_tag": "text-generation",
         "input_modalities": ["text"], "output_modalities": ["text"]}]
    write_json(path, providers)
    write_json(selections_path, selections)


def remove_provider(home):
    """Drop only this launcher's fixed-model provider and selection.

    Other providers, pools and user selections are preserved. Native mode must
    not leave a ghost provider pointing at the fixed port 30000.
    """
    providers_path = home / "providers.json"
    providers = read_json(providers_path, None)
    if isinstance(providers, list):
        remaining = [p for p in providers if p.get("id") != PROVIDER_ID]
        if len(remaining) != len(providers):
            write_json(providers_path, remaining)
    selections_path = home / "provider_model_allowlist.json"
    selections = read_json(selections_path, None)
    if isinstance(selections, dict) and isinstance(selections.get("providers"), dict):
        if selections["providers"].pop(PROVIDER_ID, None) is not None:
            write_json(selections_path, selections)


def base_url():
    return os.getenv("SGLANG_BASE_URL", "http://127.0.0.1:30000/v1").rstrip("/")


def seed_runtime_cache(bundle, home):
    # Keep bundled AOT kernels available without overwriting a user's cache.
    for name in ("huggingface", "sglang"):
        source = bundle / name
        target = Path(os.getenv("HF_HOME", str(home / "cache" / name))) if name == "huggingface" else home / "cache" / name
        if not source.is_dir():
            continue
        for path in source.rglob("*"):
            destination = target / path.relative_to(source)
            if path.is_symlink():
                if not destination.exists() and not destination.is_symlink():
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    destination.symlink_to(os.readlink(path))
            elif path.is_dir():
                destination.mkdir(parents=True, exist_ok=True)
            elif not destination.exists() and not destination.is_symlink():
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(path, destination)


def healthy(url, authenticated=False):
    headers = {}
    if authenticated and os.getenv("SGLANG_API_KEY"):
        headers["Authorization"] = "Bearer " + os.environ["SGLANG_API_KEY"]
    try:
        with HTTP.open(Request(url, headers=headers), timeout=3) as response:
            return response.status == 200
    except (URLError, OSError, TimeoutError):
        return False


def env_bool(name):
    value = os.getenv(name, "").strip().lower()
    return value in ("1", "true", "yes", "on")


def legacy_mode():
    return env_bool(LEGACY_ENV)


def launch_args():
    model = os.environ.get("SGLANG_MODEL_PATH", "")
    if not model or not (Path(model) / "config.json").is_file():
        raise ValueError("Mount a complete HF model directory (including config.json and "
                         "weights) at SGLANG_MODEL_PATH; see docker/sglang/README.md")
    extra = json.loads(os.getenv("SGLANG_EXTRA_ARGS", "[]"))
    if not isinstance(extra, list) or not all(isinstance(x, str) for x in extra):
        raise ValueError("SGLANG_EXTRA_ARGS must be a JSON array of strings")
    reserved = {"--model", "--model-path", "--host", "--port", "--served-model-name", "--api-key"}
    if any(x.split("=", 1)[0] in reserved for x in extra):
        raise ValueError("Use the dedicated environment variables for model, name and API key; "
                         "host/port are managed internally")
    args = [sys.executable, "-m", "sglang.launch_server", "--model-path", model,
            "--served-model-name", os.getenv("SGLANG_MODEL_NAME", "local-sglang"),
            "--host", "127.0.0.1", "--port", "30000"]
    if os.getenv("SGLANG_API_KEY"):
        args += ["--api-key", os.environ["SGLANG_API_KEY"]]
    return args + extra


def stop_requested(_signum, _frame):
    global STOPPING
    STOPPING = True


def cancel_install(signum, frame):
    stop_requested(signum, frame)
    raise InterruptedError("Startup installation cancelled")


def shutdown(children):
    for child in children:
        if child.poll() is None:
            signal_group(child, signal.SIGTERM)
    deadline = time.monotonic() + 20
    for child in children:
        try:
            child.wait(timeout=max(0.1, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            signal_group(child, signal.SIGKILL)
            child.wait()


def signal_group(child, sig):
    try:
        os.killpg(child.pid, sig)
    except ProcessLookupError:
        pass  # The process may exit between poll() and killpg().


def main():
    if sys.argv[1:] == ["health"]:
        if not healthy("http://127.0.0.1:11435/api/health"):
            return 1
        if legacy_mode() and not os.getenv("SGLANG_BASE_URL"):
            return 0 if healthy(base_url() + "/models", True) else 1
        return 0
    if len(sys.argv) > 1:
        raise ValueError("Configure SGLang with environment variables; no positional arguments")
    home = app_home()
    for name in ("tmp", "cache", "logs"):
        (home / name).mkdir(parents=True, exist_ok=True)
    for name in ("TMPDIR", "TMP", "TEMP"):
        os.environ[name] = str(home / "tmp")
    listen_addr = configure_cluster(home)
    legacy = legacy_mode()
    args = None
    if legacy:
        model = os.getenv("SGLANG_MODEL_NAME", "local-sglang").strip()
        if not model:
            raise ValueError("SGLANG_MODEL_NAME must not be empty")
        external = bool(os.getenv("SGLANG_BASE_URL"))
        args = None if external else launch_args()
    # This image serves through SGLang; startup installation must never pull
    # llama.cpp, even when inherited deployment variables enable it elsewhere.
    os.environ["CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER"] = "0"
    signal.signal(signal.SIGTERM, cancel_install)
    signal.signal(signal.SIGINT, cancel_install)
    try:
        binary = select_binary(home)
    except InterruptedError:
        return 0
    seed_runtime_cache(Path("/opt/csglite/runtime-cache"), home)
    if legacy:
        # A fixed-model server is a provider, not a per-model native runtime.
        # Disable native spawning so CSGLite cannot start a second SGLang.
        os.environ["CSGHUB_LITE_SGLANG_DISABLE"] = "1"
        configure_provider(home, base_url(), model)
    else:
        # Switching back to native must remove the launcher's own fixed-model
        # provider; otherwise it keeps pointing at a port nothing listens on.
        remove_provider(home)
    os.environ["PATH"] = str(home / "bin") + os.pathsep + os.environ.get("PATH", "")
    signal.signal(signal.SIGTERM, stop_requested)
    signal.signal(signal.SIGINT, stop_requested)
    children = []
    try:
        if args:
            children.append(subprocess.Popen(args, start_new_session=True))
        children.append(subprocess.Popen(
            [binary, "serve", "--listen", listen_addr], start_new_session=True))
        deadline = time.monotonic() + int(os.getenv("SGLANG_START_TIMEOUT", "1800"))
        ready = False
        if legacy and args:
            print("CSGLite starting on :11435; waiting for SGLang model readiness", flush=True)
        else:
            print("CSGLite starting on :11435; manage models through the UI or API", flush=True)
        while not STOPPING:
            for child in children:
                code = child.poll()
                if code is not None:
                    print("A managed service exited (code %s); stopping container" % code,
                          file=sys.stderr, flush=True)
                    return code if code > 0 else 1
            if not ready:
                ready = healthy("http://127.0.0.1:11435/api/health")
                if legacy and args and ready:
                    ready = healthy(base_url() + "/models", True)
                if ready:
                    print("CSGLite ready on :11435", flush=True)
                elif time.monotonic() > deadline:
                    print("CSGLite readiness timeout", file=sys.stderr, flush=True)
                    return 1
            time.sleep(1)
        return 0
    finally:
        shutdown(children)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError) as error:
        print("CSGLite + SGLang: %s" % error, file=sys.stderr)
        sys.exit(1)
