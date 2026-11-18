"""Build-time text-runtime reduction; never invoked during container startup."""
import argparse
from importlib import metadata
import json
from pathlib import Path
import shutil
import subprocess
import sys

from packaging.requirements import Requirement
from packaging.utils import canonicalize_name

# Keep the precompiled GPU kernels even when they are optional dependencies.
# NVCC, GCC, Ninja, headers and CUDA libraries also stay: text inference can JIT.
ROOTS = ("sglang", "pip", "setuptools", "wheel", "packaging", "ninja",
         "flashinfer-cubin", "flashinfer-jit-cache")


def dependency_closure(distributions, roots):
    """Resolve installed Requires-Dist, including propagated extras and markers."""
    keep = set()
    visited = set()
    pending = [(canonicalize_name(name), frozenset()) for name in roots]
    while pending:
        name, extras = pending.pop()
        key = (name, extras)
        if key in visited or name not in distributions:
            continue
        visited.add(key)
        keep.add(name)
        for value in distributions[name].requires or ():
            requirement = Requirement(value)
            if requirement.marker and not any(
                    requirement.marker.evaluate({"extra": extra}) for extra in ({""} | set(extras))):
                continue
            pending.append((canonicalize_name(requirement.name), frozenset(requirement.extras)))
    return keep


def prune_math_archives(roots):
    # PyTorch/SGLang load the shared math libraries. Do not remove the device
    # runtime, CUDA compiler archives, headers, or any shared library used by JIT.
    prefixes = ("libcublas", "libcudnn", "libcufft", "libcusolver", "libcusparse",
                "libcurand", "libnpp", "libnvjpeg")
    removed = {}
    for root in roots:
        for path in root.glob("*.a"):
            if path.name.startswith(prefixes) and path.is_file():
                actual = path.resolve()
                removed[str(actual)] = actual.stat().st_size
                actual.unlink()
    return removed


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    if args.apply and Path(sys.prefix) != Path("/opt/sglang"):
        raise SystemExit("Refusing to prune outside the container's /opt/sglang environment")
    installed = {canonicalize_name(d.metadata["Name"]): d for d in metadata.distributions()}
    if "sglang" not in installed:
        raise SystemExit("SGLang must already be installed")
    keep = dependency_closure(installed, ROOTS)
    remove = sorted(set(installed) - keep)
    report = {"removed": remove,
              "retained": {name: installed[name].version for name in sorted(keep)}}
    print(json.dumps(report, indent=2), flush=True)
    if not args.apply:
        return
    if remove:
        subprocess.run([sys.executable, "-m", "pip", "uninstall", "-y"] + remove, check=True)
    math_roots = [Path("/usr/lib/x86_64-linux-gnu")]
    math_roots.extend(Path("/usr/local").glob("cuda*/targets/*/lib"))
    report["removed_static_math_archives"] = prune_math_archives(math_roots)
    print("Removed static math archive bytes:", sum(report["removed_static_math_archives"].values()), flush=True)
    # Keep runtime code and licenses; drop source-control and documentation assets.
    workspace = Path("/sgl-workspace/sglang")
    for name in (".git", "docs", "test", "tests", "benchmark", "examples", "assets"):
        shutil.rmtree(workspace / name, ignore_errors=True)
    for path in ("/root/.cache/pip", "/root/.cargo", "/root/.rustup",
                 "/var/lib/apt/lists", "/var/cache/apt/archives", "/usr/share/man", "/usr/share/info"):
        shutil.rmtree(path, ignore_errors=True)
    # Relocate upstream AOT kernel caches out of HOME before it is volume-mounted.
    bundle = Path("/opt/csglite/runtime-cache")
    bundle.mkdir(parents=True, exist_ok=True)
    for name in ("huggingface", "sglang"):
        source = Path("/root/.cache") / name
        if source.exists():
            shutil.move(str(source), str(bundle / name))
    (bundle.parent / "runtime-packages.json").write_text(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
