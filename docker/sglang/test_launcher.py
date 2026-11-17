import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import launcher


class LauncherTests(unittest.TestCase):
    def setUp(self):
        # Even test staging stays inside the configured application's tree.
        root = Path.home() / ".csghub-lite" / "tmp"
        root.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=str(root))
        self.addCleanup(self.temporary.cleanup)
        self.home = Path(self.temporary.name)

    def test_provider_registration_preserves_other_providers_and_selections(self):
        launcher.write_json(self.home / "providers.json", [{"id": "existing"}])
        launcher.write_json(self.home / "provider_model_allowlist.json",
                            {"version": 1, "providers": {"existing": [{"model": "other"}]}})
        launcher.configure_provider(self.home, "http://127.0.0.1:30000/v1", "first")
        launcher.configure_provider(self.home, "http://127.0.0.1:30000/v1", "second")
        providers = json.loads((self.home / "providers.json").read_text())
        self.assertEqual(len(providers), 2)
        self.assertEqual(providers[0], {"id": "existing"})
        selected = json.loads((self.home / "provider_model_allowlist.json").read_text())
        self.assertEqual(selected["providers"]["existing"], [{"model": "other"}])
        self.assertEqual(selected["providers"][launcher.PROVIDER_ID][0]["original_model"], "second")
        self.assertEqual((self.home / "providers.json").stat().st_mode & 0o777, 0o600)

    def test_invalid_config_is_not_overwritten(self):
        path = self.home / "providers.json"
        path.write_text("broken json")
        with self.assertRaises(ValueError):
            launcher.configure_provider(self.home, "http://localhost/v1", "model")
        self.assertEqual(path.read_text(), "broken json")

    def test_model_validation_and_arguments(self):
        with patch.dict(os.environ, {"SGLANG_MODEL_PATH": str(self.home),
                                     "SGLANG_EXTRA_ARGS": "[]"}, clear=True):
            with self.assertRaises(ValueError):
                launcher.launch_args()
            (self.home / "config.json").write_text("{}")
            self.assertIn(str(self.home), launcher.launch_args())
            os.environ["SGLANG_EXTRA_ARGS"] = '["--port=11435"]'
            with self.assertRaises(ValueError):
                launcher.launch_args()
            os.environ["SGLANG_EXTRA_ARGS"] = '"--tp 2"'
            with self.assertRaises(ValueError):
                launcher.launch_args()

    def test_child_exit_stops_sibling(self):
        child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(120)"],
                                 start_new_session=True)
        launcher.shutdown([child])
        self.assertIsNotNone(child.poll())

    def test_supervisor_reports_service_failure(self):
        # No HTTP server is needed: the gateway command fails before readiness.
        binary = self.home / "csghub-lite"
        binary.write_text("#!/bin/sh\nexit 23\n")
        binary.chmod(0o755)
        env = dict(os.environ, HOME=str(self.home), PATH=str(self.home) + os.pathsep + os.environ["PATH"],
                   SGLANG_BASE_URL="http://127.0.0.1:1/v1", SGLANG_START_TIMEOUT="3")
        result = subprocess.run([sys.executable, str(Path(launcher.__file__))], env=env,
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 23, result.stderr.decode())

    def test_cluster_environment_reaches_csghub_lite(self):
        binary = self.home / "csghub-lite"
        captured = self.home / "captured.json"
        binary.write_text("#!%s\nimport os,json\nfrom pathlib import Path\n"
                          "Path(%r).write_text(json.dumps(dict(os.environ)))\n"
                          "raise SystemExit(23)\n" % (sys.executable, str(captured)))
        binary.chmod(0o755)
        cluster = {"CSGHUB_LITE_CLUSTER_SECRET": "test-only",
                   "CSGHUB_LITE_CLUSTER_JOIN_TOKEN": "test-token",
                   "CSGHUB_LITE_CLUSTER_NAME": "test-cluster",
                   "CSGHUB_LITE_CLUSTER_ADDR": ":11438",
                   "CSGHUB_LITE_CLUSTER_SEEDS": "192.0.2.1:11438",
                   "CSGHUB_LITE_CLUSTER_DISCOVERY": "none",
                   "CSGHUB_LITE_CLUSTER_ADVERTISE_HOST": "192.0.2.2",
                   "CSGHUB_LITE_CLUSTER_DISABLED": "0"}
        env = dict(os.environ, **cluster)
        env.update(HOME=str(self.home), PATH=str(self.home) + os.pathsep + os.environ["PATH"],
                   SGLANG_BASE_URL="http://127.0.0.1:1/v1", SGLANG_START_TIMEOUT="3")
        result = subprocess.run([sys.executable, str(Path(launcher.__file__))], env=env,
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 23, result.stderr.decode())
        actual = json.loads(captured.read_text())
        self.assertEqual({key: actual[key] for key in cluster}, cluster)


if __name__ == "__main__":
    unittest.main()
