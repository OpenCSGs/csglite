package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
)

const (
	runtimeDirName        = "ai-runtime"
	legacyRuntimeDirName  = "image-runtime"
	venvDirName           = "venv"
	manifestFileName      = "runtime.json"
	aliyunPyPIIndex       = "https://mirrors.aliyun.com/pypi/simple"
	aliyunTorchRoot       = "https://mirrors.aliyun.com/pytorch-wheels"
	officialTorchRoot     = "https://download.pytorch.org/whl"
	mirrorModeEnv         = "CSGHUB_LITE_PACKAGE_MIRROR"
	regionEnv             = "CSGHUB_LITE_REGION"
	torchIndexOverrideEnv = "CSGHUB_LITE_TORCH_INDEX_URL"
	pypiIndexOverrideEnv  = "CSGHUB_LITE_PYPI_INDEX_URL"
)

var requiredPythonPackages = []string{
	"diffusers",
	"transformers",
	"accelerate",
	"safetensors",
	"sentencepiece",
	"google.protobuf",
	"PIL",
}

var requiredASRPythonPackages = []string{
	"fastapi",
	"funasr",
	"modelscope",
	"transformers",
	"safetensors",
	"soundfile",
	"librosa",
	"imageio_ffmpeg",
	"uvicorn",
}

var defaultTorchPackages = []string{"torch", "torchvision", "torchaudio"}

var aliyunCUDATorchPackages = []string{
	"torch==2.5.1+cu124",
	"torchvision==0.20.1+cu124",
	"torchaudio==2.5.1+cu124",
}

// HardwareKind describes the PyTorch wheel/runtime family to use.
type HardwareKind string

const (
	HardwareCPU  HardwareKind = "cpu"
	HardwareMPS  HardwareKind = "mps"
	HardwareCUDA HardwareKind = "cuda"
	HardwareROCm HardwareKind = "rocm"
)

// RuntimeStatus describes whether the Diffusers runtime is ready.
type RuntimeStatus struct {
	Ready           bool         `json:"ready"`
	RuntimeDir      string       `json:"runtime_dir"`
	VenvDir         string       `json:"venv_dir"`
	Python          string       `json:"python,omitempty"`
	Platform        string       `json:"platform"`
	Arch            string       `json:"arch"`
	Hardware        HardwareKind `json:"hardware"`
	TorchIndexURL   string       `json:"torch_index_url,omitempty"`
	MissingPackages []string     `json:"missing_packages,omitempty"`
	InstallCommand  []string     `json:"install_command,omitempty"`
	Error           string       `json:"error,omitempty"`
}

type RuntimeManifest struct {
	Python      string       `json:"python"`
	Platform    string       `json:"platform"`
	Arch        string       `json:"arch"`
	Hardware    HardwareKind `json:"hardware"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	TorchIndex  string       `json:"torch_index_url,omitempty"`
	PyPIIndex   string       `json:"pypi_index_url,omitempty"`
	PackageSpec []string     `json:"package_spec"`
}

type PackageMirror string

const (
	PackageMirrorOfficial PackageMirror = "official"
	PackageMirrorAliyun   PackageMirror = "aliyun"
)

type PackageIndexes struct {
	Mirror            PackageMirror
	TorchIndexURL     string
	TorchFindLinksURL string
	PyPIIndexURL      string
}

type RuntimeManager struct {
	rootDir string
}

type ProgressFunc func(step string, current, total int)

func NewRuntimeManager() (*RuntimeManager, error) {
	home, err := config.AppHome()
	if err != nil {
		return nil, err
	}
	rootDir := filepath.Join(home, runtimeDirName)
	if err := migrateLegacyRuntimeDir(filepath.Join(home, legacyRuntimeDirName), rootDir); err != nil {
		return nil, err
	}
	return NewRuntimeManagerAt(rootDir), nil
}

func NewRuntimeManagerAt(rootDir string) *RuntimeManager {
	return &RuntimeManager{rootDir: filepath.Clean(rootDir)}
}

func migrateLegacyRuntimeDir(legacyDir, rootDir string) error {
	legacyDir = filepath.Clean(legacyDir)
	rootDir = filepath.Clean(rootDir)
	if legacyDir == rootDir {
		return nil
	}
	if _, err := os.Stat(rootDir); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking AI runtime directory: %w", err)
	}
	if _, err := os.Stat(legacyDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("checking legacy image runtime directory: %w", err)
	}
	if err := os.Rename(legacyDir, rootDir); err != nil {
		return fmt.Errorf("migrating legacy image runtime to AI runtime: %w", err)
	}
	return nil
}

func (m *RuntimeManager) RootDir() string {
	return m.rootDir
}

func (m *RuntimeManager) VenvDir() string {
	return filepath.Join(m.rootDir, venvDirName)
}

func (m *RuntimeManager) PythonPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.VenvDir(), "Scripts", "python.exe")
	}
	return filepath.Join(m.VenvDir(), "bin", "python")
}

func (m *RuntimeManager) Status(ctx context.Context) RuntimeStatus {
	hardware := DetectHardware()
	indexes := ResolvePackageIndexes(hardware)
	status := RuntimeStatus{
		RuntimeDir:    m.rootDir,
		VenvDir:       m.VenvDir(),
		Python:        m.PythonPath(),
		Platform:      runtime.GOOS,
		Arch:          runtime.GOARCH,
		Hardware:      hardware,
		TorchIndexURL: torchSourceURL(indexes),
	}
	status.InstallCommand = m.InstallCommand(status.Hardware)

	if _, err := os.Stat(status.Python); err != nil {
		status.Error = "Diffusers runtime is not installed"
		status.MissingPackages = append([]string{"torch"}, requiredPythonPackages...)
		return status
	}
	missing, err := missingPackages(ctx, status.Python, requiredPythonPackages)
	if err != nil {
		status.Error = err.Error()
		status.MissingPackages = append([]string{"torch"}, requiredPythonPackages...)
		return status
	}
	status.MissingPackages = missing
	status.Ready = len(missing) == 0
	if !status.Ready {
		status.Error = "Diffusers runtime is missing Python packages"
	}
	return status
}

func (m *RuntimeManager) EnsureReady(ctx context.Context) error {
	status := m.Status(ctx)
	if status.Ready {
		return nil
	}
	return &RuntimeNotReadyError{Status: status}
}

func (m *RuntimeManager) ASRStatus(ctx context.Context) RuntimeStatus {
	hardware := DetectHardware()
	indexes := ResolvePackageIndexes(hardware)
	status := RuntimeStatus{
		RuntimeDir:    m.rootDir,
		VenvDir:       m.VenvDir(),
		Python:        m.PythonPath(),
		Platform:      runtime.GOOS,
		Arch:          runtime.GOARCH,
		Hardware:      hardware,
		TorchIndexURL: torchSourceURL(indexes),
	}
	status.InstallCommand = m.InstallCommand(status.Hardware)

	if _, err := os.Stat(status.Python); err != nil {
		status.Error = "ASR runtime is not installed"
		status.MissingPackages = append([]string{"torch", "torchaudio"}, requiredASRPythonPackages...)
		return status
	}
	missing, err := missingPackages(ctx, status.Python, append([]string{"torch", "torchaudio"}, requiredASRPythonPackages...))
	if err != nil {
		status.Error = err.Error()
		status.MissingPackages = append([]string{"torch", "torchaudio"}, requiredASRPythonPackages...)
		return status
	}
	status.MissingPackages = missing
	status.Ready = len(missing) == 0
	if !status.Ready {
		status.Error = "ASR runtime is missing Python packages"
	}
	return status
}

func (m *RuntimeManager) EnsureASRReady(ctx context.Context) error {
	status := m.ASRStatus(ctx)
	if status.Ready {
		return nil
	}
	return &RuntimeNotReadyError{Status: status}
}

type RuntimeNotReadyError struct {
	Status RuntimeStatus
}

func (e *RuntimeNotReadyError) Error() string {
	if e.Status.Error != "" {
		return e.Status.Error
	}
	return "Diffusers runtime is not ready"
}

func IsRuntimeNotReady(err error) bool {
	var target *RuntimeNotReadyError
	return errors.As(err, &target)
}

func RuntimeStatusFromError(err error) (RuntimeStatus, bool) {
	var target *RuntimeNotReadyError
	if errors.As(err, &target) {
		return target.Status, true
	}
	return RuntimeStatus{}, false
}

func (m *RuntimeManager) Install(ctx context.Context) (RuntimeStatus, error) {
	return m.InstallWithProgress(ctx, nil)
}

func (m *RuntimeManager) InstallWithProgress(ctx context.Context, progress ProgressFunc) (RuntimeStatus, error) {
	return m.InstallWithProgressOptions(ctx, progress, false)
}

func (m *RuntimeManager) InstallWithProgressOptions(ctx context.Context, progress ProgressFunc, upgradePackages bool) (RuntimeStatus, error) {
	if progress == nil {
		progress = func(string, int, int) {}
	}
	hardware := DetectHardware()
	indexes := ResolvePackageIndexes(hardware)
	progress(fmt.Sprintf("detect system %s/%s %s mirror=%s", runtime.GOOS, runtime.GOARCH, hardware, indexes.Mirror), 1, 6)
	progress("prepare image runtime", 2, 6)
	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		return m.Status(ctx), fmt.Errorf("creating image runtime directory: %w", err)
	}
	if _, err := os.Stat(m.PythonPath()); err != nil {
		hostPython, err := findHostPython()
		if err != nil {
			return m.Status(ctx), err
		}
		progress("create Python venv", 3, 6)
		if err := runCommand(ctx, hostPython, "-m", "venv", m.VenvDir()); err != nil {
			return m.Status(ctx), fmt.Errorf("creating Python venv: %w", err)
		}
	}

	python := m.PythonPath()
	if err := runCommand(ctx, python, "-m", "ensurepip", "--upgrade"); err != nil {
		return m.Status(ctx), fmt.Errorf("bootstrapping pip: %w", err)
	}
	progress("upgrade pip", 4, 6)
	pipArgs := []string{"-m", "pip", "install", "--upgrade", "pip"}
	if indexes.PyPIIndexURL != "" {
		pipArgs = append(pipArgs, "-i", indexes.PyPIIndexURL)
	}
	if err := runCommand(ctx, python, pipArgs...); err != nil {
		return m.Status(ctx), fmt.Errorf("upgrading pip: %w", err)
	}
	torchPackages := torchPackageSpecs(hardware, indexes)
	torchArgs := append([]string{"-m", "pip", "install"}, torchPackages...)
	if indexes.TorchIndexURL != "" {
		torchArgs = append(torchArgs, "--index-url", indexes.TorchIndexURL)
	} else if indexes.PyPIIndexURL != "" {
		torchArgs = append(torchArgs, "-i", indexes.PyPIIndexURL)
	}
	if indexes.TorchFindLinksURL != "" {
		torchArgs = append(torchArgs, "--find-links", indexes.TorchFindLinksURL)
	}
	if indexes.TorchIndexURL != "" {
		progress("install PyTorch from "+indexes.TorchIndexURL, 5, 6)
	} else if indexes.TorchFindLinksURL != "" && indexes.PyPIIndexURL != "" {
		progress("install PyTorch from "+string(indexes.Mirror)+" mirror", 5, 6)
	} else if indexes.TorchFindLinksURL != "" {
		progress("install PyTorch from "+indexes.TorchFindLinksURL, 5, 6)
	} else if indexes.PyPIIndexURL != "" {
		progress("install PyTorch from "+indexes.PyPIIndexURL, 5, 6)
	} else {
		progress("install PyTorch", 5, 6)
	}
	if err := runCommand(ctx, python, torchArgs...); err != nil {
		return m.Status(ctx), fmt.Errorf("installing PyTorch: %w", err)
	}
	deps := []string{"-m", "pip", "install"}
	if upgradePackages {
		deps = append(deps, "--upgrade")
	}
	deps = append(deps, "diffusers", "transformers", "accelerate", "safetensors", "sentencepiece", "protobuf", "pillow")
	if indexes.PyPIIndexURL != "" {
		deps = append(deps, "-i", indexes.PyPIIndexURL)
		if upgradePackages {
			progress("upgrade Diffusers dependencies from "+indexes.PyPIIndexURL, 6, 6)
		} else {
			progress("install Diffusers dependencies from "+indexes.PyPIIndexURL, 6, 6)
		}
	} else {
		if upgradePackages {
			progress("upgrade Diffusers dependencies", 6, 6)
		} else {
			progress("install Diffusers dependencies", 6, 6)
		}
	}
	if err := runCommand(ctx, python, deps...); err != nil {
		return m.Status(ctx), fmt.Errorf("installing Diffusers dependencies: %w", err)
	}

	now := time.Now()
	manifest := RuntimeManifest{
		Python:      python,
		Platform:    runtime.GOOS,
		Arch:        runtime.GOARCH,
		Hardware:    hardware,
		CreatedAt:   now,
		UpdatedAt:   now,
		TorchIndex:  torchSourceURL(indexes),
		PyPIIndex:   indexes.PyPIIndexURL,
		PackageSpec: append(torchPackages, "diffusers", "transformers", "accelerate", "safetensors", "sentencepiece", "protobuf", "pillow"),
	}
	if err := writeManifest(filepath.Join(m.rootDir, manifestFileName), manifest); err != nil {
		return m.Status(ctx), err
	}
	return m.Status(ctx), nil
}

func (m *RuntimeManager) InstallASRWithProgressOptions(ctx context.Context, progress ProgressFunc, upgradePackages bool) (RuntimeStatus, error) {
	if progress == nil {
		progress = func(string, int, int) {}
	}

	hardware := DetectHardware()
	indexes := ResolvePackageIndexes(hardware)
	progress(fmt.Sprintf("detect system %s/%s %s mirror=%s", runtime.GOOS, runtime.GOARCH, hardware, indexes.Mirror), 1, 5)
	progress("prepare shared Python runtime", 2, 5)
	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		return m.ASRStatus(ctx), fmt.Errorf("creating runtime directory: %w", err)
	}

	python := m.PythonPath()
	createdVenv := false
	if _, err := os.Stat(python); err != nil {
		hostPython, err := findHostPython()
		if err != nil {
			return m.ASRStatus(ctx), err
		}
		progress("create Python venv", 3, 5)
		if err := runCommand(ctx, hostPython, "-m", "venv", m.VenvDir()); err != nil {
			return m.ASRStatus(ctx), fmt.Errorf("creating Python venv: %w", err)
		}
		createdVenv = true
	}

	if createdVenv || upgradePackages {
		if err := runCommand(ctx, python, "-m", "ensurepip", "--upgrade"); err != nil {
			return m.ASRStatus(ctx), fmt.Errorf("bootstrapping pip: %w", err)
		}
		pipArgs := []string{"-m", "pip", "install", "--upgrade", "pip"}
		if indexes.PyPIIndexURL != "" {
			pipArgs = append(pipArgs, "-i", indexes.PyPIIndexURL)
		}
		if err := runCommand(ctx, python, pipArgs...); err != nil {
			return m.ASRStatus(ctx), fmt.Errorf("upgrading pip: %w", err)
		}
	}

	torchPackages := torchPackageSpecs(hardware, indexes)
	torchMissing, err := missingPackages(ctx, python, []string{"torch", "torchaudio"})
	if err != nil {
		return m.ASRStatus(ctx), err
	}
	if len(torchMissing) > 0 || upgradePackages {
		torchArgs := append([]string{"-m", "pip", "install"}, torchPackages...)
		if indexes.TorchIndexURL != "" {
			torchArgs = append(torchArgs, "--index-url", indexes.TorchIndexURL)
		} else if indexes.PyPIIndexURL != "" {
			torchArgs = append(torchArgs, "-i", indexes.PyPIIndexURL)
		}
		if indexes.TorchFindLinksURL != "" {
			torchArgs = append(torchArgs, "--find-links", indexes.TorchFindLinksURL)
		}
		if indexes.TorchIndexURL != "" {
			progress("install PyTorch from "+indexes.TorchIndexURL, 4, 5)
		} else if indexes.TorchFindLinksURL != "" && indexes.PyPIIndexURL != "" {
			progress("install PyTorch from "+string(indexes.Mirror)+" mirror", 4, 5)
		} else if indexes.TorchFindLinksURL != "" {
			progress("install PyTorch from "+indexes.TorchFindLinksURL, 4, 5)
		} else if indexes.PyPIIndexURL != "" {
			progress("install PyTorch from "+indexes.PyPIIndexURL, 4, 5)
		} else {
			progress("install PyTorch", 4, 5)
		}
		if err := runCommand(ctx, python, torchArgs...); err != nil {
			return m.ASRStatus(ctx), fmt.Errorf("installing PyTorch: %w", err)
		}
	}

	asrPackages := []string{"fastapi", "funasr", "modelscope", "transformers", "safetensors", "soundfile", "librosa", "imageio-ffmpeg", "uvicorn"}
	asrMissing, err := missingPackages(ctx, python, requiredASRPythonPackages)
	if err != nil {
		return m.ASRStatus(ctx), err
	}
	if len(asrMissing) == 0 && !upgradePackages {
		return m.ASRStatus(ctx), nil
	}
	deps := []string{"-m", "pip", "install"}
	if upgradePackages {
		deps = append(deps, "--upgrade")
	}
	if upgradePackages {
		deps = append(deps, asrPackages...)
	} else {
		deps = append(deps, asrMissing...)
	}
	if indexes.PyPIIndexURL != "" {
		deps = append(deps, "-i", indexes.PyPIIndexURL)
		if upgradePackages {
			progress("upgrade ASR dependencies from "+indexes.PyPIIndexURL, 5, 5)
		} else {
			progress("install ASR dependencies from "+indexes.PyPIIndexURL, 5, 5)
		}
	} else {
		if upgradePackages {
			progress("upgrade ASR dependencies", 5, 5)
		} else {
			progress("install ASR dependencies", 5, 5)
		}
	}
	if err := runCommand(ctx, python, deps...); err != nil {
		return m.ASRStatus(ctx), fmt.Errorf("installing ASR dependencies: %w", err)
	}

	now := time.Now()
	manifest := RuntimeManifest{
		Python:      python,
		Platform:    runtime.GOOS,
		Arch:        runtime.GOARCH,
		Hardware:    DetectHardware(),
		CreatedAt:   now,
		UpdatedAt:   now,
		TorchIndex:  torchSourceURL(indexes),
		PyPIIndex:   indexes.PyPIIndexURL,
		PackageSpec: append(torchPackageSpecs(DetectHardware(), indexes), "fastapi", "funasr", "modelscope", "transformers", "safetensors", "soundfile", "librosa", "imageio-ffmpeg", "uvicorn"),
	}
	if err := writeManifest(filepath.Join(m.rootDir, manifestFileName), manifest); err != nil {
		return m.ASRStatus(ctx), err
	}
	return m.ASRStatus(ctx), nil
}

func (m *RuntimeManager) InstallCommand(hw HardwareKind) []string {
	python := "python3"
	if runtime.GOOS == "windows" {
		python = "py -3"
	}
	venv := m.VenvDir()
	if strings.ContainsAny(venv, " \t") {
		venv = fmt.Sprintf("%q", venv)
	}
	indexes := ResolvePackageIndexes(hw)
	cmd := []string{python, "-m", "venv", venv, "&&", m.PythonPath(), "-m", "ensurepip", "--upgrade", "&&", m.PythonPath(), "-m", "pip", "install", "--upgrade", "pip"}
	if indexes.PyPIIndexURL != "" {
		cmd = append(cmd, "-i", indexes.PyPIIndexURL)
	}
	cmd = append(cmd, "&&", m.PythonPath(), "-m", "pip", "install")
	cmd = append(cmd, torchPackageSpecs(hw, indexes)...)
	if indexes.TorchIndexURL != "" {
		cmd = append(cmd, "--index-url", indexes.TorchIndexURL)
	} else if indexes.PyPIIndexURL != "" {
		cmd = append(cmd, "-i", indexes.PyPIIndexURL)
	}
	if indexes.TorchFindLinksURL != "" {
		cmd = append(cmd, "--find-links", indexes.TorchFindLinksURL)
	}
	cmd = append(cmd, "&&", m.PythonPath(), "-m", "pip", "install", "diffusers", "transformers", "accelerate", "safetensors", "sentencepiece", "protobuf", "pillow")
	if indexes.PyPIIndexURL != "" {
		cmd = append(cmd, "-i", indexes.PyPIIndexURL)
	}
	return cmd
}

func DetectHardware() HardwareKind {
	switch runtime.GOOS {
	case "darwin":
		return HardwareMPS
	case "linux":
		if commandExists("nvidia-smi") {
			return HardwareCUDA
		}
		if commandExists("rocminfo") || commandExists("rocm-smi") || pathExists("/opt/rocm") {
			return HardwareROCm
		}
	case "windows":
		if commandExists("nvidia-smi") {
			return HardwareCUDA
		}
	}
	return HardwareCPU
}

func TorchIndexURL(hw HardwareKind) string {
	return torchSourceURL(ResolvePackageIndexes(hw))
}

func PyPIIndexURL() string {
	return ResolvePackageIndexes(DetectHardware()).PyPIIndexURL
}

func torchPackageSpecs(hw HardwareKind, indexes PackageIndexes) []string {
	if indexes.Mirror == PackageMirrorAliyun && hw == HardwareCUDA && indexes.TorchFindLinksURL != "" {
		return append([]string(nil), aliyunCUDATorchPackages...)
	}
	return append([]string(nil), defaultTorchPackages...)
}

func ResolvePackageIndexes(hw HardwareKind) PackageIndexes {
	mirror := ResolvePackageMirror()
	indexes := PackageIndexes{Mirror: mirror}
	switch mirror {
	case PackageMirrorAliyun:
		indexes.PyPIIndexURL = aliyunPyPIIndex
		switch hw {
		case HardwareCUDA:
			indexes.TorchFindLinksURL = aliyunTorchRoot + "/cu124"
		case HardwareROCm:
			indexes.TorchFindLinksURL = aliyunTorchRoot + "/rocm7.1"
		case HardwareCPU:
			indexes.TorchFindLinksURL = aliyunTorchRoot + "/cpu"
		case HardwareMPS:
			// macOS MPS wheels are published through PyPI; use the Aliyun PyPI
			// mirror rather than a CUDA/ROCm-specific PyTorch wheel index.
		}
	case PackageMirrorOfficial:
		switch hw {
		case HardwareCUDA:
			indexes.TorchIndexURL = officialTorchRoot + "/cu128"
		case HardwareROCm:
			indexes.TorchIndexURL = officialTorchRoot + "/rocm7.1"
		case HardwareCPU:
			indexes.TorchIndexURL = officialTorchRoot + "/cpu"
		case HardwareMPS:
			// macOS torch wheels are served from PyPI.
		}
	}
	if v := strings.TrimSpace(os.Getenv(torchIndexOverrideEnv)); v != "" {
		indexes.TorchIndexURL = v
		indexes.TorchFindLinksURL = ""
	}
	if v := strings.TrimSpace(os.Getenv(pypiIndexOverrideEnv)); v != "" {
		indexes.PyPIIndexURL = v
	}
	return indexes
}

func torchSourceURL(indexes PackageIndexes) string {
	if indexes.TorchIndexURL != "" {
		return indexes.TorchIndexURL
	}
	return indexes.TorchFindLinksURL
}

func ResolvePackageMirror() PackageMirror {
	switch normalizeMirrorValue(os.Getenv(mirrorModeEnv)) {
	case "aliyun", "cn", "china", "domestic":
		return PackageMirrorAliyun
	case "official", "global", "foreign", "overseas", "off":
		return PackageMirrorOfficial
	}

	switch normalizeMirrorValue(os.Getenv(regionEnv)) {
	case "cn", "china", "mainland":
		return PackageMirrorAliyun
	case "intl", "international", "global", "foreign", "overseas":
		return PackageMirrorOfficial
	}

	if isChinaLocale() {
		return PackageMirrorAliyun
	}
	return PackageMirrorAliyun
}

func normalizeMirrorValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isChinaLocale() bool {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LC_CTYPE", "LANG"} {
		if strings.Contains(strings.ToLower(os.Getenv(key)), "zh_cn") {
			return true
		}
	}
	tz := strings.ToLower(os.Getenv("TZ"))
	switch tz {
	case "asia/shanghai", "asia/chongqing", "asia/harbin", "asia/urumqi", "prc":
		return true
	}
	return false
}

func findHostPython() (string, error) {
	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"py", "python"}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			if runtime.GOOS == "windows" && candidate == "py" {
				return path, nil
			}
			return path, nil
		}
	}
	return "", errors.New("Python 3.10 or 3.11 is required to install the Diffusers runtime")
}

func missingPackages(ctx context.Context, python string, packages []string) ([]string, error) {
	script := `import importlib.util, json, sys
missing = [name for name in sys.argv[1:] if importlib.util.find_spec(name) is None]
print(json.dumps(missing))
`
	args := append([]string{"-c", script}, packages...)
	cmd := exec.CommandContext(ctx, python, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("checking Diffusers runtime packages: %w", err)
	}
	var missing []string
	if err := json.Unmarshal(out, &missing); err != nil {
		return nil, fmt.Errorf("decoding runtime package check: %w", err)
	}
	return missing, nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &output)
	cmd.Stderr = io.MultiWriter(os.Stderr, &output)
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(output.String())
		if len(msg) > 4096 {
			msg = msg[len(msg)-4096:]
		}
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeManifest(path string, manifest RuntimeManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
