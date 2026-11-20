package model

import "sort"

// The marketplace local-model-filter API asks the client to declare its rule
// vocabulary, so the server can classify models with the same tables the local
// runtime uses. These accessors expose the unexported tables without copying
// them: a copy would silently drift from the runtime rules.

// ASRModelFamilies returns the model families served by the Python ASR runtime.
func ASRModelFamilies() []string {
	return append([]string(nil), asrModelFamilies...)
}

// ASRArchitectures returns the HuggingFace architectures treated as ASR.
func ASRArchitectures() []string {
	return append([]string(nil), asrArchitectures...)
}

// TTSModelFamilies returns the model families served by the Python TTS runtime.
func TTSModelFamilies() []string {
	return append([]string(nil), ttsModelFamilies...)
}

// TTSArchitectures returns the HuggingFace architectures treated as TTS.
func TTSArchitectures() []string {
	return append([]string(nil), ttsArchitectures...)
}

// DiffusionGGUFArchitectures returns the GGUF architecture names written by
// diffusion converters. None of them is a llama.cpp text architecture.
func DiffusionGGUFArchitectures() []string {
	out := make([]string, 0, len(diffusionGGUFArchitectures))
	for arch := range diffusionGGUFArchitectures {
		out = append(out, arch)
	}
	sort.Strings(out)
	return out
}
