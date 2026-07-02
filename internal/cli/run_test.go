package cli

import "testing"

func TestNewRunCmdExposesContextFlags(t *testing.T) {
	cmd := newRunCmd()

	if f := cmd.Flags().Lookup("num-ctx"); f == nil {
		t.Fatal("expected --num-ctx flag")
	}
	if f := cmd.Flags().Lookup("num-parallel"); f == nil {
		t.Fatal("expected --num-parallel flag")
	}
	if f := cmd.Flags().Lookup("n-gpu-layers"); f == nil {
		t.Fatal("expected --n-gpu-layers flag")
	}
	if f := cmd.Flags().Lookup("cache-type-k"); f == nil {
		t.Fatal("expected --cache-type-k flag")
	}
	if f := cmd.Flags().Lookup("cache-type-v"); f == nil {
		t.Fatal("expected --cache-type-v flag")
	}
	if f := cmd.Flags().Lookup("dtype"); f == nil {
		t.Fatal("expected --dtype flag")
	}
	if f := cmd.Flags().Lookup("keep-alive"); f == nil {
		t.Fatal("expected --keep-alive flag")
	}
}

func TestValidateInteractiveModelOverrides(t *testing.T) {
	tests := []struct {
		name        string
		numCtx      int
		numParallel int
		nGPULayers  int
		cacheTypeK  string
		cacheTypeV  string
		dtype       string
		wantErr     bool
	}{
		{name: "unset", numCtx: 0, numParallel: 0, nGPULayers: -1},
		{name: "valid explicit overrides", numCtx: 131072, numParallel: 1, nGPULayers: 40, cacheTypeK: "q8_0", cacheTypeV: "bf16", dtype: "q8_0"},
		{name: "valid gguf quant dtype", dtype: "q4_k_m"},
		{name: "reject too small ctx", numCtx: 512, numParallel: 0, wantErr: true},
		{name: "reject negative parallel", numCtx: 0, numParallel: -1, nGPULayers: -1, wantErr: true},
		{name: "reject invalid n gpu layers", nGPULayers: -2, wantErr: true},
		{name: "reject invalid cache type k", cacheTypeK: "fp8", wantErr: true},
		{name: "reject invalid cache type v", cacheTypeV: "int8", wantErr: true},
		{name: "reject invalid dtype", dtype: "q9_x", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInteractiveModelOverrides(tt.numCtx, tt.numParallel, tt.nGPULayers, tt.cacheTypeK, tt.cacheTypeV, tt.dtype)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateInteractiveModelOverrides(%d, %d, %d, %q, %q, %q) error = %v, wantErr %v", tt.numCtx, tt.numParallel, tt.nGPULayers, tt.cacheTypeK, tt.cacheTypeV, tt.dtype, err, tt.wantErr)
			}
		})
	}
}

func TestValidateRunOverrides(t *testing.T) {
	tests := []struct {
		name      string
		keepAlive string
		wantErr   bool
	}{
		{name: "unset"},
		{name: "forever", keepAlive: "-1"},
		{name: "duration", keepAlive: "1h"},
		{name: "reject invalid keep alive", keepAlive: "later", wantErr: true},
		{name: "reject unsupported negative keep alive", keepAlive: "-5m", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRunOverrides(0, 0, -1, "", "", "", tt.keepAlive)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRunOverrides(keepAlive=%q) error = %v, wantErr %v", tt.keepAlive, err, tt.wantErr)
			}
		})
	}
}

func TestConvertStatusMessageUsesEffectiveDType(t *testing.T) {
	got := convertStatusMessage("")
	want := "Converting model to GGUF format (output dtype: f16, first time only, this may take a moment)..."
	if got != want {
		t.Fatalf("convertStatusMessage(\"\") = %q, want %q", got, want)
	}

	got = convertStatusMessage("Q8_0")
	want = "Converting model to GGUF format (output dtype: q8_0, first time only, this may take a moment)..."
	if got != want {
		t.Fatalf("convertStatusMessage(\"Q8_0\") = %q, want %q", got, want)
	}
}
