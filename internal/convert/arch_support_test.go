package convert

import "testing"

func TestIsSupportedHFArchitecture(t *testing.T) {
	cases := map[string]string{
		"Qwen2ForCausalLM":                   "qwen2",
		"QwenForCausalLM":                    "qwen2",
		"ModernBertModel":                    "modernbert",
		"Idefics3ForConditionalGeneration":   "mmp",
		"Gemma4ForConditionalGeneration":     "gemma4",
		"BailingMoeV3ForCausalLM":            "bailingmoe3",
		"KimiK3ForConditionalGeneration":      "kimi-k3",
		"MuseGlimmerForConditionalGeneration": "muse-glimmer",
		"PocketTTSModel":                     "pockettts",
		"MiniMaxText01ForCausalLM":           "minimax-01",
		"MiniMaxM1ForCausalLM":               "minimax-01",
		"GraniteSWAForCausalLM":              "granite_swa",
		"GraniteMoeSWAForCausalLM":           "granite_swa",
		"GraniteSwitchForCausalLM":           "graniteswitch",
	}
	for arch, wantRuntimeArch := range cases {
		t.Run(arch, func(t *testing.T) {
			gotRuntimeArch, ok := SupportedHFArchitecture(arch)
			if !ok {
				t.Fatalf("%s should be supported", arch)
			}
			if gotRuntimeArch != wantRuntimeArch {
				t.Fatalf("runtime arch = %q, want %q", gotRuntimeArch, wantRuntimeArch)
			}
			if !IsSupportedHFArchitecture(arch) {
				t.Fatalf("%s should be supported", arch)
			}
		})
	}
	if IsSupportedHFArchitecture("UnknownModel") {
		t.Fatal("UnknownModel should not be supported")
	}
	if gotRuntimeArch, ok := SupportedHFArchitecture("UnknownModel"); ok || gotRuntimeArch != "" {
		t.Fatalf("unknown runtime arch = %q, ok=%v; want empty false", gotRuntimeArch, ok)
	}
}
