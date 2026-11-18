import os
from pathlib import Path
import tempfile
import subprocess
import sys
import time
import unittest
from unittest.mock import patch

import lite_update


class LiteUpdateTests(unittest.TestCase):
    def setUp(self):
        root = Path.home() / ".csghub-lite" / "tmp"
        root.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=str(root))
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.home = self.root / "data"
        self.bundled = self.root / "bundled" / "csghub-lite"
        self.cached = self.home / "bin" / "csghub-lite"
        self.binary(self.bundled, "bundled-build")
        env = dict(PATH=str(self.bundled.parent) + os.pathsep + os.environ["PATH"],
                   CSGHUB_LITE_VERSION="latest")
        self.environment = patch.dict(os.environ, env)
        self.environment.start()
        self.addCleanup(self.environment.stop)

    def binary(self, path, version):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('#!/bin/sh\necho "csghub-lite version %s"\n' % version)
        path.chmod(0o755)

    def test_matching_pin_starts_offline_and_is_not_a_substring_match(self):
        self.binary(self.cached, "1.0.5")
        with patch.dict(os.environ, CSGHUB_LITE_VERSION="v1.0.5"), patch.object(lite_update, "install_release") as install:
            self.assertEqual(lite_update.select_binary(self.home), str(self.cached))
            install.assert_not_called()
        with patch.dict(os.environ, CSGHUB_LITE_VERSION="v1.0.50"), patch.object(lite_update, "install_release", side_effect=OSError("offline")):
            with self.assertRaises(OSError):
                lite_update.select_binary(self.home)
        self.assertEqual(lite_update.binary_version(self.cached), "1.0.5")

    def test_latest_failure_preserves_cached_or_seeds_bundled(self):
        with patch.object(lite_update, "install_release", side_effect=OSError("offline")):
            self.assertEqual(lite_update.select_binary(self.home), str(self.cached))
            self.assertEqual(self.cached.read_bytes(), self.bundled.read_bytes())
            self.binary(self.cached, "1.0.5")
            lite_update.select_binary(self.home)
            self.assertEqual(lite_update.binary_version(self.cached), "1.0.5")

    def test_bundled_mode_ignores_cached_release_without_network(self):
        self.binary(self.cached, "1.0.5")
        with patch.dict(os.environ, CSGHUB_LITE_VERSION="bundled"), patch.object(lite_update, "install_release") as install:
            lite_update.select_binary(self.home)
            install.assert_not_called()
        self.assertEqual(self.cached.read_bytes(), self.bundled.read_bytes())

    def test_invalid_pin_fails_before_install(self):
        with patch.dict(os.environ, CSGHUB_LITE_VERSION="v1.0.5;echo bad"), patch.object(lite_update, "install_release") as install:
            with self.assertRaises(ValueError):
                lite_update.select_binary(self.home)
            install.assert_not_called()

    def test_shutdown_during_install_cancels_children_and_cleans_staging(self):
        installer = self.root / "slow-install.sh"
        started = self.root / "started"
        installer.write_text('#!/bin/sh\ntouch "$TEST_STARTED"\nsleep 120\n')
        env = dict(os.environ, HOME=str(self.root), SGLANG_BASE_URL="http://127.0.0.1:1/v1",
                   CSGHUB_LITE_VERSION="latest", CSGHUB_LITE_INSTALL_URL=installer.as_uri(),
                   TEST_STARTED=str(started))
        process = subprocess.Popen([sys.executable, str(Path(__file__).with_name("launcher.py"))],
                                   env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 10
            while not started.exists() and process.poll() is None and time.monotonic() < deadline:
                time.sleep(0.05)
            self.assertTrue(started.exists())
            process.terminate()
            output, _ = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 0, output.decode())
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
        self.assertEqual(list((self.root / ".csghub-lite" / "tmp").iterdir()), [])

    def test_official_installer_contract_persistence_and_child_cleanup(self):
        # Use a local installer fixture through real curl/sh, without downloads.
        installer = self.root / "install.sh"
        installer.write_text('''#!/bin/sh
set -eu
old=$(command -v csghub-lite)
[ "$old" = "$INSTALL_DIR/csghub-lite" ]
printf '%s\\n' "$CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER" "$CSGHUB_LITE_REQUIRE_LLAMA_SERVER" "${CSGHUB_LITE_VERSION:-latest}" "$INSTALL_DIR" "$TMPDIR" "${CSGHUB_LITE_CLUSTER_SECRET:-absent}" > "$TEST_CAPTURE"
version="${TEST_BAD_VERSION:-${CSGHUB_LITE_VERSION:-v1.0.5}}"
printf '#!/bin/sh\\necho "csghub-lite version %s"\\n' "$version" > "$INSTALL_DIR/csghub-lite"
chmod +x "$INSTALL_DIR/csghub-lite"
( trap 'echo stopped > "$TEST_STOPPED"; exit 0' TERM; while :; do sleep 1; done ) &
sleep 0.1
''')
        capture = self.root / "capture"
        stopped = self.root / "stopped"
        with patch.dict(os.environ, CSGHUB_LITE_INSTALL_URL=installer.as_uri(),
                        CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER="1",
                        CSGHUB_LITE_CLUSTER_SECRET="test-secret", TEST_CAPTURE=str(capture), TEST_STOPPED=str(stopped)):
            self.assertEqual(lite_update.select_binary(self.home), str(self.cached))
            fields = capture.read_text().splitlines()
            self.assertEqual(fields[:3], ["0", "0", "latest"])
            self.assertEqual(fields[5], "absent")
            self.assertTrue(Path(fields[3]).is_relative_to(self.home / "tmp"))
            self.assertTrue(Path(fields[4]).is_relative_to(self.home / "tmp"))
            self.assertTrue(stopped.exists(), "installer background server must be stopped")
            self.assertEqual(lite_update.binary_version(self.cached), "1.0.5")
            self.assertTrue(self.bundled.exists())
            # A recreated container reuses the same persistent, pinned binary.
            with patch.dict(os.environ, CSGHUB_LITE_VERSION="v1.0.5"), patch.object(lite_update, "install_release") as install:
                lite_update.select_binary(self.home)
                install.assert_not_called()
            # A broken/mismatched installer cannot replace the working cache.
            with patch.dict(os.environ, CSGHUB_LITE_VERSION="v1.0.6", TEST_BAD_VERSION="v9.9.9"):
                with self.assertRaises(OSError):
                    lite_update.select_binary(self.home)
                self.assertEqual(lite_update.binary_version(self.cached), "1.0.5")
            with patch.dict(os.environ, CSGHUB_LITE_VERSION="v1.0.6"):
                lite_update.select_binary(self.home)
                self.assertEqual(capture.read_text().splitlines()[2], "v1.0.6")
                self.assertEqual(lite_update.binary_version(self.cached), "1.0.6")
        self.assertEqual(list((self.home / "tmp").iterdir()), [])


class LatestNoDowngradeTests(unittest.TestCase):
    def setUp(self):
        root = Path.home() / ".csghub-lite" / "tmp"
        root.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=str(root))
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.home = self.root / "data"
        self.bundled = self.root / "bundled" / "csghub-lite"
        self.cached = self.home / "bin" / "csghub-lite"
        self.binary(self.bundled, "bundled-build")
        env = dict(PATH=str(self.bundled.parent) + os.pathsep + os.environ["PATH"],
                   CSGHUB_LITE_VERSION="latest")
        self.environment = patch.dict(os.environ, env)
        self.environment.start()
        self.addCleanup(self.environment.stop)

    def binary(self, path, version):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('#!/bin/sh\necho "csghub-lite version %s"\n' % version)
        path.chmod(0o755)

    def fake_latest(self, version):
        def install(_home, _bundled, _requested, target):
            self.binary(target, version)
        return patch.object(lite_update, "install_release", side_effect=install)

    def test_latest_does_not_replace_newer_bundled_prerelease(self):
        self.binary(self.bundled, "1.0.6-sglang.qa1")
        with self.fake_latest("1.0.5"):
            lite_update.select_binary(self.home)
        self.assertEqual(lite_update.binary_version(self.cached), "1.0.6-sglang.qa1")

    def test_latest_does_not_replace_newer_cached_release(self):
        self.binary(self.cached, "1.0.7")
        self.binary(self.bundled, "1.0.6")
        with self.fake_latest("1.0.6"):
            lite_update.select_binary(self.home)
        self.assertEqual(lite_update.binary_version(self.cached), "1.0.7")

    def test_release_replaces_same_core_prerelease(self):
        self.binary(self.cached, "1.0.6-pre")
        self.binary(self.bundled, "1.0.6-pre")
        with self.fake_latest("1.0.6"):
            lite_update.select_binary(self.home)
        self.assertEqual(lite_update.binary_version(self.cached), "1.0.6")

    def test_explicit_pin_still_allows_downgrade(self):
        self.binary(self.cached, "1.0.6")
        self.binary(self.bundled, "1.0.6")
        def install(_home, _bundled, _requested, target):
            self.binary(target, "1.0.5")
        with patch.object(lite_update, "install_release", side_effect=install), \
                patch.dict(os.environ, CSGHUB_LITE_VERSION="v1.0.5"):
            lite_update.select_binary(self.home)
        self.assertEqual(lite_update.binary_version(self.cached), "1.0.5")

    def test_bundled_mode_remains_an_explicit_offline_recovery(self):
        self.binary(self.cached, "1.0.7")
        self.binary(self.bundled, "1.0.6")
        with patch.dict(os.environ, CSGHUB_LITE_VERSION="bundled"):
            lite_update.select_binary(self.home)
        self.assertEqual(lite_update.binary_version(self.cached), "1.0.6")

    def test_semver_precedence_and_invalid_versions(self):
        versions = ["1.0.6-alpha", "1.0.6-alpha.1", "1.0.6-alpha.beta",
                    "1.0.6-beta", "1.0.6-beta.2", "1.0.6-beta.11",
                    "1.0.6-rc.1", "1.0.6", "1.0.7-alpha"]
        keys = [lite_update.semver_key(version) for version in versions]
        self.assertEqual(keys, sorted(keys))
        self.assertEqual(lite_update.semver_key("1.0.6+build.1"),
                         lite_update.semver_key("1.0.6+build.2"))
        for version in ("dev", "01.0.6", "1.0.6-01", "1.0.6-", "1.0.6-a..b"):
            self.assertIsNone(lite_update.semver_key(version), version)


if __name__ == "__main__":
    unittest.main()
