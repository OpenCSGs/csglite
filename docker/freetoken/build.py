"""Build the FreeToken Linux CUDA benchmark image with Docker Buildx."""

import argparse
import hashlib
import shutil
import subprocess
import tempfile
from pathlib import Path
from urllib.parse import quote
from urllib.request import urlopen

VERSION = "0.2.2"
TAG = f"freetoken-v{VERSION}-cu130-a800"
RELEASE = f"https://github.com/FlashML-org/FreeToken/releases/download/v{VERSION}"
WHEELS = {
    f"freetoken-{VERSION}-cp312-cp312-manylinux_2_27_x86_64.whl":
        "e8e735cd0929110c131617f5585e61fe609c4c4e081537bd063428c02b1f4130",
    f"freetoken_kernel_cache-{VERSION}+cu130-py3-none-linux_x86_64.whl":
        "a4c6419c5aa3382710d5eab6f04fc40f7f01bf522f5b1d498b437f8b2ac923a1",
}


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def wheel_path(cache, name, expected_digest):
    path = cache / name
    if path.is_file() and sha256(path) == expected_digest:
        return path
    print(f"Downloading {name}", flush=True)
    with tempfile.TemporaryDirectory(dir=cache) as directory:
        downloaded = Path(directory) / name
        with urlopen(f"{RELEASE}/{quote(name)}", timeout=90) as response:
            with downloaded.open("wb") as stream:
                shutil.copyfileobj(response, stream)
        if sha256(downloaded) != expected_digest:
            raise ValueError(f"SHA-256 mismatch for {name}")
        downloaded.replace(path)
    return path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", default=f"csglite:{TAG}")
    args = parser.parse_args()

    source = Path(__file__).resolve().parent
    work = Path.home() / ".csghub-lite" / "tmp" / TAG
    wheels = work / "wheels"
    wheels.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=work, prefix="build-context-") as directory:
        context = Path(directory)
        for name in ("Dockerfile", "entrypoint.sh", ".dockerignore"):
            shutil.copy2(source / name, context / name)
        (context / "wheels").mkdir()
        for name, digest in WHEELS.items():
            shutil.copy2(wheel_path(wheels, name, digest), context / "wheels" / name)
        subprocess.run(
            ["docker", "buildx", "build", "--platform", "linux/amd64",
             "--target", "precompiled", "--tag", args.image, "--load", str(context)],
            check=True,
        )


if __name__ == "__main__":
    main()
