package convert

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CSGHUB_LITE_CONVERTER_URL, if set, is the raw URL of convert_hf_to_gguf.py to download
// once per URL (e.g. GitLab mirror). When unset, the copy embedded in the binary is used
// (no GitHub access required at runtime).

const (
	pythonPackageIndexURL         = "https://mirrors.aliyun.com/pypi/simple"
	pythonPackageIndexArgs        = "--index-url " + pythonPackageIndexURL
	pythonCPUOnlyTorchIndexURL    = "https://mirrors.aliyun.com/pytorch-wheels/cpu"
	pythonCPUOnlyTorchFallbackURL = "https://download.pytorch.org/whl/cpu"
	pythonCPUOnlyTorchInstallArgs = pythonPackageIndexArgs + " --find-links " + pythonCPUOnlyTorchIndexURL + " torch"
	pythonDepsInstallArgs         = "safetensors transformers sentencepiece protobuf"
	minPythonMajor                = 3
	minPythonMinor                = 9
)

type converterRepairResult struct {
	attempted bool
	succeeded bool
	note      string
}

type converterRepairPlan struct {
	installBundledGGUFPy bool
	upgradePackages      []string
}

func pythonInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "  brew install python"
	case "windows":
		return "  winget install -e --id Python.Python.3.12\n" +
			"  If `winget` is unavailable, download Python from https://python.org and enable `Add Python to PATH` during setup."
	default:
		return "  sudo apt update && sudo apt install -y python3 python3-pip python3-venv    # Debian / Ubuntu\n" +
			"  sudo dnf install -y python3 python3-pip                                    # Fedora / RHEL / Rocky"
	}
}

func pythonDepsInstallHint() string {
	return pythonDepsInstallHintForGOOS(runtime.GOOS)
}

func pythonDepsInstallHintForGOOS(goos string) string {
	if goos == "windows" {
		venvDir := `"%USERPROFILE%\.csghub-lite\tools\python"`
		venvPython := `"%USERPROFILE%\.csghub-lite\tools\python\Scripts\python.exe"`
		return fmt.Sprintf(
			"  py -m venv %s\n"+
				"  %s -m pip install --upgrade %s pip\n"+
				"  %s -m pip install %s\n"+
				"  %s -m pip install %s %s\n"+
				"  csghub-lite automatically tries the official PyTorch CPU index if the Aliyun mirror is unavailable.\n"+
				"  csghub-lite automatically checks this virtual environment on the next run.",
			venvDir,
			venvPython,
			pythonPackageIndexArgs,
			venvPython,
			pythonCPUOnlyTorchInstallArgs,
			venvPython,
			pythonPackageIndexArgs,
			pythonDepsInstallArgs,
		)
	}

	venvDir := "~/.csghub-lite/tools/python"
	venvPython := venvDir + "/bin/python"
	return fmt.Sprintf(
		"  python3 -m venv %s\n"+
			"  %s -m pip install --upgrade %s pip\n"+
			"  %s -m pip install %s\n"+
			"  %s -m pip install %s %s\n"+
			"  csghub-lite automatically tries the official PyTorch CPU index if the Aliyun mirror is unavailable.\n"+
			"  csghub-lite automatically checks this virtual environment on the next run.",
		venvDir,
		venvPython,
		pythonPackageIndexArgs,
		venvPython,
		pythonCPUOnlyTorchInstallArgs,
		venvPython,
		pythonPackageIndexArgs,
		pythonDepsInstallArgs,
	)
}

func preferredPipInstallCommand() string {
	if runtime.GOOS == "windows" {
		return `"%USERPROFILE%\.csghub-lite\tools\python\Scripts\python.exe" -m pip install --upgrade`
	}
	return "~/.csghub-lite/tools/python/bin/python -m pip install --upgrade"
}

func bundledConverterVersionString() string {
	return fmt.Sprintf("llama.cpp %s (bundled revision %d)", BundledConverterLLamacppRef, bundledConverterRevision)
}

func converterContextSummary() string {
	if rawURL := strings.TrimSpace(os.Getenv("CSGHUB_LITE_CONVERTER_URL")); rawURL != "" {
		return fmt.Sprintf("Converter source: CSGHUB_LITE_CONVERTER_URL=%s", rawURL)
	}
	return fmt.Sprintf("Converter version: %s", bundledConverterVersionString())
}

func converterProgressSummary() string {
	if strings.TrimSpace(os.Getenv("CSGHUB_LITE_CONVERTER_URL")) != "" {
		return "official converter from CSGHUB_LITE_CONVERTER_URL"
	}
	return fmt.Sprintf("official converter from %s", bundledConverterVersionString())
}

func converterErrorf(format string, args ...any) error {
	return fmt.Errorf("%s\n%s", converterContextSummary(), fmt.Sprintf(format, args...))
}

func converterCacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".csghub-lite", "tools")
}

func managedPythonVenvDir() string {
	return filepath.Join(converterCacheDir(), "python")
}

func managedPythonVenvExecutable() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(managedPythonVenvDir(), "Scripts", "python.exe")
	}
	return filepath.Join(managedPythonVenvDir(), "bin", "python")
}

func managedGGUFPyPath() string {
	return filepath.Join(
		bundledConverterDir(),
		fmt.Sprintf("gguf-py-%s-r%d", BundledConverterLLamacppRef, bundledConverterRevision),
	)
}

func managedConversionPath() string {
	return filepath.Join(bundledConverterVersionDir(), "conversion")
}

func bundledConverterDir() string {
	return filepath.Join(converterCacheDir(), "bundled")
}

func bundledConverterVersionDir() string {
	return filepath.Join(
		bundledConverterDir(),
		fmt.Sprintf("llama-%s-r%d", BundledConverterLLamacppRef, bundledConverterRevision),
	)
}

func remoteConverterDir() string {
	return filepath.Join(converterCacheDir(), "remote")
}

// findPythonEnv locates a suitable Python interpreter.
// Returns (pythonPath, missingDeps) where:
//   - pythonPath != "" && missingDeps == "": ready to use
//   - pythonPath != "" && missingDeps != "": Python found but packages missing
//   - pythonPath == "": no Python found at all
func findPythonEnv() (pythonPath string, missingDeps string) {
	if p := managedPythonVenvExecutable(); p != "" {
		if _, err := os.Stat(p); err == nil {
			if missing := checkPythonDeps(p); missing == "" {
				return p, ""
			}
			// Prefer reporting missing packages for the managed venv so the
			// setup hint installs into the same interpreter csghub-lite will use.
			return p, checkPythonDeps(p)
		}
	}

	if firstPython := findPythonInterpreter(); firstPython != "" {
		missing := checkPythonDeps(firstPython)
		if missing == "" {
			return firstPython, ""
		}
		return firstPython, missing
	}
	return "", ""
}

func findPythonInterpreter() string {
	python, _ := findPythonInterpreterWithStatus()
	return python
}

func findPythonInterpreterWithStatus() (string, string) {
	candidates := []string{"python3.13", "python3.12", "python3.11", "python3.10", "python3.9", "python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "python3"}
	}

	extraPaths := []string{
		"/opt/homebrew/bin/python3.13",
		"/opt/homebrew/bin/python3.12",
		"/opt/homebrew/bin/python3.11",
		"/opt/homebrew/bin/python3.10",
		"/opt/homebrew/bin/python3.9",
		"/opt/homebrew/bin/python3",
		"/usr/local/bin/python3.9",
		"/usr/local/bin/python3",
	}

	unsupported := ""
	for _, name := range candidates {
		if p, err := exec.LookPath(name); err == nil {
			if pythonVersionSupported(p) {
				return p, ""
			}
			unsupported = rememberUnsupportedPython(unsupported, p)
		}
	}
	for _, p := range extraPaths {
		if _, err := os.Stat(p); err == nil {
			if pythonVersionSupported(p) {
				return p, ""
			}
			unsupported = rememberUnsupportedPython(unsupported, p)
		}
	}
	return "", unsupported
}

func rememberUnsupportedPython(current, python string) string {
	if current != "" {
		return current
	}
	version := pythonVersionString(python)
	if version == "" {
		return python
	}
	return fmt.Sprintf("%s (%s)", python, version)
}

func pythonVersionString(python string) string {
	output, err := exec.Command(python, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func minPythonVersionString() string {
	return fmt.Sprintf("%d.%d+", minPythonMajor, minPythonMinor)
}

func pythonNotFoundOrUnsupportedMessage(unsupported string) string {
	if strings.TrimSpace(unsupported) == "" {
		return fmt.Sprintf("Python %s was not found on PATH.", minPythonVersionString())
	}
	return fmt.Sprintf("Found Python at %s, but csghub-lite requires Python %s.", unsupported, minPythonVersionString())
}

func pythonSetupIntro(unsupported string) string {
	if strings.TrimSpace(unsupported) == "" {
		return fmt.Sprintf("  1. Install Python %s and make sure python3 is available on PATH.", minPythonVersionString())
	}
	return fmt.Sprintf("  1. Install Python %s and make sure the newer python3 is available on PATH.", minPythonVersionString())
}

func pythonTooOldOrMissingLog(unsupported string) string {
	if strings.TrimSpace(unsupported) == "" {
		return "python3 not found"
	}
	return "python3 unsupported: " + unsupported
}

func pythonVersionSupported(python string) bool {
	cmd := exec.Command(python, "-c", fmt.Sprintf(
		"import sys; raise SystemExit(0 if sys.version_info >= (%d, %d) else 1)",
		minPythonMajor,
		minPythonMinor,
	))
	return cmd.Run() == nil
}

// checkPythonDeps returns a comma-separated list of missing packages, or "" if all present.
func checkPythonDeps(python string) string {
	required := requiredPythonModules()
	var missing []string
	for _, pkg := range required {
		cmd := exec.Command(python, "-c", "import "+pkg)
		if cmd.Run() != nil {
			missing = append(missing, pkg)
		}
	}
	return strings.Join(missing, ", ")
}

func requiredPythonModules() []string {
	return []string{"torch", "safetensors", "transformers", "sentencepiece", "google.protobuf"}
}

func ensureConverterScript() (string, error) {
	if u := strings.TrimSpace(os.Getenv("CSGHUB_LITE_CONVERTER_URL")); u != "" {
		return ensureRemoteConverterScript(u)
	}
	return materializeBundledConverter()
}

func bundledConverterStamp() string {
	return fmt.Sprintf("%d %s", bundledConverterRevision, BundledConverterLLamacppRef)
}

func materializeBundledConverter() (string, error) {
	if len(bundledConverterPy) == 0 {
		return "", fmt.Errorf("embedded convert_hf_to_gguf.py is missing (rebuild csghub-lite)")
	}
	dir := bundledConverterVersionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating tools dir: %w", err)
	}
	revPath := filepath.Join(dir, "bundled_convert_hf_revision")
	dst := filepath.Join(dir, "convert_hf_to_gguf.py")
	wantStamp := bundledConverterStamp()
	if prev, err := os.ReadFile(revPath); err == nil && string(prev) == wantStamp {
		if _, err := os.Stat(dst); err == nil {
			return dst, nil
		}
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, bundledConverterPy, 0o644); err != nil {
		return "", fmt.Errorf("writing converter: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("installing converter: %w", err)
	}
	if err := os.WriteFile(revPath, []byte(wantStamp), 0o644); err != nil {
		return "", fmt.Errorf("writing converter revision: %w", err)
	}
	return dst, nil
}

func ensureRemoteConverterScript(rawURL string) (string, error) {
	dir := remoteConverterDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating tools dir: %w", err)
	}
	urlPath := filepath.Join(dir, "remote_convert_hf_url")
	dst := filepath.Join(dir, "convert_hf_to_gguf.py")
	if prev, err := os.ReadFile(urlPath); err == nil && string(prev) == rawURL {
		if _, err := os.Stat(dst); err == nil {
			return dst, nil
		}
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", fmt.Errorf("downloading converter from CSGHUB_LITE_CONVERTER_URL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading converter: HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("writing downloaded converter: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.WriteFile(urlPath, []byte(rawURL), 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

// ConvertPython uses the official llama.cpp convert_hf_to_gguf.py to convert
// a HuggingFace model directory to GGUF format. Returns the path to the
// generated GGUF file. Requires python3 with torch, safetensors, and transformers.
func ConvertPython(modelDir string, progress ProgressFunc, dtype string) (string, error) {
	if progress == nil {
		progress = func(string, int, int) {}
	}

	effectiveDType, err := resolveDType(dtype)
	if err != nil {
		return "", err
	}

	if existingPath, ok, err := FindGGUFForDType(modelDir, effectiveDType); err != nil {
		return "", err
	} else if ok {
		log.Printf("CONVERT: existing GGUF found model_dir=%s dtype=%q path=%s", modelDir, effectiveDType, existingPath)
		return existingPath, nil
	}

	basePython, unsupportedPython := findPythonInterpreterWithStatus()
	if basePython == "" {
		log.Printf("CONVERT: %s for model_dir=%s", pythonTooOldOrMissingLog(unsupportedPython), modelDir)
		return "", converterErrorf(
			"this checkpoint is SafeTensors-only; csghub-lite converts it to GGUF once using the official llama.cpp Python script.\n"+
				"The Python runtime and conversion packages are not bundled with the release binary.\n\n"+
				"%s\n"+
				"Please complete these one-time setup steps:\n"+
				"%s\n"+
				"%s\n"+
				"  2. Install conversion deps:\n"+
				"%s\n\n"+
				"If the hub offers a GGUF build of the same model, download that instead to skip conversion.",
			pythonNotFoundOrUnsupportedMessage(unsupportedPython),
			pythonSetupIntro(unsupportedPython),
			pythonInstallHint(),
			pythonDepsInstallHint(),
		)
	}

	progress("Preparing Python conversion environment", 0, 0)
	log.Printf("CONVERT: preparing Python environment base=%s model_dir=%s", basePython, modelDir)
	python, setupOutput, setupErr := ensureManagedPythonEnv(basePython, progress)
	if setupErr != nil {
		log.Printf("CONVERT: preparing Python environment failed: %v", setupErr)
		if setupOutput == "" {
			setupOutput = "(no setup output)"
		}
		return "", converterErrorf(
			"this checkpoint is SafeTensors-only; csghub-lite converts it to GGUF once using the official llama.cpp Python script.\n"+
				"csghub-lite tried to prepare an isolated Python environment automatically, but setup failed.\n\n"+
				"Automatic setup failed: %s\n%s\n\n"+
				"Run these one-time setup commands manually, then retry:\n"+
				"%s\n\n"+
				"If a GGUF variant exists on CSGHub or Hugging Face, use it to skip conversion.",
			setupErr,
			lastNLines(setupOutput, 12),
			pythonDepsInstallHint(),
		)
	}

	step := fmt.Sprintf("Preparing converter (%s)", bundledConverterVersionString())
	if strings.TrimSpace(os.Getenv("CSGHUB_LITE_CONVERTER_URL")) != "" {
		step = "Downloading converter from CSGHUB_LITE_CONVERTER_URL"
	}
	progress(step, 0, 0)
	log.Printf("CONVERT: %s", step)
	script, err := ensureConverterScript()
	if err != nil {
		return "", converterErrorf("%v", err)
	}

	if sourceName, err := ensureConverterGGUFPySource(progress); err != nil {
		log.Printf("CONVERT: preparing gguf-py failed: %v", err)
		return "", converterErrorf(
			"this checkpoint is SafeTensors-only; csghub-lite converts it to GGUF once using the official llama.cpp Python script.\n"+
				"csghub-lite could not materialize the matching embedded `gguf-py` package.\n\n"+
				"Embedded gguf-py setup failed: %s\n\n"+
				"Reinstall or upgrade csghub-lite to restore the bundled converter files.\n\n"+
				"If a GGUF variant exists on CSGHub or Hugging Face, use it to skip conversion.",
			err,
		)
	} else {
		progress(fmt.Sprintf("Prepared matching gguf-py from %s", sourceName), 0, 0)
		log.Printf("CONVERT: prepared matching gguf-py from %s", sourceName)
	}

	if err := ensureSentenceTransformerPoolingConfig(modelDir); err != nil {
		return "", converterErrorf("preparing SentenceTransformers pooling config: %v", err)
	}

	outputName := generateOutputName(modelDir, effectiveDType)
	outputPath := filepath.Join(modelDir, outputName)

	progress(fmt.Sprintf("Converting with %s to GGUF (dtype: %s)", converterProgressSummary(), effectiveDType), 0, 0)
	log.Printf("CONVERT: running converter script=%s output=%s dtype=%s", script, outputPath, effectiveDType)
	if err := convertModelWithAutoRepair(python, script, modelDir, outputPath, effectiveDType, progress); err != nil {
		log.Printf("CONVERT: converter failed output=%s dtype=%s: %v", outputPath, effectiveDType, err)
		return "", err
	}

	if effectiveDType == "auto" {
		if existingPath, ok, err := FindGGUFForDType(modelDir, "auto"); err != nil {
			return "", err
		} else if ok {
			outputPath = existingPath
		} else {
			return "", converterErrorf("converter finished but output file not found for dtype %q", effectiveDType)
		}
	} else if _, err := os.Stat(outputPath); err != nil {
		return "", converterErrorf("converter finished but output file not found: %s", outputPath)
	}

	if hasVisionConfig(modelDir) {
		if _, ok, err := FindMMProjForDType(modelDir, effectiveDType); err != nil {
			return "", err
		} else if !ok {
			progress(fmt.Sprintf("Converting vision encoder (mmproj) to GGUF (dtype: %s)", effectiveDType), 0, 0)
			log.Printf("CONVERT: converting vision encoder model_dir=%s dtype=%s", modelDir, effectiveDType)
			mmOut, mmErr := runMMProjConverter(python, script, modelDir, effectiveDType)
			if mmErr != nil {
				log.Printf("mmproj conversion failed (non-fatal): %s\n%s", mmErr, lastNLines(mmOut, 5))
			} else {
				log.Printf("mmproj conversion succeeded")
			}
		}
	}

	log.Printf("CONVERT: conversion complete output=%s dtype=%s", outputPath, effectiveDType)
	return outputPath, nil
}

func hasVisionConfig(modelDir string) bool {
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		VisionConfig json.RawMessage `json:"vision_config"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return false
	}
	return len(cfg.VisionConfig) > 0
}

type sentenceTransformerModule struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

func ensureSentenceTransformerPoolingConfig(modelDir string) error {
	modulePath := filepath.Join(modelDir, "modules.json")
	data, err := os.ReadFile(modulePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var modules []sentenceTransformerModule
	if err := json.Unmarshal(data, &modules); err != nil {
		return nil
	}

	poolingPath := ""
	for _, module := range modules {
		if strings.HasSuffix(strings.TrimSpace(module.Type), "Pooling") {
			poolingPath = strings.TrimSpace(module.Path)
			break
		}
	}
	if poolingPath == "" {
		return nil
	}

	relPath, ok := cleanSentenceTransformerModulePath(poolingPath)
	if !ok {
		return fmt.Errorf("invalid pooling module path %q", poolingPath)
	}
	configPath := filepath.Join(modelDir, relPath, "config.json")
	if _, err := os.Stat(configPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	config := sentenceTransformerPoolingConfig(inferSentenceTransformerPooling(modelDir))
	data, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(configPath, data, 0o644)
}

func cleanSentenceTransformerModulePath(raw string) (string, bool) {
	normalized := filepath.Clean(filepath.FromSlash(strings.TrimSpace(raw)))
	if normalized == "." || normalized == "" || filepath.IsAbs(normalized) {
		return "", false
	}
	if normalized == ".." || strings.HasPrefix(normalized, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return normalized, true
}

func inferSentenceTransformerPooling(modelDir string) string {
	normalized := normalizeEmbeddingModelName(filepath.Base(modelDir))
	switch {
	case strings.Contains(normalized, "qwen3embedding"),
		strings.Contains(normalized, "gteqwen"):
		return "lasttoken"
	case strings.Contains(normalized, "bge"),
		strings.Contains(normalized, "gtelargeenv15"),
		strings.Contains(normalized, "gtebaseenv15"),
		strings.Contains(normalized, "gtesmallenv15"):
		return "cls"
	default:
		return "mean"
	}
}

func normalizeEmbeddingModelName(modelName string) string {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	replacer := strings.NewReplacer(
		"/", "",
		"-", "",
		"_", "",
		".", "",
		" ", "",
	)
	return replacer.Replace(modelName)
}

func sentenceTransformerPoolingConfig(pooling string) map[string]interface{} {
	pooling = strings.ToLower(strings.TrimSpace(pooling))
	return map[string]interface{}{
		"pooling_mode":                      pooling,
		"pooling_mode_cls_token":            pooling == "cls",
		"pooling_mode_mean_tokens":          pooling == "mean",
		"pooling_mode_max_tokens":           false,
		"pooling_mode_mean_sqrt_len_tokens": false,
		"pooling_mode_weightedmean_tokens":  false,
		"pooling_mode_lasttoken":            pooling == "lasttoken",
		"include_prompt":                    true,
	}
}

// PythonConverterAvailable returns true if python3 and the required
// dependencies are available for running the official converter.
func PythonConverterAvailable() bool {
	p, missing := findPythonEnv()
	return p != "" && missing == ""
}

func ensureManagedPythonEnv(basePython string, progress ProgressFunc) (string, string, error) {
	python := managedPythonVenvExecutable()
	if python == "" {
		return "", "", fmt.Errorf("managed Python executable path is empty")
	}
	if _, err := os.Stat(python); err != nil {
		progress("Creating Python virtual environment", 0, 0)
		log.Printf("CONVERT: creating Python virtual environment path=%s", managedPythonVenvDir())
		output, runErr := runCommand(basePython, "-m", "venv", managedPythonVenvDir())
		if runErr != nil {
			return "", output, fmt.Errorf("creating Python virtual environment: %w", runErr)
		}
	} else {
		progress("Checking Python conversion packages", 0, 0)
		log.Printf("CONVERT: checking Python conversion packages python=%s", python)
		if missing := checkPythonDeps(python); missing == "" {
			log.Printf("CONVERT: Python conversion environment ready python=%s", python)
			return python, "", nil
		} else {
			log.Printf("CONVERT: Python conversion packages missing: %s", missing)
		}
	}

	var combined []string
	steps := []struct {
		progress         string
		args             []string
		fallbackProgress string
		fallbackArgs     []string
	}{
		{
			progress: "Installing Python package manager updates",
			args:     []string{"-m", "pip", "install", "--upgrade", "--index-url", pythonPackageIndexURL, "pip"},
		},
		{
			progress:         "Installing CPU PyTorch for model conversion from Aliyun mirror",
			args:             []string{"-m", "pip", "install", "--upgrade", "--index-url", pythonPackageIndexURL, "--find-links", pythonCPUOnlyTorchIndexURL, "torch"},
			fallbackProgress: "Retrying CPU PyTorch install from official PyTorch index",
			fallbackArgs:     []string{"-m", "pip", "install", "--upgrade", "--index-url", pythonCPUOnlyTorchFallbackURL, "torch"},
		},
		{
			progress: "Installing model conversion Python packages",
			args:     []string{"-m", "pip", "install", "--upgrade", "--index-url", pythonPackageIndexURL, "safetensors", "transformers", "sentencepiece", "protobuf"},
		},
	}
	for _, step := range steps {
		progress(step.progress, 0, 0)
		log.Printf("CONVERT: %s", step.progress)
		output, err := runPythonPipCommand(python, step.args...)
		if output != "" {
			combined = append(combined, output)
		}
		if err != nil && len(step.fallbackArgs) > 0 {
			combined = append(combined, fmt.Sprintf("%s failed: %v", strings.Join(step.args, " "), err))
			progress(step.fallbackProgress, 0, 0)
			log.Printf("CONVERT: %s", step.fallbackProgress)
			output, err = runPythonPipCommand(python, step.fallbackArgs...)
			if output != "" {
				combined = append(combined, output)
			}
		}
		if err != nil {
			return "", strings.Join(combined, "\n"), err
		}
	}
	if missing := checkPythonDeps(python); missing != "" {
		return "", strings.Join(combined, "\n"), fmt.Errorf("missing Python packages after automatic install: %s", missing)
	}
	progress("Python conversion environment ready", 0, 0)
	log.Printf("CONVERT: Python conversion environment ready python=%s", python)
	return python, strings.Join(combined, "\n"), nil
}

func runPythonPipCommand(python string, args ...string) (string, error) {
	output, err := runCommand(python, args...)
	if err != nil {
		return output, fmt.Errorf("%s %s: %w", python, strings.Join(args, " "), err)
	}
	return output, nil
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "PIP_DISABLE_PIP_VERSION_CHECK=1")
	return runLoggedCommand(cmd)
}

func runLoggedCommand(cmd *exec.Cmd) (string, error) {
	start := time.Now()
	done := make(chan struct{})
	log.Printf("CONVERT: command started: %s", strings.Join(cmd.Args, " "))
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				log.Printf("CONVERT: command still running after %s: %s", time.Since(start).Round(time.Second), strings.Join(cmd.Args, " "))
			case <-done:
				return
			}
		}
	}()
	output, err := cmd.CombinedOutput()
	close(done)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		log.Printf("CONVERT: command failed after %s: %s: %v", elapsed, strings.Join(cmd.Args, " "), err)
	} else {
		log.Printf("CONVERT: command completed after %s: %s", elapsed, strings.Join(cmd.Args, " "))
	}
	return strings.TrimSpace(string(output)), err
}

func ensureConverterGGUFPySource(progress ProgressFunc) (string, error) {
	progress("Preparing embedded matching gguf-py", 0, 0)
	return ensureBundledGGUFPy()
}

func convertModelWithAutoRepair(python, script, modelDir, outputPath, dtype string, progress ProgressFunc) error {
	output, err := runModelConverter(python, script, modelDir, outputPath, dtype)
	if err == nil {
		return nil
	}

	repair := attemptConverterAutoRepair(python, output, progress)
	if repair.attempted && repair.succeeded {
		_ = os.Remove(outputPath)
		progress(fmt.Sprintf("Retrying converter after automatic repair (dtype: %s)", dtype), 0, 0)
		log.Printf("CONVERT: automatic repair succeeded; retrying converter dtype=%s", dtype)
		output, err = runModelConverter(python, script, modelDir, outputPath, dtype)
		if err == nil {
			return nil
		}
	}

	_ = os.Remove(outputPath)
	return formatConverterFailure(err, output, repair.note)
}

func runModelConverter(python, script, modelDir, outputPath, dtype string) (string, error) {
	cmd := exec.Command(python, script, modelDir,
		"--outfile", outputPath,
		"--outtype", dtype,
	)
	cmd.Env = converterPythonEnv()
	return runLoggedCommand(cmd)
}

func runMMProjConverter(python, script, modelDir, dtype string) (string, error) {
	cmd := exec.Command(python, script, modelDir,
		"--outtype", dtype,
		"--mmproj",
	)
	cmd.Dir = modelDir
	cmd.Env = converterPythonEnv()
	return runLoggedCommand(cmd)
}

func converterPythonEnv() []string {
	env := os.Environ()
	ggufPath := managedGGUFPyPath()
	if existing := os.Getenv("PYTHONPATH"); existing != "" {
		env = append(env, "PYTHONPATH="+ggufPath+string(os.PathListSeparator)+existing)
	} else {
		env = append(env, "PYTHONPATH="+ggufPath)
	}
	return env
}

func attemptConverterAutoRepair(python, combined string, progress ProgressFunc) converterRepairResult {
	plan := repairPlanForConverterFailure(combined)
	if !plan.installBundledGGUFPy && len(plan.upgradePackages) == 0 {
		return converterRepairResult{}
	}

	var notes []string
	var failures []string
	var otherPackages []string
	for _, pkg := range plan.upgradePackages {
		if pkg == "gguf" {
			continue
		}
		otherPackages = append(otherPackages, pkg)
	}

	if plan.installBundledGGUFPy {
		progress("Restoring embedded matching gguf-py", 0, 0)
		sourceName, err := ensureBundledGGUFPy()
		if err != nil {
			failures = append(failures, fmt.Sprintf(
				"csghub-lite detected that this bundled converter needs matching `gguf-py` from llama.cpp tag `%s`.\n"+
					"Restoring the embedded gguf-py package failed: %s\n\n"+
					"Reinstall or upgrade csghub-lite to restore the bundled converter files.",
				BundledConverterLLamacppRef,
				err,
			))
		} else {
			notes = append(notes, fmt.Sprintf("prepared matching gguf-py from %s", sourceName))
		}
	}

	if len(otherPackages) > 0 {
		progress(fmt.Sprintf("Upgrading Python package%s: %s", pluralSuffix(len(otherPackages)), strings.Join(otherPackages, ", ")), 0, 0)
		pipOutput, pipErr := upgradePythonPackages(python, otherPackages)
		command := fmt.Sprintf("%s -m pip install --upgrade --index-url %s %s", python, pythonPackageIndexURL, strings.Join(otherPackages, " "))

		if pipErr != nil {
			pipSummary := lastNLines(pipOutput, 10)
			if pipSummary == "" {
				pipSummary = "(no pip output)"
			}
			failures = append(failures, fmt.Sprintf(
				"csghub-lite tried to run:\n  %s\n\n"+
					"Automatic package upgrade failed: %s\n%s",
				command,
				pipErr,
				pipSummary,
			))
		} else {
			notes = append(notes, fmt.Sprintf("upgraded %s", strings.Join(otherPackages, ", ")))
		}
	}

	if len(notes) == 0 {
		return converterRepairResult{
			attempted: true,
			note:      repairFailureNote(failures),
		}
	}

	return converterRepairResult{
		attempted: true,
		succeeded: true,
		note:      repairSuccessNote(notes, failures),
	}
}

func upgradePythonPackages(python string, packages []string) (string, error) {
	args := []string{"-m", "pip", "install", "--upgrade", "--index-url", pythonPackageIndexURL}
	args = append(args, packages...)
	cmd := exec.Command(python, args...)
	cmd.Env = append(os.Environ(), "PIP_DISABLE_PIP_VERSION_CHECK=1")
	return runLoggedCommand(cmd)
}

func repairPlanForConverterFailure(combined string) converterRepairPlan {
	if combined == "" {
		return converterRepairPlan{}
	}

	var plan converterRepairPlan
	add := func(pkg string) {
		for _, existing := range plan.upgradePackages {
			if existing == pkg {
				return
			}
		}
		plan.upgradePackages = append(plan.upgradePackages, pkg)
	}

	lower := strings.ToLower(combined)
	if (strings.Contains(combined, "AttributeError") &&
		(strings.Contains(combined, "MODEL_ARCH") || strings.Contains(combined, "gguf."))) ||
		strings.Contains(lower, "no module named 'gguf'") ||
		strings.Contains(lower, "no module named \"gguf\"") {
		plan.installBundledGGUFPy = true
	}
	if strings.Contains(combined, "Transformers does not recognize this architecture") ||
		strings.Contains(combined, "pip install --upgrade transformers") ||
		strings.Contains(combined, "pip install git+https://github.com/huggingface/transformers.git") ||
		strings.Contains(lower, "no module named 'transformers.models.") {
		add("transformers")
	}
	if strings.Contains(lower, "no module named 'sentencepiece'") ||
		strings.Contains(lower, "no module named \"sentencepiece\"") {
		add("sentencepiece")
	}
	if strings.Contains(lower, "no module named 'google'") ||
		strings.Contains(lower, "no module named \"google\"") ||
		strings.Contains(lower, "no module named 'google.protobuf'") ||
		strings.Contains(lower, "no module named \"google.protobuf\"") {
		add("protobuf")
	}

	return plan
}

func repairFailureNote(failures []string) string {
	if len(failures) == 0 {
		return ""
	}
	return "\n\nAutomatic repair failed:\n\n" + strings.Join(failures, "\n\n")
}

func repairSuccessNote(notes, failures []string) string {
	note := fmt.Sprintf(
		"\n\ncsghub-lite auto-repaired the converter environment (%s) and retried once.",
		strings.Join(notes, ", "),
	)
	if len(failures) > 0 {
		note += "\n\nSome automatic repair steps still failed, so manual cleanup may still be needed:\n\n" +
			strings.Join(failures, "\n\n")
	}
	return note
}

func ensureBundledGGUFPy() (string, error) {
	dir := bundledConverterDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating bundled converter dir: %w", err)
	}
	if err := ensureBundledConversion(); err != nil {
		return "", err
	}

	dst := managedGGUFPyPath()
	if bundledGGUFPyReady(dst) {
		return "embedded llama.cpp " + BundledConverterLLamacppRef, nil
	}
	unlock, err := acquireBundledGGUFPyLock(dst)
	if err != nil {
		return "", err
	}
	defer unlock()
	if bundledGGUFPyReady(dst) {
		return "embedded llama.cpp " + BundledConverterLLamacppRef, nil
	}

	tmpDir, err := os.MkdirTemp(dir, ".gguf-py-*")
	if err != nil {
		return "", fmt.Errorf("creating gguf-py temp dir: %w", err)
	}
	defer func() {
		if tmpDir != "" {
			os.RemoveAll(tmpDir)
		}
	}()

	if err := materializeBundledGGUFPy(tmpDir); err != nil {
		return "", fmt.Errorf("materializing embedded gguf-py: %w", err)
	}
	if !bundledGGUFPyReady(dst) {
		if err := os.RemoveAll(dst); err != nil {
			return "", fmt.Errorf("removing incomplete gguf-py cache: %w", err)
		}
	}
	if err := os.Rename(tmpDir, dst); err != nil {
		// A concurrent conversion may have installed the same immutable bundle.
		if bundledGGUFPyReady(dst) {
			return "embedded llama.cpp " + BundledConverterLLamacppRef, nil
		}
		return "", fmt.Errorf("installing embedded gguf-py: %w", err)
	}
	tmpDir = ""

	return "embedded llama.cpp " + BundledConverterLLamacppRef, nil
}

func ensureBundledConversion() error {
	dst := managedConversionPath()
	if bundledConversionReady(dst) {
		return nil
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating bundled conversion dir: %w", err)
	}
	unlock, err := acquireBundledGGUFPyLock(dst)
	if err != nil {
		return err
	}
	defer unlock()
	if bundledConversionReady(dst) {
		return nil
	}
	tmpDir, err := os.MkdirTemp(dir, ".conversion-*")
	if err != nil {
		return fmt.Errorf("creating conversion package temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := materializeBundledPythonTree(bundledConversion, bundledConversionRoot, tmpDir); err != nil {
		return fmt.Errorf("materializing embedded conversion package: %w", err)
	}
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("removing incomplete conversion package: %w", err)
	}
	if err := os.Rename(tmpDir, dst); err != nil {
		if bundledConversionReady(dst) {
			return nil
		}
		return fmt.Errorf("installing embedded conversion package: %w", err)
	}
	return nil
}

const (
	bundledGGUFPyLockWait  = 30 * time.Second
	bundledGGUFPyLockStale = 2 * time.Minute
)

func acquireBundledGGUFPyLock(dst string) (func(), error) {
	lockPath := dst + ".lock"
	deadline := time.Now().Add(bundledGGUFPyLockWait)

	for {
		lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if closeErr := lock.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("closing gguf-py initialization lock: %w", closeErr)
			}
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("creating gguf-py initialization lock: %w", err)
		}

		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > bundledGGUFPyLockStale {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for matching gguf-py initialization")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func bundledGGUFPyReady(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "gguf", "__init__.py"))
	return err == nil
}

func bundledConversionReady(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "__init__.py"))
	return err == nil
}

func materializeBundledGGUFPy(dst string) error {
	return materializeBundledPythonTree(bundledGGUFPy, bundledGGUFPyRoot, dst)
}

func materializeBundledPythonTree(source fs.FS, root, dst string) error {
	return fs.WalkDir(source, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func formatConverterFailure(err error, output string, repairNote string) error {
	return converterErrorf(
		"convert_hf_to_gguf.py failed: %s\n%s%s%s",
		err,
		lastNLines(output, 5),
		repairNote,
		hintForConverterScriptFailure(output),
	)
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func hintForConverterScriptFailure(combined string) string {
	if combined == "" {
		return ""
	}
	// Typical mismatch: script from a new llama.cpp tag + older PyPI/distro `gguf`.
	if strings.Contains(combined, "AttributeError") &&
		(strings.Contains(combined, "MODEL_ARCH") || strings.Contains(combined, "gguf.")) {
		return fmt.Sprintf(
			"\n\nLikely the `gguf` Python package is older than this converter script expects.\n"+
				"csghub-lite includes matching `gguf-py` from llama.cpp tag `%s` in its binary.\n"+
				"To reset the bundled copy, delete the bundled converter cache under %s\n",
			BundledConverterLLamacppRef,
			bundledConverterDir(),
		)
	}
	if strings.Contains(combined, "Transformers does not recognize this architecture") ||
		strings.Contains(combined, "pip install --upgrade transformers") ||
		strings.Contains(combined, "pip install git+https://github.com/huggingface/transformers.git") {
		return "\n\nThe installed `transformers` package looks too old for this model.\n" +
			"If the automatic upgrade did not fix it, run:\n" +
			"  " + preferredPipInstallCommand() + " transformers\n"
	}
	return ""
}
