from types import SimpleNamespace
from pathlib import Path
import tempfile
import unittest

from minimal_runtime import dependency_closure, prune_math_archives


class DependencyClosureTests(unittest.TestCase):
    def test_static_math_reduction_keeps_jit_runtime_and_shared_libraries(self):
        root = Path.home() / ".csghub-lite" / "tmp"
        root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=str(root)) as directory:
            libs = Path(directory)
            files = ["libcublas_static.a", "libcudnn_static.a", "libcublas.so.12",
                     "libcudart_static.a", "libcudadevrt.a", "libnvrtc_static.a", "cuda.h"]
            for name in files:
                (libs / name).write_bytes(b"test")
            removed = prune_math_archives([libs, libs])
            self.assertEqual(sum(removed.values()), 8)
            for name in files[2:]:
                self.assertTrue((libs / name).is_file(), name)

    def test_default_runtime_excludes_diffusion_and_dev_extras(self):
        installed = {
            "sglang": SimpleNamespace(requires=["torch", "sglang[diffusion]; extra == 'all'",
                                               "diffusers; extra == 'diffusion'", "pytest; extra == 'dev'"]),
            "torch": SimpleNamespace(requires=["nvidia-cublas-cu12"]),
            "nvidia-cublas-cu12": SimpleNamespace(requires=[]),
            "diffusers": SimpleNamespace(requires=[]),
            "pytest": SimpleNamespace(requires=[]),
        }
        self.assertEqual(dependency_closure(installed, ["sglang"]),
                         {"sglang", "torch", "nvidia-cublas-cu12"})

    def test_extras_propagate_and_cycles_terminate(self):
        installed = {
            "sglang": SimpleNamespace(requires=["flashinfer-python[cu12]"]),
            "flashinfer-python": SimpleNamespace(requires=["cuda12; extra == 'cu12'",
                                                           "cuda13; extra == 'cu13'", "sglang"]),
            "cuda12": SimpleNamespace(requires=[]),
            "cuda13": SimpleNamespace(requires=[]),
        }
        self.assertEqual(dependency_closure(installed, ["sglang"]),
                         {"sglang", "flashinfer-python", "cuda12"})

    def test_unavailable_markers_are_not_retained(self):
        installed = {"sglang": SimpleNamespace(requires=["future; python_version >= '999'"]),
                     "future": SimpleNamespace(requires=[])}
        self.assertEqual(dependency_closure(installed, ["sglang"]), {"sglang"})


if __name__ == "__main__":
    unittest.main()
