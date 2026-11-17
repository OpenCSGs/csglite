package hardware

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GPU is one visible NVIDIA accelerator as reported by nvidia-smi. Memory is in
// bytes; zero means the driver did not report it.
type GPU struct {
	Index       int
	Name        string
	MemoryTotal uint64
	MemoryUsed  uint64
}

// QueryNVIDIAGPUs lists the NVIDIA GPUs visible to this process. SGLang's
// default placement uses it to pick one GPU and size its KV pool instead of
// assuming every accelerator on the host belongs to the model.
func QueryNVIDIAGPUs() ([]GPU, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return QueryNVIDIAGPUsContext(ctx)
}

// QueryNVIDIAGPUsContext is QueryNVIDIAGPUs with a caller-provided deadline so
// a wedged driver cannot block a heartbeat or model listing.
func QueryNVIDIAGPUsContext(ctx context.Context) ([]GPU, error) {
	binary, err := ResolveNVIDIASMI()
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, binary,
		"--query-gpu=index,name,memory.used,memory.total",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	gpus := ParseNVIDIAGPUs(out)
	if len(gpus) == 0 {
		return nil, exec.ErrNotFound
	}
	return gpus, nil
}

// ParseNVIDIAGPUs parses the CSV emitted by QueryNVIDIAGPUs. Fields a driver
// does not support are left zero rather than guessed.
func ParseNVIDIAGPUs(out []byte) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil || fields[1] == "" {
			continue
		}
		gpu := GPU{Index: index, Name: fields[1]}
		if used, err := strconv.ParseUint(fields[2], 10, 64); err == nil {
			gpu.MemoryUsed = used << 20
		}
		if total, err := strconv.ParseUint(fields[3], 10, 64); err == nil {
			gpu.MemoryTotal = total << 20
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

func ResolveNVIDIASMI() (string, error) {
	if path, err := exec.LookPath("nvidia-smi"); err == nil {
		return path, nil
	}

	return firstExistingPath(nvidiaSMICandidates(runtime.GOOS, os.Getenv))
}

func nvidiaSMICandidates(goos string, getenv func(string) string) []string {
	candidates := []string{
		"/usr/bin/nvidia-smi",
		"/usr/local/nvidia/bin/nvidia-smi",
		"/opt/nvidia/bin/nvidia-smi",
	}
	if goos == "windows" {
		if programFiles := getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"))
		}
		if programFilesX86 := getenv("ProgramFiles(x86)"); programFilesX86 != "" {
			candidates = append(candidates, filepath.Join(programFilesX86, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"))
		}
	}
	return candidates
}

func firstExistingPath(candidates []string) (string, error) {
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}
