"""Use the public installer for Lite only; leave the SGLang/CUDA stack fixed."""
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time


INSTALL_URL = "https://hub.opencsg.com/csghub-lite/install.sh"
RELEASE = re.compile(r"v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)\Z")


def binary_version(binary):
    try:
        result = subprocess.run([str(binary), "--version"], capture_output=True,
                                text=True, timeout=10, check=True)
        return result.stdout.strip().split()[-1].removeprefix("v")
    except (OSError, subprocess.SubprocessError, IndexError):
        return None


def replace_binary(source, target):
    # Staging and replacement share a filesystem, including custom bin mounts.
    candidate = None
    try:
        with tempfile.NamedTemporaryFile(dir=str(target.parent), prefix=".lite-", delete=False) as output:
            candidate = Path(output.name)
            with Path(source).open("rb") as original:
                shutil.copyfileobj(original, output)
        candidate.chmod(0o755)
        os.replace(str(candidate), str(target))
    finally:
        if candidate and candidate.exists():
            candidate.unlink()


def run_installer_command(args, env, timeout):
    process = subprocess.Popen(args, env=env, stdin=subprocess.DEVNULL, start_new_session=True)
    try:
        code = process.wait(timeout=timeout)
        if code:
            raise subprocess.CalledProcessError(code, args)
    finally:
        # Older public installers start a background server. Reap that private
        # process group before the supervisor starts its own foreground server.
        for sig in (signal.SIGTERM, signal.SIGKILL):
            try:
                os.killpg(process.pid, sig)
            except ProcessLookupError:
                break
            if sig == signal.SIGTERM:
                deadline = time.monotonic() + 3
                while time.monotonic() < deadline:
                    process.poll()
                    try:
                        os.killpg(process.pid, 0)
                    except ProcessLookupError:
                        break
                    time.sleep(0.1)
        process.wait()


def install_release(home, bundled, requested, target):
    with tempfile.TemporaryDirectory(prefix="lite-install-", dir=str(home / "tmp")) as temporary:
        staging = Path(temporary)
        stage_bin = staging / "bin"
        stage_bin.mkdir()
        stage_tmp = staging / "tmp"
        stage_tmp.mkdir()
        candidate = stage_bin / "csghub-lite"
        # The installer cleans up the previous PATH executable. Give it its own
        # disposable copy, so a failed install cannot remove the cached/fallback.
        shutil.copy2(bundled, candidate)
        env = dict(os.environ)
        env.update(INSTALL_DIR=str(stage_bin), PATH=str(stage_bin) + os.pathsep + env.get("PATH", ""),
                   TMPDIR=str(stage_tmp), TMP=str(stage_tmp), TEMP=str(stage_tmp),
                   CSGHUB_LITE_FORCE="1", CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER="0",
                   CSGHUB_LITE_REQUIRE_LLAMA_SERVER="0", CSGHUB_LITE_CLUSTER_DISABLED="1")
        # Only the real, supervised Lite process receives cluster credentials.
        for key in list(env):
            if key.startswith("CSGHUB_LITE_CLUSTER_") and key != "CSGHUB_LITE_CLUSTER_DISABLED":
                env.pop(key)
        if requested == "latest":
            env.pop("CSGHUB_LITE_VERSION", None)  # Empty means latest to install.sh.
        else:
            env["CSGHUB_LITE_VERSION"] = "v" + requested
        bypass = env.get("no_proxy", env.get("NO_PROXY", ""))
        env["no_proxy"] = env["NO_PROXY"] = bypass + ",git-devops.opencsg.com"
        installer = staging / "install.sh"
        print("CSGLite: running official installer for %s (llama.cpp disabled)" % requested, flush=True)
        run_installer_command(["curl", "-fsSL", "--connect-timeout", "10", "--max-time", "30",
                               os.getenv("CSGHUB_LITE_INSTALL_URL", INSTALL_URL), "-o", str(installer)], env, 35)
        run_installer_command(["sh", str(installer)], env, 180)
        installed = binary_version(candidate)
        if not installed or not RELEASE.fullmatch(installed):
            raise ValueError("Installer did not provide a release executable")
        if requested != "latest" and installed != requested:
            raise ValueError("Installed Lite version does not match v" + requested)
        replace_binary(candidate, target)
        print("CSGLite: using v%s from %s" % (installed, target), flush=True)


def select_binary(home):
    requested = os.getenv("CSGHUB_LITE_VERSION", "latest").strip() or "latest"
    if requested not in ("latest", "bundled"):
        match = RELEASE.fullmatch(requested)
        if not match:
            raise ValueError("CSGHUB_LITE_VERSION must be latest, bundled, or a release such as v0.8.55")
        requested = match.group(1)
    managed = home / "bin"
    # Ignore the persisted executable while locating the image's fallback.
    search = os.pathsep.join(p for p in os.environ.get("PATH", "").split(os.pathsep)
                             if os.path.abspath(p) != str(managed.absolute()))
    bundled = shutil.which("csghub-lite", path=search)
    if not bundled:
        raise OSError("The image's bundled CSGLite executable was not found")
    managed.mkdir(parents=True, exist_ok=True)
    (home / "tmp").mkdir(parents=True, exist_ok=True)
    target = managed / "csghub-lite"
    if requested == "bundled":
        replace_binary(bundled, target)
        return str(target)
    current = binary_version(target)
    if requested != "latest" and current == requested:
        return str(target)
    try:
        install_release(home, bundled, requested, target)
    except InterruptedError:
        raise
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        if requested != "latest":
            raise OSError("Cannot install requested Lite v%s (%s)" % (requested, type(error).__name__)) from error
        print("CSGLite: startup update failed (%s); using %s" % (
            type(error).__name__, "cached Lite" if current else "bundled Lite"), flush=True)
        if not current:
            replace_binary(bundled, target)
    return str(target)
