"""Build inputs must fail before a Go/Docker invocation can ship an empty UI."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class BuildInputTests(unittest.TestCase):
    def setUp(self):
        storage_tmp = Path.home() / ".csghub-lite" / "tmp"
        storage_tmp.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=str(storage_tmp))
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.script = self.root / "docker" / "sglang" / "build.sh"
        self.script.parent.mkdir(parents=True)
        shutil.copyfile(Path(__file__).with_name("build.sh"), self.script)
        self.env = dict(os.environ, CSGHUB_LITE_BUILD_ROOT=str(self.root / "tmp"),
                        CSGHUB_LITE_VERSION="")

    def run_build(self):
        return subprocess.run(["bash", str(self.script)], env=self.env,
                              capture_output=True, text=True, timeout=10)

    def test_source_archive_without_ui_fails_with_build_instruction(self):
        result = self.run_build()
        self.assertEqual(result.returncode, 66, result.stderr)
        self.assertIn("make build-web", result.stderr)

    def test_unversioned_bundled_binary_cannot_bypass_latest_protection(self):
        static = self.root / "internal" / "server" / "static"
        (static / "assets").mkdir(parents=True)
        (static / "index.html").write_text('<script src="/assets/main.js"></script>')
        (static / "assets" / "main.js").write_text("console.log('fixture')")
        for version in ("", "dev", "1.0.6;echo invalid", "1.0.6-01"):
            self.env["CSGHUB_LITE_VERSION"] = version
            result = self.run_build()
            self.assertEqual(result.returncode, 64, result.stderr)
            self.assertIn("semantic build version", result.stderr)


if __name__ == "__main__":
    unittest.main()
