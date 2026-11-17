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

    def test_public_worker_uses_existing_schema_loopback_and_preserves_settings(self):
        launcher.write_json(self.home / "config.json", {"cloud_provider_enabled": False,
                            "cluster": {"name": "preserved"}})
        env = {"CSGHUB_LITE_CLUSTER_MODE": "worker",
               "CSGHUB_LITE_CLUSTER_GATEWAY_URL": "https://gateway.example.com:8080",
               "CSGHUB_LITE_CLUSTER_TLS_CA_FILE": str(self.home / "gateway-ca.pem"),
               "CSGHUB_LITE_CLUSTER_JOIN_TOKEN": "test-token-never-persisted"}
        with patch.dict(os.environ, env, clear=True):
            self.assertEqual(launcher.configure_cluster(self.home), "127.0.0.1:11435")
        actual = json.loads((self.home / "config.json").read_text())
        self.assertFalse(actual["cloud_provider_enabled"])
        self.assertEqual(actual["cluster"]["name"], "preserved")
        self.assertEqual(actual["cluster"]["mode"], "worker")
        self.assertEqual(actual["cluster"]["gateway_url"], env["CSGHUB_LITE_CLUSTER_GATEWAY_URL"])
        self.assertNotIn("test-token-never-persisted", json.dumps(actual))
        # Restarting without provisioning env honours the persisted role.
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(launcher.configure_cluster(self.home), "127.0.0.1:11435")

    def test_public_mode_rejects_lan_secret_without_rewriting_config(self):
        original = {"cluster": {"mode": "lan", "name": "keep"}}
        launcher.write_json(self.home / "config.json", original)
        with patch.dict(os.environ, {"CSGHUB_LITE_CLUSTER_MODE": "worker",
                                     "CSGHUB_LITE_CLUSTER_SECRET": "test-only"}, clear=True):
            with self.assertRaisesRegex(ValueError, "unset the LAN"):
                launcher.configure_cluster(self.home)
        self.assertEqual(json.loads((self.home / "config.json").read_text()), original)

    def test_lan_and_standalone_keep_container_listener(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(launcher.configure_cluster(self.home), "0.0.0.0:11435")
        with patch.dict(os.environ, {"CSGHUB_LITE_CLUSTER_MODE": "lan"}, clear=True):
            self.assertEqual(launcher.configure_cluster(self.home), "0.0.0.0:11435")

    def test_worker_launcher_passes_loopback_and_join_env_to_lite(self):
        binary = self.home / "csghub-lite"
        captured = self.home / "worker-captured.json"
        binary.write_text("#!%s\nimport os,json,sys\nfrom pathlib import Path\n"
                          "Path(%r).write_text(json.dumps({'args':sys.argv[1:],'token':os.getenv('CSGHUB_LITE_CLUSTER_JOIN_TOKEN')}))\n"
                          "raise SystemExit(23)\n" % (sys.executable, str(captured)))
        binary.chmod(0o755)
        env = dict(os.environ, HOME=str(self.home), PATH=str(self.home)+os.pathsep+os.environ["PATH"],
                   CSGHUB_LITE_VERSION="bundled", CSGHUB_LITE_CLUSTER_MODE="worker",
                   CSGHUB_LITE_CLUSTER_GATEWAY_URL="https://gateway.example.com:8080",
                   CSGHUB_LITE_CLUSTER_JOIN_TOKEN="test-join", SGLANG_START_TIMEOUT="3")
        env.pop("CSGHUB_LITE_CLUSTER_SECRET", None)
        result = subprocess.run([sys.executable, str(Path(launcher.__file__))], env=env,
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 23, result.stderr.decode())
        actual = json.loads(captured.read_text())
        self.assertEqual(actual["args"], ["serve", "--listen", "127.0.0.1:11435"])
        self.assertEqual(actual["token"], "test-join")

    def test_bundled_kernels_preserve_user_cache_and_symlinks(self):
        bundle = self.home / "bundle"
        source = bundle / "huggingface"
        source.mkdir(parents=True)
        (source / "kernel").write_text("bundled kernel")
        (source / "existing").write_text("bundled old value")
        (source / "alias").symlink_to("kernel")
        target = self.home / "cache" / "huggingface"
        target.mkdir(parents=True)
        (target / "existing").write_text("user value")
        with patch.dict(os.environ, {"HF_HOME": str(target)}):
            launcher.seed_runtime_cache(bundle, self.home)
            launcher.seed_runtime_cache(bundle, self.home)
        self.assertEqual((target / "kernel").read_text(), "bundled kernel")
        self.assertEqual((target / "existing").read_text(), "user value")
        self.assertTrue((target / "alias").is_symlink())
        self.assertEqual((target / "alias").read_text(), "bundled kernel")

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

    def test_native_mode_starts_without_a_fixed_model(self):
        # The default is native managed mode: no SGLANG_MODEL_PATH and no
        # seeded provider, because CSGLite owns per-model SGLang processes.
        binary = self.home / "csghub-lite"
        binary.write_text("#!/bin/sh\nexit 23\n")
        binary.chmod(0o755)
        env = dict(os.environ, HOME=str(self.home), PATH=str(self.home) + os.pathsep + os.environ["PATH"],
                   CSGHUB_LITE_VERSION="bundled", SGLANG_START_TIMEOUT="3")
        env.pop("SGLANG_MODEL_PATH", None)
        env.pop("SGLANG_BASE_URL", None)
        env.pop(launcher.LEGACY_ENV, None)
        result = subprocess.run([sys.executable, str(Path(launcher.__file__))], env=env,
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 23, result.stderr.decode())
        self.assertFalse((self.home / ".csghub-lite" / "providers.json").exists())

    def test_legacy_mode_disables_native_spawning_and_seeds_provider(self):
        binary = self.home / "csghub-lite"
        captured = self.home / "captured.json"
        binary.write_text("#!%s\nimport os,json\nfrom pathlib import Path\n"
                          "Path(%r).write_text(json.dumps(dict(os.environ)))\n"
                          "raise SystemExit(23)\n" % (sys.executable, str(captured)))
        binary.chmod(0o755)
        model = self.home / "model"
        model.mkdir()
        (model / "config.json").write_text("{}")
        env = dict(os.environ, HOME=str(self.home), PATH=str(self.home) + os.pathsep + os.environ["PATH"],
                   CSGHUB_LITE_VERSION="bundled", SGLANG_START_TIMEOUT="3",
                   SGLANG_MODEL_PATH=str(model), SGLANG_MODEL_NAME="fixed",
                   SGLANG_BASE_URL="http://127.0.0.1:1/v1", **{launcher.LEGACY_ENV: "1"})
        result = subprocess.run([sys.executable, str(Path(launcher.__file__))], env=env,
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 23, result.stderr.decode())
        actual = json.loads(captured.read_text())
        self.assertEqual(actual["CSGHUB_LITE_SGLANG_DISABLE"], "1")
        providers = json.loads((self.home / ".csghub-lite" / "providers.json").read_text())
        self.assertEqual(providers[-1]["id"], launcher.PROVIDER_ID)

    def test_supervisor_reports_service_failure(self):
        # No HTTP server is needed: the CSGLite command fails before readiness.
        binary = self.home / "csghub-lite"
        binary.write_text("#!/bin/sh\nexit 23\n")
        binary.chmod(0o755)
        env = dict(os.environ, HOME=str(self.home), PATH=str(self.home) + os.pathsep + os.environ["PATH"],
                   CSGHUB_LITE_VERSION="bundled",
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
                   CSGHUB_LITE_VERSION="bundled",
                   CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER="1",
                   SGLANG_BASE_URL="http://127.0.0.1:1/v1", SGLANG_START_TIMEOUT="3")
        result = subprocess.run([sys.executable, str(Path(launcher.__file__))], env=env,
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 23, result.stderr.decode())
        actual = json.loads(captured.read_text())
        self.assertEqual({key: actual[key] for key in cluster}, cluster)
        self.assertEqual(actual["CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER"], "0")


if __name__ == "__main__":
    unittest.main()


class BuildScriptTests(unittest.TestCase):
    def test_build_fails_fast_without_synced_ui(self):
        # A source archive has no gitignored index.html/assets. The build must
        # stop before invoking Go or Docker and explain how to build the UI.
        root = Path.home() / ".csghub-lite" / "tmp"
        root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=str(root)) as temporary:
            repo = Path(temporary) / "repo"
            script = repo / "docker" / "sglang" / "build.sh"
            script.parent.mkdir(parents=True)
            script.write_bytes((Path(launcher.__file__).parent / "build.sh").read_bytes())
            script.chmod(0o755)
            (repo / "internal" / "server" / "static").mkdir(parents=True)
            result = subprocess.run(["bash", str(script)], capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 66, result.stderr.decode())
            self.assertIn("make build-web", result.stderr.decode())


class LauncherModeSwitchTests(unittest.TestCase):
    def setUp(self):
        root = Path.home() / ".csghub-lite" / "tmp"
        root.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=str(root))
        self.addCleanup(self.temporary.cleanup)
        self.home = Path(self.temporary.name)
        binary = self.home / "csghub-lite"
        binary.write_text("#!/bin/sh\nexit 23\n")
        binary.chmod(0o755)

    def run_launcher(self, legacy):
        env = dict(os.environ, HOME=str(self.home), PATH=str(self.home) + os.pathsep + os.environ["PATH"],
                   CSGHUB_LITE_VERSION="bundled", SGLANG_START_TIMEOUT="3")
        env.pop("SGLANG_MODEL_PATH", None)
        env.pop("SGLANG_BASE_URL", None)
        if legacy:
            model = self.home / "model"
            model.mkdir(exist_ok=True)
            (model / "config.json").write_text("{}")
            env.update(SGLANG_MODEL_PATH=str(model), SGLANG_MODEL_NAME="fixed",
                       SGLANG_BASE_URL="http://127.0.0.1:1/v1", **{launcher.LEGACY_ENV: "1"})
        else:
            env.pop(launcher.LEGACY_ENV, None)
        return subprocess.run([sys.executable, str(Path(launcher.__file__))], env=env,
                              capture_output=True, timeout=10)

    def test_native_removes_only_the_launcher_provider_and_legacy_readds_it(self):
        home = self.home / ".csghub-lite"
        home.mkdir(parents=True, exist_ok=True)
        launcher.write_json(home / "providers.json", [{"id": "user"}, {"id": launcher.PROVIDER_ID}])
        launcher.write_json(home / "provider_model_allowlist.json",
                            {"version": 1, "providers": {"user": [{"model": "keep"}], launcher.PROVIDER_ID: [{"model": "old"}]}})

        result = self.run_launcher(legacy=False)
        self.assertEqual(result.returncode, 23, result.stderr.decode())
        providers = json.loads((home / "providers.json").read_text())
        self.assertEqual([p["id"] for p in providers], ["user"])
        selections = json.loads((home / "provider_model_allowlist.json").read_text())
        self.assertEqual(selections["providers"]["user"], [{"model": "keep"}])
        self.assertNotIn(launcher.PROVIDER_ID, selections["providers"])

        result = self.run_launcher(legacy=True)
        self.assertEqual(result.returncode, 23, result.stderr.decode())
        providers = json.loads((home / "providers.json").read_text())
        self.assertEqual([p["id"] for p in providers], ["user", launcher.PROVIDER_ID])
        selections = json.loads((home / "provider_model_allowlist.json").read_text())
        self.assertIn(launcher.PROVIDER_ID, selections["providers"])
