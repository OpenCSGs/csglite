package imagegen

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/logutil"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestResolveImageBackend(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\nlibrary_name: mlx\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"QwenImage21Pipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qwen21RuntimeEnv, "auto")
	got, err := ResolveImageBackend(dir, "mlx-community/Qwen-Image-2.1-MLX-4bit")
	if err != nil {
		t.Fatal(err)
	}
	if got != "mlx-qwen-image-2.1" {
		t.Fatalf("ResolveImageBackend() = %q, want mlx-qwen-image-2.1", got)
	}

	t.Setenv(qwen21RuntimeEnv, "diffusers")
	if _, err := ResolveImageBackend(dir, "mlx-community/Qwen-Image-2.1-MLX-4bit"); err == nil {
		t.Fatal("ResolveImageBackend() with diffusers override succeeded for MLX weights")
	}
}

func TestResolveImageBackendRejectsOtherMLXImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\nlibrary_name: mlx\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"FluxPipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qwen21RuntimeEnv, "diffusers")
	if _, err := ResolveImageBackend(dir, "mlx-community/FLUX.1-schnell-4bit"); err == nil {
		t.Fatal("ResolveImageBackend() accepted an MLX image checkpoint that is not Qwen-Image-2.1")
	}
}

func TestResolveImageBackendRejectsInvalidOverride(t *testing.T) {
	t.Setenv(qwen21RuntimeEnv, "invalid")
	if _, err := ResolveImageBackend(t.TempDir(), "owner/model"); err == nil {
		t.Fatal("ResolveImageBackend() accepted invalid override")
	}
}

func TestDiffusersGenerateReportsWorkerCrash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	port, err := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}

	logBuf := logutil.NewTailWriter(64 * 1024)
	_, _ = logBuf.Write([]byte(" 0%|  | 0/40\r 2%|  | 1/40\nlibc++abi: terminating due to uncaught exception of type std::runtime_error: There is no Stream(gpu, 1) in current thread.\n"))
	exitCh := make(chan error, 1)
	exitCh <- errors.New("signal: abort trap")
	close(exitCh)
	engine := &DiffusersEngine{port: port, client: server.Client(), exitCh: exitCh, logBuf: logBuf}

	_, err = engine.Generate(context.Background(), api.OpenAIImagesGenerationRequest{Prompt: "apple"})
	if err == nil {
		t.Fatal("Generate() succeeded against a crashed worker")
	}
	for _, want := range []string{"exited during generation", "signal: abort trap", "There is no Stream(gpu, 1)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Generate() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestResolveImageBackendRejectsGGUFImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\nlibrary_name: gguf\npipeline_tag: text-to-image\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qwen-image-2.1-turbo-Q4_K_M.gguf"), []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveImageBackend(dir, "unsloth/Qwen-Image-2.1-Turbo-GGUF")
	if err == nil || !strings.Contains(err.Error(), "GGUF image model") {
		t.Fatalf("ResolveImageBackend() error = %v, want GGUF image model refusal", err)
	}
}

func TestDiffusersUpgradeHint(t *testing.T) {
	for _, output := range []string{
		"AttributeError: module diffusers has no attribute QwenImage21Pipeline",
		"AttributeError: module 'diffusers' has no attribute 'QwenImage21Pipeline'. Did you mean: 'QwenImagePipeline'?",
		// The worker log attached to issue #350, verbatim: the real error is
		// the unquoted form and carries a "Did you mean" suggestion that must
		// not be captured instead of the missing class.
		"Traceback (most recent call last):\n" +
			"  File \"/root/.csghub-lite/ai-runtime/diffusers_worker.py\", line 558, in <module>\n" +
			"    main()\n" +
			"  File \"/root/.csghub-lite/ai-runtime/diffusers_worker.py\", line 551, in main\n" +
			"    worker.load()\n" +
			"  File \"/root/.csghub-lite/ai-runtime/diffusers_worker.py\", line 336, in load\n" +
			"    self.pipeline = DiffusionPipeline.from_pretrained(self.model_dir, **kwargs)\n" +
			"  File \"/root/.csghub-lite/ai-runtime/venv/lib/python3.11/site-packages/huggingface_hub/utils/_validators.py\", line 114, in _inner_fn\n" +
			"    return fn(*args, **kwargs)\n" +
			"  File \"/root/.csghub-lite/ai-runtime/venv/lib/python3.11/site-packages/diffusers/pipelines/pipeline_utils.py\", line 926, in from_pretrained\n" +
			"    pipeline_class = _get_pipeline_class(\n" +
			"  File \"/root/.csghub-lite/ai-runtime/venv/lib/python3.11/site-packages/diffusers/pipelines/pipeline_loading_utils.py\", line 541, in _get_pipeline_class\n" +
			"    pipeline_cls = getattr(diffusers_module, class_name)\n" +
			"  File \"/root/.csghub-lite/ai-runtime/venv/lib/python3.11/site-packages/diffusers/utils/import_utils.py\", line 1045, in __getattr__\n" +
			"    raise AttributeError(f\"module {self.__name__} has no attribute {name}\")\n" +
			"AttributeError: module diffusers has no attribute QwenImage21Pipeline. Did you mean: 'QwenImagePipeline'?",
	} {
		hint := diffusersUpgradeHint(output)
		if !strings.Contains(hint, "csghub-lite runtime upgrade --pipeline QwenImage21Pipeline") {
			t.Fatalf("diffusersUpgradeHint(%q) = %q, want the upgrade command with the pipeline class", output, hint)
		}
	}
	for _, output := range []string{
		"",
		"ImportError: No module named 'diffusers'",
		"AttributeError: module 'transformers' has no attribute 'QwenImagePipeline'",
		"AttributeError: module 'diffusers' has no attribute 'QwenImagePipelineHelper'",
		"Traceback (most recent call last):\n" +
			"  File \"/venv/lib/python3.11/site-packages/diffusers/pipelines/qwenimage/pipeline_qwen_image.py\", line 12, in __call__\n" +
			"AttributeError: 'QwenImagePipeline' object has no attribute 'enable_vae_tiling'",
		"Traceback (most recent call last):\n" +
			"  File \"/venv/lib/python3.11/site-packages/diffusers/pipelines/qwenimage/pipeline_qwen_image.py\", line 12, in load\n" +
			"AttributeError: module 'diffusers' has no attribute 'some_helper'",
	} {
		if hint := diffusersUpgradeHint(output); hint != "" {
			t.Fatalf("diffusersUpgradeHint(%q) = %q, want no hint", output, hint)
		}
	}
}

func TestWorkerStartErrorAddsDiffusersUpgradeHint(t *testing.T) {
	logBuf := logutil.NewTailWriter(64 * 1024)
	_, _ = logBuf.Write([]byte("Traceback (most recent call last):\n" +
		"  File \"diffusers_worker.py\", line 336, in load\n" +
		"AttributeError: module 'diffusers' has no attribute 'QwenImage21Pipeline'. Did you mean: 'QwenImagePipeline'?\n"))
	engine := &DiffusersEngine{logBuf: logBuf}
	msg := engine.workerStartError("Diffusers worker exited before becoming ready: exit status 1")
	if !strings.Contains(msg, "runtime upgrade --pipeline QwenImage21Pipeline") {
		t.Fatalf("workerStartError() missing the upgrade hint:\n%s", msg)
	}

	plain := logutil.NewTailWriter(64 * 1024)
	_, _ = plain.Write([]byte("ImportError: No module named 'diffusers'\n"))
	engine = &DiffusersEngine{logBuf: plain}
	msg = engine.workerStartError("Diffusers worker exited before becoming ready: exit status 1")
	if strings.Contains(msg, "csghub-lite runtime upgrade") {
		t.Fatalf("workerStartError() added the upgrade hint to an unrelated error:\n%s", msg)
	}
}
