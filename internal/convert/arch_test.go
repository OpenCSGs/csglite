package convert

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestGetConverter(t *testing.T) {
	cfg := &modelConfig{}
	cases := []struct {
		arch     string
		wantType string
		wantNil  bool
	}{
		{"llama", "*convert.standardConverter", false},
		{"qwen2", "*convert.standardConverter", false},
		{"qwen35", "", true},
		{"qwen35moe", "", true},
		{"qwen3next", "", true},
		{"mamba", "*convert.mambaConverter", false},
		{"mamba2", "*convert.mamba2Converter", false},
		{"jamba", "*convert.jambaConverter", false},
		{"rwkv6", "*convert.rwkv6Converter", false},
		{"rwkv6qwen2", "*convert.rwkv6Converter", false},
		{"rwkv7", "*convert.rwkv7Converter", false},
		{"arwkv7", "*convert.rwkv7Converter", false},
		// Hybrid architectures
		{"falcon-h1", "*convert.hybridConverter", false},
		{"granitehybrid", "*convert.hybridConverter", false},
		{"nemotron_h", "*convert.hybridConverter", false},
		{"plamo2", "*convert.hybridConverter", false},
		{"lfm2", "*convert.hybridConverter", false},
		{"lfm2moe", "*convert.hybridConverter", false},
		{"kimi-linear", "*convert.hybridConverter", false},
		// Standard archs
		{"gemma", "*convert.standardConverter", false},
		{"phi3", "*convert.standardConverter", false},
		{"deepseek2", "*convert.standardConverter", false},
		{"gpt2", "*convert.standardConverter", false},
		{"bloom", "*convert.standardConverter", false},
		{"t5", "*convert.standardConverter", false},
	}
	for _, tc := range cases {
		c := getConverter(tc.arch, cfg)
		if tc.wantNil {
			if c != nil {
				t.Errorf("getConverter(%q): expected nil, got %T", tc.arch, c)
			}
			continue
		}
		if c == nil {
			t.Errorf("getConverter(%q): unexpected nil", tc.arch)
			continue
		}
		if c.Arch() != tc.arch {
			t.Errorf("getConverter(%q).Arch() = %q, want %q", tc.arch, c.Arch(), tc.arch)
		}
	}
}

func TestQwen35ArchitecturesFallbackToPython(t *testing.T) {
	cfg := &modelConfig{}
	for _, arch := range []string{"qwen35", "qwen35moe", "qwen3next"} {
		if got := getConverter(arch, cfg); got != nil {
			t.Fatalf("%s should currently fall back to Python, got %T", arch, got)
		}
	}
}

func TestDetectGGUFArch(t *testing.T) {
	cases := []struct {
		hfArch string
		want   string
		wantOK bool
	}{
		{"Qwen3_5ForCausalLM", "qwen35", true},
		{"Qwen3_5MoeForCausalLM", "qwen35moe", true},
		{"Qwen3NextForCausalLM", "qwen3next", true},
		{"MambaForCausalLM", "mamba", true},
		{"Mamba2ForCausalLM", "mamba2", true},
		{"JambaForCausalLM", "jamba", true},
		{"Rwkv6ForCausalLM", "rwkv6", true},
		{"Rwkv7ForCausalLM", "rwkv7", true},
		{"LlamaForCausalLM", "llama", true},
		// New mappings
		{"FalconH1ForCausalLM", "falcon-h1", true},
		{"GraniteMoeHybridForCausalLM", "granitehybrid", true},
		{"BambaForCausalLM", "granitehybrid", true},
		{"NemotronHForCausalLM", "nemotron_h", true},
		{"KimiLinearForCausalLM", "kimi-linear", true},
		{"KimiLinearModel", "kimi-linear", true},
		{"Plamo2ForCausalLM", "plamo2", true},
		{"PLaMo2ForCausalLM", "plamo2", true},
		{"Lfm2ForCausalLM", "lfm2", true},
		{"LFM2ForCausalLM", "lfm2", true},
		{"GPT2LMHeadModel", "gpt2", true},
		{"Llama4ForConditionalGeneration", "llama4", true},
		{"Gemma3ForConditionalGeneration", "gemma3", true},
		{"QWenLMHeadModel", "qwen2", true},
		{"T5ForConditionalGeneration", "t5", true},
		{"BloomModel", "bloom", true},
		{"DreamModel", "dream", true},
		{"HYV4ForCausalLM", "hy_v4", true},
		{"Spark2_5ForCausalLM", "spark2_5", true},
		{"Qwen4ExpForCausalLM", "qwen4exp", true},
		{"Qwen4ExpForConditionalGeneration", "qwen4exp", true},
		{"NemotronHPuzzleForCausalLM", "nemotron_h_moe", true},
		{"Dots3NoteForCausalLM", "dots3note", true},
		{"Dots3NoteTextForCausalLM", "dots3note", true},
		{"Dots3NoteForConditionalGeneration", "dots3note", true},
		{"UnknownArch", "", false},
	}
	for _, tc := range cases {
		got, ok := detectGGUFArch(tc.hfArch)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("detectGGUFArch(%q) = (%q, %v), want (%q, %v)", tc.hfArch, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestQwen35SSMTransform(t *testing.T) {
	// Test A_log → -exp(x)
	f32 := func(v float32) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, math.Float32bits(v))
		return b
	}
	readF32 := func(b []byte) float32 {
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	}

	// A_log transform
	input := f32(1.0) // exp(1.0) ≈ 2.718
	out := qwen35SSMTransform(input, "model.layers.0.linear_attn.A_log", GGMLTypeF32)
	got := readF32(out)
	expected := float32(-math.E)
	if math.Abs(float64(got-expected)) > 0.001 {
		t.Errorf("A_log transform: got %f, want %f", got, expected)
	}

	// norm.weight +1 transform
	input = f32(0.5)
	out = qwen35SSMTransform(input, "model.layers.0.input_layernorm.norm.weight", GGMLTypeF32)
	got = readF32(out)
	if math.Abs(float64(got-1.5)) > 0.001 {
		t.Errorf("norm +1 transform: got %f, want 1.5", got)
	}

	// SSM norm should NOT have +1 applied
	input = f32(0.5)
	out = qwen35SSMTransform(input, "model.layers.0.linear_attn.norm.weight", GGMLTypeF32)
	got = readF32(out)
	if math.Abs(float64(got-0.5)) > 0.001 {
		t.Errorf("SSM norm should not transform: got %f, want 0.5", got)
	}
}

func TestQwen35ReorderAxisBytes(t *testing.T) {
	// axis size = num_k_heads * num_v_per_k * head_dim = 2 * 2 * 1 = 4
	// old order: [k0v0, k0v1, k1v0, k1v1] -> new order: [k0v0, k1v0, k0v1, k1v1]
	data := make([]byte, 16)
	for i, v := range []float32{0, 1, 2, 3} {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
	}
	out := reorderAxisBytes(data, []int64{4}, 0, 2, 2, 1, GGMLTypeF32)
	expected := []float32{0, 2, 1, 3}
	for i, want := range expected {
		got := math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:]))
		if got != want {
			t.Errorf("reorderAxisBytes[%d] = %f, want %f", i, got, want)
		}
	}
}

func TestQwen35ReorderRowsSegment(t *testing.T) {
	// 6x1 matrix, reorder rows [2:6] with 2 key heads, 2 value-per-key, head_dim=1.
	// rows: [0, 1 | 2, 3, 4, 5] -> [0, 1 | 2, 4, 3, 5]
	data := make([]byte, 24)
	for i, v := range []float32{0, 1, 2, 3, 4, 5} {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
	}
	out := reorderRowsSegment(data, []int64{6, 1}, GGMLTypeF32, 2, 4, 2, 2, 1)
	expected := []float32{0, 1, 2, 4, 3, 5}
	for i, want := range expected {
		got := math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:]))
		if got != want {
			t.Errorf("reorderRowsSegment[%d] = %f, want %f", i, got, want)
		}
	}
}

func TestMambaNameMapper(t *testing.T) {
	mapper := mambaNameMapper()
	cases := []struct {
		input string
		want  string
	}{
		{"backbone.embedding.weight", "token_embd.weight"},
		{"backbone.layers.0.mixer.in_proj.weight", "blk.0.ssm_in.weight"},
		{"backbone.layers.0.mixer.conv1d.weight", "blk.0.ssm_conv1d.weight"},
		{"backbone.layers.0.mixer.x_proj.weight", "blk.0.ssm_x.weight"},
		{"backbone.layers.0.mixer.dt_proj.weight", "blk.0.ssm_dt.weight"},
		{"backbone.layers.0.mixer.A_log", "blk.0.ssm_a"},
		{"backbone.layers.0.mixer.D", "blk.0.ssm_d"},
		{"backbone.layers.0.mixer.out_proj.weight", "blk.0.ssm_out.weight"},
		{"backbone.layers.0.norm.weight", "blk.0.attn_norm.weight"},
		{"backbone.norm_f.weight", "output_norm.weight"},
		{"lm_head.weight", "output.weight"},
		// transformers naming
		{"model.layers.0.mixer.A_log", "blk.0.ssm_a"},
		{"model.embed_tokens.weight", "token_embd.weight"},
		{"model.norm.weight", "output_norm.weight"},
	}
	for _, tc := range cases {
		got := mapper(tc.input)
		if got != tc.want {
			t.Errorf("mambaNameMapper(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMamba2NameMapper(t *testing.T) {
	mapper := mamba2NameMapper()
	cases := []struct {
		input string
		want  string
	}{
		{"backbone.layers.0.mixer.dt_bias", "blk.0.ssm_dt.bias"},
		{"backbone.layers.0.mixer.norm.weight", "blk.0.ssm_norm.weight"},
	}
	for _, tc := range cases {
		got := mapper(tc.input)
		if got != tc.want {
			t.Errorf("mamba2NameMapper(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestRWKV6NameMapper(t *testing.T) {
	mapper := rwkv6NameMapper()
	cases := []struct {
		input string
		want  string
	}{
		{"rwkv.embeddings.weight", "token_embd.weight"},
		{"rwkv.blocks.0.pre_ln.weight", "blk.0.attn_norm.weight"},
		{"rwkv.ln_out.weight", "output_norm.weight"},
		{"rwkv.head.weight", "output.weight"},
	}
	for _, tc := range cases {
		got := mapper(tc.input)
		if got != tc.want {
			t.Errorf("rwkv6NameMapper(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTranspose2D(t *testing.T) {
	// 2x3 matrix of float32: [[1,2,3],[4,5,6]]
	data := make([]byte, 24)
	for i, v := range []float32{1, 2, 3, 4, 5, 6} {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
	}
	out := transpose2D(data, 2, 3, 4)
	// Expected: [[1,4],[2,5],[3,6]] → 3x2
	expected := []float32{1, 4, 2, 5, 3, 6}
	for i, want := range expected {
		got := math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:]))
		if got != want {
			t.Errorf("transpose2D[%d] = %f, want %f", i, got, want)
		}
	}
}

func TestPermute021(t *testing.T) {
	// 2x3x2 tensor of float32
	data := make([]byte, 48)
	for i := range 12 {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(float32(i)))
	}
	// permute(0,2,1): (2,3,2) → (2,2,3)
	out := permute021(data, 2, 3, 2, 4)
	// [0][0][0]=0, [0][0][1]=1, [0][1][0]=2, [0][1][1]=3, [0][2][0]=4, [0][2][1]=5
	// → [0][0][0]=0, [0][0][1]=2, [0][0][2]=4, [0][1][0]=1, [0][1][1]=3, [0][1][2]=5
	expected := []float32{0, 2, 4, 1, 3, 5, 6, 8, 10, 7, 9, 11}
	for i, want := range expected {
		got := math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:]))
		if got != want {
			t.Errorf("permute021[%d] = %f, want %f", i, got, want)
		}
	}
}

func TestPreTokenizerForArch(t *testing.T) {
	qwenArchs := []string{
		"Qwen3_5ForConditionalGeneration",
		"Qwen3_5ForCausalLM",
		"Qwen3_5MoeForConditionalGeneration",
		"Qwen3_5MoeForCausalLM",
		"Qwen3NextForCausalLM",
		"QWenLMHeadModel",
		"KimiLinearForCausalLM",
		"RWKV6Qwen2ForCausalLM",
	}
	for _, arch := range qwenArchs {
		got := preTokenizerForArch(arch)
		if got != "qwen2" {
			t.Errorf("preTokenizerForArch(%q) = %q, want %q", arch, got, "qwen2")
		}
	}

	rwkvArchs := []string{
		"Rwkv6ForCausalLM",
		"Rwkv7ForCausalLM",
		"RWKV7ForCausalLM",
		"RwkvHybridForCausalLM",
	}
	for _, arch := range rwkvArchs {
		got := preTokenizerForArch(arch)
		if got != "rwkv-world" {
			t.Errorf("preTokenizerForArch(%q) = %q, want %q", arch, got, "rwkv-world")
		}
	}

	if got := preTokenizerForArch("GPT2LMHeadModel"); got != "gpt2" {
		t.Errorf("preTokenizerForArch(GPT2LMHeadModel) = %q, want gpt2", got)
	}
	if got := preTokenizerForArch("ChatGLMModel"); got != "chatglm-bpe" {
		t.Errorf("preTokenizerForArch(ChatGLMModel) = %q, want chatglm-bpe", got)
	}
	if got := preTokenizerForArch("DeepseekV3ForCausalLM"); got != "deepseek-llm" {
		t.Errorf("preTokenizerForArch(DeepseekV3ForCausalLM) = %q, want deepseek-llm", got)
	}
	// Unknown should return default
	if got := preTokenizerForArch("FalconForCausalLM"); got != "default" {
		t.Errorf("preTokenizerForArch(FalconForCausalLM) = %q, want default", got)
	}
}

func TestChooseOutputType(t *testing.T) {
	if got := chooseOutputType("blk.0.attn_norm.weight", GGMLTypeF16, 1); got != GGMLTypeF32 {
		t.Errorf("norm tensor should be F32, got %v", got)
	}
	if got := chooseOutputType("blk.0.attn_q.weight", GGMLTypeBF16, 2); got != GGMLTypeF16 {
		t.Errorf("BF16 2D tensor should become F16, got %v", got)
	}
	if got := chooseOutputType("blk.0.attn_q.weight", GGMLTypeF16, 2); got != GGMLTypeF16 {
		t.Errorf("F16 2D tensor should stay F16, got %v", got)
	}
	if got := chooseOutputType("blk.0.attn_q.bias", GGMLTypeBF16, 1); got != GGMLTypeF32 {
		t.Errorf("1D bias tensor should be F32, got %v", got)
	}
	if got := chooseOutputType("blk.0.attn_q.bias", GGMLTypeF16, 1); got != GGMLTypeF32 {
		t.Errorf("1D F16 tensor should be F32, got %v", got)
	}
}

func TestConvertDtype(t *testing.T) {
	// BF16 value 0x3F80 = 1.0f in BF16
	bf16Data := []byte{0x80, 0x3F}
	f32Data := convertDtype(bf16Data, GGMLTypeBF16, GGMLTypeF32)
	if len(f32Data) != 4 {
		t.Fatalf("BF16→F32: expected 4 bytes, got %d", len(f32Data))
	}
	v := math.Float32frombits(binary.LittleEndian.Uint32(f32Data))
	if math.Abs(float64(v-1.0)) > 0.01 {
		t.Errorf("BF16→F32: got %f, want 1.0", v)
	}

	// F16 1.0 = 0x3C00 (IEEE half)
	f16One := []byte{0x00, 0x3C}
	f32FromF16 := convertDtype(f16One, GGMLTypeF16, GGMLTypeF32)
	if len(f32FromF16) != 4 {
		t.Fatalf("F16→F32: expected 4 bytes, got %d", len(f32FromF16))
	}
	v2 := math.Float32frombits(binary.LittleEndian.Uint32(f32FromF16))
	if math.Abs(float64(v2-1.0)) > 0.01 {
		t.Errorf("F16→F32: got %f, want 1.0", v2)
	}
}

func TestChooseOutputTypeForSSM(t *testing.T) {
	// SSM conv and A must be F32 for llama.cpp SSM_CONV / SSM_SCAN (see ggml-cpu ops.cpp).
	if got := chooseOutputTypeForSSM("blk.0.ssm_conv1d.weight", GGMLTypeBF16, 2); got != GGMLTypeF32 {
		t.Errorf("ssm_conv1d should be F32, got %v", got)
	}
	if got := chooseOutputTypeForSSM("blk.0.ssm_a", GGMLTypeF16, 2); got != GGMLTypeF32 {
		t.Errorf("ssm_a should be F32, got %v", got)
	}
	if got := chooseOutputTypeForSSM("blk.0.ssm_in.weight", GGMLTypeBF16, 2); got != GGMLTypeF16 {
		t.Errorf("ssm_in should stay F16 when source BF16, got %v", got)
	}
}

func TestHybridSSMTransform(t *testing.T) {
	f32 := func(v float32) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, math.Float32bits(v))
		return b
	}
	readF32 := func(b []byte) float32 {
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	}

	// A_log → -exp(x)
	input := f32(0.0) // -exp(0) = -1.0
	out := hybridSSMTransform(input, "model.layers.0.mamba.A_log", GGMLTypeF32, false)
	if got := readF32(out); math.Abs(float64(got+1.0)) > 0.001 {
		t.Errorf("A_log transform: got %f, want -1.0", got)
	}

	// norm.weight with normShift=true → +1.0
	input = f32(0.5)
	out = hybridSSMTransform(input, "model.layers.0.input_layernorm.norm.weight", GGMLTypeF32, true)
	if got := readF32(out); math.Abs(float64(got-1.5)) > 0.001 {
		t.Errorf("norm +1 transform: got %f, want 1.5", got)
	}

	// norm.weight with normShift=false → no change
	input = f32(0.5)
	out = hybridSSMTransform(input, "model.layers.0.input_layernorm.norm.weight", GGMLTypeF32, false)
	if got := readF32(out); math.Abs(float64(got-0.5)) > 0.001 {
		t.Errorf("norm without shift: got %f, want 0.5", got)
	}

	// mamba.norm should NOT have +1 even with normShift=true
	input = f32(0.5)
	out = hybridSSMTransform(input, "model.layers.0.mamba.norm.weight", GGMLTypeF32, true)
	if got := readF32(out); math.Abs(float64(got-0.5)) > 0.001 {
		t.Errorf("mamba.norm should not transform: got %f, want 0.5", got)
	}
}

func TestHybridNameMapping(t *testing.T) {
	// falcon-h1 SSM name mapping
	mapper := tensorNameMapper("falcon-h1")
	cases := []struct {
		input string
		want  string
	}{
		{"model.layers.0.mamba.in_proj.weight", "blk.0.ssm_in.weight"},
		{"model.layers.0.mamba.conv1d.weight", "blk.0.ssm_conv1d.weight"},
		{"model.layers.0.mamba.A_log", "blk.0.ssm_a"},
		{"model.layers.0.mamba.D", "blk.0.ssm_d"},
		{"model.layers.0.mamba.out_proj.weight", "blk.0.ssm_out.weight"},
		{"model.layers.0.mamba.dt_bias", "blk.0.ssm_dt.bias"},
		{"model.layers.0.mamba.norm.weight", "blk.0.ssm_norm.weight"},
		// Standard attention tensors should still work
		{"model.layers.0.self_attn.q_proj.weight", "blk.0.attn_q.weight"},
		{"model.layers.0.mlp.gate_proj.weight", "blk.0.ffn_gate.weight"},
	}
	for _, tc := range cases {
		got := mapper(tc.input)
		if got != tc.want {
			t.Errorf("falcon-h1 mapper(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	// kimi-linear SSM name mapping
	mapper = tensorNameMapper("kimi-linear")
	kimiCases := []struct {
		input string
		want  string
	}{
		{"model.layers.0.linear_attn.A_log", "blk.0.ssm_a"},
		{"model.layers.0.linear_attn.conv1d.weight", "blk.0.ssm_conv1d.weight"},
		{"model.layers.0.linear_attn.out_proj.weight", "blk.0.ssm_out.weight"},
	}
	for _, tc := range kimiCases {
		got := mapper(tc.input)
		if got != tc.want {
			t.Errorf("kimi-linear mapper(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestAllHybridArchitecturesHaveConverters(t *testing.T) {
	cfg := &modelConfig{}
	hybrids := []string{
		"falcon-h1", "granitehybrid", "nemotron_h",
		"plamo2", "lfm2", "lfm2moe", "kimi-linear",
	}
	for _, arch := range hybrids {
		c := getConverter(arch, cfg)
		if c == nil {
			t.Errorf("getConverter(%q) returned nil; hybrid should have Go converter", arch)
			continue
		}
		if c.Arch() != arch {
			t.Errorf("getConverter(%q).Arch() = %q", arch, c.Arch())
		}
	}
}

func TestMambaWriteKV(t *testing.T) {
	cfg := &modelConfig{
		HiddenSize:      768,
		NumHiddenLayers: 24,
		RmsNormEps:      1e-5,
		ModelType:       "mamba",
	}
	conv := &mambaConverter{cfg: cfg}
	w := newGGUFWriter()
	conv.WriteKV(w, cfg)

	findKV := func(key string) interface{} {
		for _, kv := range w.kvs {
			if kv.key == key {
				return kv.value
			}
		}
		return nil
	}

	if v := findKV("mamba.ssm.conv_kernel"); v != uint32(4) {
		t.Errorf("ssm.conv_kernel = %v, want 4", v)
	}
	if v := findKV("mamba.ssm.state_size"); v != uint32(16) {
		t.Errorf("ssm.state_size = %v, want 16", v)
	}
	if v := findKV("mamba.ssm.inner_size"); v != uint32(768*2) {
		t.Errorf("ssm.inner_size = %v, want %d", v, 768*2)
	}
	// dt_rank default = ceil(768/16) = 48
	if v := findKV("mamba.ssm.time_step_rank"); v != uint32(48) {
		t.Errorf("ssm.time_step_rank = %v, want 48", v)
	}
	if v := findKV("mamba.context_length"); v != uint32(1<<20) {
		t.Errorf("context_length = %v, want %d", v, 1<<20)
	}
}
