package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/autostart"
	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/hardware"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

// float2 is a float64 that marshals to JSON with at most 2 decimal places.
type float2 float64

func (f float2) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%.2f", float64(f))), nil
}

type systemInfo struct {
	CPUCores          int    `json:"cpu_cores"`
	CPUUsage          float2 `json:"cpu_usage"`
	CPUClock          string `json:"cpu_clock"`
	RAMUsed           uint64 `json:"ram_used"`
	RAMTotal          uint64 `json:"ram_total"`
	RAMInfo           string `json:"ram_info"`
	GPUName           string `json:"gpu_name"`
	GPUVRAMUsed       uint64 `json:"gpu_vram_used"`
	GPUVRAMTotal      uint64 `json:"gpu_vram_total"`
	GPUUsageAvailable bool   `json:"gpu_usage_available"`
	GPUSharedMemory   bool   `json:"gpu_shared_memory"`
}

type gpuInfo struct {
	Name           string
	VRAMUsed       uint64
	VRAMTotal      uint64
	UsageAvailable bool
	SharedMemory   bool
}

type windowsCPUInfoPayload struct {
	Processors []windowsProcessorInfo `json:"Processors"`
	TotalUsage *float64               `json:"TotalUsage"`
}

type windowsProcessorInfo struct {
	Name           string   `json:"Name"`
	LoadPercentage *float64 `json:"LoadPercentage"`
	MaxClockSpeed  float64  `json:"MaxClockSpeed"`
}

type windowsMemoryInfo struct {
	TotalVisibleMemorySize uint64 `json:"TotalVisibleMemorySize"`
	FreePhysicalMemory     uint64 `json:"FreePhysicalMemory"`
}

// GET /api/settings -- application settings (version, model directory, etc.)
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentSettingsResponse(s.cfg, s.version))
}

// POST /api/settings -- update application settings (e.g., model directory)
func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req api.SettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var dirsUpdated bool
	var configUpdated bool
	storageDir := strings.TrimSpace(req.StorageDir)
	if storageDir != "" {
		storageDir = filepath.Clean(storageDir)
		s.cfg.ModelDir = config.ModelDirForStorage(storageDir)
		s.cfg.DatasetDir = config.DatasetDirForStorage(storageDir)
		dirsUpdated = true
		configUpdated = true
	} else {
		modelDir := strings.TrimSpace(req.ModelDir)
		datasetDir := strings.TrimSpace(req.DatasetDir)
		if modelDir != "" {
			s.cfg.ModelDir = filepath.Clean(modelDir)
			dirsUpdated = true
			configUpdated = true
		}
		if datasetDir != "" {
			s.cfg.DatasetDir = filepath.Clean(datasetDir)
			dirsUpdated = true
			configUpdated = true
		}
	}

	if req.WebSearch != nil {
		next, err := webSearchSettingsToConfig(*req.WebSearch)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.cfg.WebSearch = next
		configUpdated = true
	}
	if req.ServerURL != nil {
		serverURL := strings.TrimSpace(*req.ServerURL)
		if serverURL == "" {
			serverURL = config.DefaultServerURL
		}
		if serverURL != strings.TrimSpace(s.cfg.ServerURL) && strings.TrimSpace(s.cfg.Token) != "" {
			s.cfg.Token = ""
		}
		s.cfg.ServerURL = serverURL
		configUpdated = true
	}
	if req.AIGatewayURL != nil {
		s.cfg.AIGatewayURL = strings.TrimSpace(*req.AIGatewayURL)
		configUpdated = true
	}

	if dirsUpdated {
		if err := os.MkdirAll(s.cfg.ModelDir, 0o755); err != nil {
			writeError(w, http.StatusBadRequest, "invalid model directory: "+err.Error())
			return
		}
		if err := os.MkdirAll(s.cfg.DatasetDir, 0o755); err != nil {
			writeError(w, http.StatusBadRequest, "invalid dataset directory: "+err.Error())
			return
		}
	}
	if configUpdated {
		if err := config.Save(s.cfg); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error())
			return
		}
	}

	if req.Autostart != nil {
		if *req.Autostart {
			if err := autostart.Enable(); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to enable autostart: "+err.Error())
				return
			}
		} else {
			if err := autostart.Disable(); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to disable autostart: "+err.Error())
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, currentSettingsResponse(s.cfg, s.version))
}

func currentSettingsResponse(cfg *config.Config, version string) api.SettingsResponse {
	autostartEnabled, _ := autostart.IsEnabled()
	return api.SettingsResponse{
		Version:             version,
		StorageDir:          cfg.StorageDir(),
		ModelDir:            cfg.ModelDir,
		DatasetDir:          cfg.DatasetDir,
		ServerURL:           strings.TrimSpace(cfg.ServerURL),
		AIGatewayURL:        resolveCloudURL(cfg),
		DefaultServerURL:    config.DefaultServerURL,
		DefaultAIGatewayURL: cloud.DefaultBaseURL,
		Autostart:           autostartEnabled,
		WebSearch:           webSearchConfigToSettings(cfg.WebSearch),
	}
}

func webSearchConfigToSettings(cfg config.WebSearchConfig) api.WebSearchSettings {
	cfg = config.NormalizeWebSearchConfig(cfg)
	return api.WebSearchSettings{
		Enabled:        cfg.Enabled,
		MaxResults:     cfg.MaxResults,
		Language:       cfg.Language,
		Providers:      append([]string{}, cfg.Providers...),
		SafeSearch:     cfg.SafeSearch,
		TimeoutSeconds: cfg.TimeoutSeconds,
	}
}

func webSearchSettingsToConfig(settings api.WebSearchSettings) (config.WebSearchConfig, error) {
	next := config.WebSearchConfig{
		Enabled:        settings.Enabled,
		MaxResults:     settings.MaxResults,
		Language:       strings.TrimSpace(settings.Language),
		Providers:      normalizeWebSearchProviders(settings.Providers),
		SafeSearch:     settings.SafeSearch,
		TimeoutSeconds: settings.TimeoutSeconds,
	}
	return config.NormalizeWebSearchConfig(next), nil
}

func normalizeWebSearchProviders(providers []string) []string {
	out := make([]string, 0, len(providers))
	seen := map[string]struct{}{}
	for _, provider := range providers {
		provider = strings.ToLower(strings.TrimSpace(provider))
		switch provider {
		case "sogou", "baidu", "quark", "bing", "duckduckgo":
		default:
			continue
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		out = append(out, provider)
	}
	return out
}

// GET /api/system -- system resource information
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	info := systemInfo{
		CPUCores: runtime.NumCPU(),
	}

	cpuUsage, cpuClock := getCPUInfo()
	info.CPUUsage = float2(cpuUsage)
	info.CPUClock = cpuClock
	info.RAMUsed, info.RAMTotal, info.RAMInfo = getRAMInfo()
	gpu := getGPUInfo(info.RAMTotal)
	info.GPUName = gpu.Name
	info.GPUVRAMUsed = gpu.VRAMUsed
	info.GPUVRAMTotal = gpu.VRAMTotal
	info.GPUUsageAvailable = gpu.UsageAvailable
	info.GPUSharedMemory = gpu.SharedMemory

	writeJSON(w, http.StatusOK, info)
}

// POST /api/shutdown -- gracefully stop the local server
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})

	go func() {
		time.Sleep(100 * time.Millisecond)
		log.Println("shutting down server...")
		s.shutdownRuntime()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutCtx)
	}()
}

func getCPUInfo() (usage float64, clock string) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err == nil {
			clock = strings.TrimSpace(string(out))
		}
		// CPU usage via top
		out, err = exec.Command("sh", "-c", "top -l 1 -n 0 | grep 'CPU usage'").Output()
		if err == nil {
			line := string(out)
			if idx := strings.Index(line, "idle"); idx > 0 {
				parts := strings.Fields(line[:idx])
				if len(parts) > 0 {
					idle := strings.TrimSuffix(parts[len(parts)-1], "%")
					if v, e := strconv.ParseFloat(idle, 64); e == nil {
						usage = math.Round((100-v)*100) / 100
					}
				}
			}
		}
	case "linux":
		out, err := exec.Command("sh", "-c", "lscpu | grep 'Model name'").Output()
		if err == nil {
			parts := strings.SplitN(string(out), ":", 2)
			if len(parts) == 2 {
				clock = strings.TrimSpace(parts[1])
			}
		}
		out, err = exec.Command("sh", "-c", "grep 'cpu ' /proc/stat").Output()
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) >= 5 {
				user, _ := strconv.ParseFloat(fields[1], 64)
				system, _ := strconv.ParseFloat(fields[3], 64)
				idle, _ := strconv.ParseFloat(fields[4], 64)
				total := user + system + idle
				if total > 0 {
					usage = math.Round((user+system)/total*10000) / 100
				}
			}
		}
	case "windows":
		out, err := runPowerShellCommand(`$processors = @(Get-CimInstance Win32_Processor -ErrorAction SilentlyContinue | Select-Object Name, LoadPercentage, MaxClockSpeed); $total = Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq '_Total' } | Select-Object -ExpandProperty PercentProcessorTime -ErrorAction SilentlyContinue; [pscustomobject]@{ Processors = $processors; TotalUsage = $total } | ConvertTo-Json -Compress -Depth 3`)
		if err == nil {
			usage, clock = parseWindowsCPUInfo(out)
		}
	}
	return
}

func getRAMInfo() (used, total uint64, info string) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			total, _ = strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		}
		out, err = exec.Command("sh", "-c", "vm_stat | grep 'Pages active\\|Pages wired'").Output()
		if err == nil {
			var pages uint64
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					val := strings.TrimSuffix(fields[len(fields)-1], ".")
					if v, e := strconv.ParseUint(val, 10, 64); e == nil {
						pages += v
					}
				}
			}
			used = pages * 16384 // macOS page size is 16KB
		}
	case "linux":
		out, err := exec.Command("sh", "-c", "grep -E 'MemTotal|MemAvailable' /proc/meminfo").Output()
		if err == nil {
			var memTotal, memAvail uint64
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					val, _ := strconv.ParseUint(fields[1], 10, 64)
					val *= 1024 // kB to bytes
					if strings.HasPrefix(line, "MemTotal") {
						memTotal = val
					} else if strings.HasPrefix(line, "MemAvailable") {
						memAvail = val
					}
				}
			}
			total = memTotal
			used = memTotal - memAvail
		}
	case "windows":
		out, err := runPowerShellCommand(`Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue | Select-Object TotalVisibleMemorySize, FreePhysicalMemory | ConvertTo-Json -Compress`)
		if err == nil {
			used, total, info = parseWindowsRAMInfo(out)
		}
	}
	return
}

func runPowerShellCommand(script string) ([]byte, error) {
	powershell, err := exec.LookPath("powershell")
	if err != nil {
		return nil, err
	}
	script = "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false; " + script
	return exec.Command(
		powershell,
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		script,
	).Output()
}

func parseWindowsCPUInfo(out []byte) (usage float64, clock string) {
	var payload windowsCPUInfoPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return 0, ""
	}

	var loadTotal float64
	var loadCount int
	for _, processor := range payload.Processors {
		if clock == "" {
			clock = strings.TrimSpace(processor.Name)
			if clock == "" && processor.MaxClockSpeed > 0 {
				clock = formatProcessorClockMHz(processor.MaxClockSpeed)
			}
		}
		if processor.LoadPercentage != nil {
			loadTotal += *processor.LoadPercentage
			loadCount++
		}
	}

	if payload.TotalUsage != nil {
		return normalizeUsagePercent(*payload.TotalUsage), clock
	}
	if loadCount > 0 {
		return normalizeUsagePercent(loadTotal / float64(loadCount)), clock
	}
	return 0, clock
}

func parseWindowsRAMInfo(out []byte) (used, total uint64, info string) {
	var payload windowsMemoryInfo
	if err := json.Unmarshal(out, &payload); err != nil {
		return 0, 0, ""
	}

	total = payload.TotalVisibleMemorySize * 1024
	free := payload.FreePhysicalMemory * 1024
	if free > total {
		free = total
	}
	used = total - free
	return used, total, ""
}

func formatProcessorClockMHz(speedMHz float64) string {
	if speedMHz <= 0 {
		return ""
	}
	return fmt.Sprintf("%.2f GHz", speedMHz/1000)
}

func normalizeUsagePercent(value float64) float64 {
	switch {
	case value < 0:
		value = 0
	case value > 100:
		value = 100
	}
	return math.Round(value*100) / 100
}

func getGPUInfo(systemMemoryTotal uint64) gpuInfo {
	if info, ok := getNVIDIAGPUInfo(systemMemoryTotal); ok {
		return info
	}
	if runtime.GOOS == "darwin" {
		return getDarwinGPUInfo(systemMemoryTotal)
	}
	return gpuInfo{}
}

func getNVIDIAGPUInfo(systemMemoryTotal uint64) (gpuInfo, bool) {
	binary, err := hardware.ResolveNVIDIASMI()
	if err != nil {
		return gpuInfo{}, false
	}

	out, err := exec.Command(binary,
		"--query-gpu=name,memory.used,memory.total",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpuInfo{}, false
	}
	info, ok := parseNVIDIASMIOutput(out)
	if ok && info.SharedMemory && info.VRAMTotal == 0 {
		info.VRAMTotal = systemMemoryTotal
	}
	return info, ok
}

func parseNVIDIASMIOutput(out []byte) (gpuInfo, bool) {
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}

		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}

		usedField := strings.TrimSpace(fields[1])
		totalField := strings.TrimSpace(fields[2])
		usedMiB, errUsed := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		totalMiB, errTotal := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 64)
		if errUsed == nil && errTotal == nil {
			return gpuInfo{
				Name:           name,
				VRAMUsed:       usedMiB * 1024 * 1024,
				VRAMTotal:      totalMiB * 1024 * 1024,
				UsageAvailable: true,
			}, true
		}

		if memoryFieldUnsupported(usedField) || memoryFieldUnsupported(totalField) {
			return gpuInfo{
				Name:           name,
				UsageAvailable: false,
				SharedMemory:   isNVIDIAUnifiedMemoryGPU(name),
			}, true
		}
	}
	return gpuInfo{}, false
}

func getDarwinGPUInfo(systemMemoryTotal uint64) gpuInfo {
	out, err := exec.Command("system_profiler", "-json", "SPDisplaysDataType").Output()
	if err != nil {
		return gpuInfo{}
	}

	name, total, shared := parseDarwinSystemProfilerOutput(out)
	if total == 0 && shared {
		total = systemMemoryTotal
	}
	if name == "" && total == 0 {
		return gpuInfo{}
	}

	return gpuInfo{
		Name:           name,
		VRAMTotal:      total,
		UsageAvailable: false,
		SharedMemory:   shared,
	}
}

func parseDarwinSystemProfilerOutput(out []byte) (name string, total uint64, shared bool) {
	var payload struct {
		Displays []map[string]interface{} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", 0, false
	}

	for _, display := range payload.Displays {
		if name == "" {
			name = stringField(display, "sppci_model", "_name")
		}

		total = parseMemoryBytes(
			stringField(display, "spdisplays_vram"),
			stringField(display, "spdisplays_vram_shared"),
			stringField(display, "spdisplays_vram_dynamic"),
		)

		vendor := strings.ToLower(stringField(display, "spdisplays_vendor"))
		lowerName := strings.ToLower(name)
		if strings.Contains(vendor, "apple") || strings.Contains(lowerName, "apple") {
			shared = true
		}

		if name != "" || total > 0 {
			return name, total, shared
		}
	}

	return "", 0, false
}

func stringField(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		if text, ok := raw.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func parseMemoryBytes(values ...string) uint64 {
	for _, value := range values {
		if bytes := parseMemoryString(value); bytes > 0 {
			return bytes
		}
	}
	return 0
}

func parseMemoryString(value string) uint64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if idx := strings.LastIndex(value, ":"); idx >= 0 {
		value = strings.TrimSpace(value[idx+1:])
	}
	value = strings.ReplaceAll(value, ",", "")
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return 0
	}

	number, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}

	unit := strings.ToUpper(fields[1])
	switch {
	case strings.HasPrefix(unit, "TB"):
		return uint64(number * 1024 * 1024 * 1024 * 1024)
	case strings.HasPrefix(unit, "GB"):
		return uint64(number * 1024 * 1024 * 1024)
	case strings.HasPrefix(unit, "MB"):
		return uint64(number * 1024 * 1024)
	case strings.HasPrefix(unit, "KB"):
		return uint64(number * 1024)
	default:
		return 0
	}
}

func memoryFieldUnsupported(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "n/a" || value == "na" || strings.Contains(value, "not supported")
}

func isNVIDIAUnifiedMemoryGPU(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "gb10") ||
		strings.Contains(name, "grace blackwell") ||
		strings.Contains(name, "dgx spark")
}
