package convert

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConvertLoRAQwen35ReordersVHeadsOnTheRightFactor(t *testing.T) {
	base := t.TempDir()
	writeTestJSON(t, filepath.Join(base, "config.json"), map[string]any{
		"architectures":          []string{"Qwen3_5ForCausalLM"},
		"hidden_size":            3,
		"num_hidden_layers":      1,
		"linear_num_key_heads":   2,
		"linear_num_value_heads": 4,
		"linear_key_head_dim":    1,
		"linear_value_head_dim":  1,
	})
	adapter := t.TempDir()
	writeTestJSON(t, filepath.Join(adapter, "adapter_config.json"), map[string]any{
		"peft_type": "LORA", "r": 2, "lora_alpha": 4,
	})
	// in_proj_z: W is (4 v-heads, hidden 3); out_proj: W is (hidden 3, 4 v-heads).
	tensors := map[string]testTensor{
		"base_model.model.model.layers.0.linear_attn.in_proj_z.lora_A.weight": seqTensor(2, 3),
		"base_model.model.model.layers.0.linear_attn.in_proj_z.lora_B.weight": seqTensor(4, 2),
		"base_model.model.model.layers.0.linear_attn.out_proj.lora_A.weight":  seqTensor(2, 4),
		"base_model.model.model.layers.0.linear_attn.out_proj.lora_B.weight":  seqTensor(3, 2),
		"base_model.model.model.layers.0.mlp.up_proj.lora_A.weight":           seqTensor(2, 3),
		"base_model.model.model.layers.0.mlp.up_proj.lora_B.weight":           seqTensor(5, 2),
		"base_model.model.lm_head.lora_A.weight":                              seqTensor(2, 3),
		"base_model.model.lm_head.lora_B.weight":                              seqTensor(7, 2),
	}
	writeTestSafeTensors(t, filepath.Join(adapter, "adapter_model.safetensors"), tensors)

	out := filepath.Join(t.TempDir(), "lora.gguf")
	if err := ConvertLoRA(adapter, base, out); err != nil {
		t.Fatalf("ConvertLoRA: %v", err)
	}
	kvs, got := readTestGGUF(t, out)

	if kvs["general.type"] != "adapter" || kvs["adapter.type"] != "lora" || kvs["general.architecture"] != "qwen35" {
		t.Fatalf("unexpected adapter metadata: %#v", kvs)
	}
	if kvs["adapter.lora.alpha"] != float32(4) {
		t.Fatalf("adapter.lora.alpha = %#v, want 4", kvs["adapter.lora.alpha"])
	}
	if len(got) != 8 {
		t.Fatalf("got %d tensors, want 8: %v", len(got), keys(got))
	}
	// Grouped V heads [G0v0 G0v1 G1v0 G1v1] become tiled [G0v0 G1v0 G0v1 G1v1].
	perm := []int{0, 2, 1, 3}
	zB := tensors["base_model.model.model.layers.0.linear_attn.in_proj_z.lora_B.weight"]
	assertTensor(t, got, "blk.0.attn_gate.weight.lora_b", []uint64{2, 4}, permuteRows(zB, perm))
	assertTensor(t, got, "blk.0.attn_gate.weight.lora_a", []uint64{3, 2}, tensors["base_model.model.model.layers.0.linear_attn.in_proj_z.lora_A.weight"].data)
	outA := tensors["base_model.model.model.layers.0.linear_attn.out_proj.lora_A.weight"]
	assertTensor(t, got, "blk.0.ssm_out.weight.lora_a", []uint64{4, 2}, permuteCols(outA, perm))
	assertTensor(t, got, "blk.0.ssm_out.weight.lora_b", []uint64{2, 3}, tensors["base_model.model.model.layers.0.linear_attn.out_proj.lora_B.weight"].data)
	assertTensor(t, got, "blk.0.ffn_up.weight.lora_b", []uint64{2, 5}, tensors["base_model.model.model.layers.0.mlp.up_proj.lora_B.weight"].data)
	assertTensor(t, got, "output.weight.lora_b", []uint64{2, 7}, tensors["base_model.model.lm_head.lora_B.weight"].data)
}

func TestConvertLoRARejectsUnsupportedAdapters(t *testing.T) {
	base := t.TempDir()
	writeTestJSON(t, filepath.Join(base, "config.json"), map[string]any{"architectures": []string{"Qwen3_5ForCausalLM"}})
	tests := []struct {
		name   string
		config map[string]any
		arch   string
		extra  map[string]testTensor
	}{
		{name: "dora", config: map[string]any{"peft_type": "LORA", "r": 2, "lora_alpha": 4, "use_dora": true}},
		{name: "rank pattern", config: map[string]any{"peft_type": "LORA", "r": 2, "lora_alpha": 4, "rank_pattern": map[string]any{"q_proj": 8}}},
		{name: "llama permutes", config: map[string]any{"peft_type": "LORA", "r": 2, "lora_alpha": 4}, arch: "LlamaForCausalLM"},
		{name: "missing half", config: map[string]any{"peft_type": "LORA", "r": 2, "lora_alpha": 4}, extra: map[string]testTensor{
			"base_model.model.model.layers.0.mlp.gate_proj.lora_A.weight": seqTensor(2, 3),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := base
			if tt.arch != "" {
				baseDir = t.TempDir()
				writeTestJSON(t, filepath.Join(baseDir, "config.json"), map[string]any{"architectures": []string{tt.arch}})
			}
			adapter := t.TempDir()
			writeTestJSON(t, filepath.Join(adapter, "adapter_config.json"), tt.config)
			tensors := map[string]testTensor{
				"base_model.model.model.layers.0.mlp.up_proj.lora_A.weight": seqTensor(2, 3),
				"base_model.model.model.layers.0.mlp.up_proj.lora_B.weight": seqTensor(5, 2),
			}
			for k, v := range tt.extra {
				tensors[k] = v
			}
			writeTestSafeTensors(t, filepath.Join(adapter, "adapter_model.safetensors"), tensors)
			if err := ConvertLoRA(adapter, baseDir, filepath.Join(t.TempDir(), "out.gguf")); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

type testTensor struct {
	rows, cols int
	data       []float32
}

func seqTensor(rows, cols int) testTensor {
	data := make([]float32, rows*cols)
	for i := range data {
		data[i] = float32(i + 1)
	}
	return testTensor{rows: rows, cols: cols, data: data}
}

func permuteRows(t testTensor, perm []int) []float32 {
	out := make([]float32, 0, len(t.data))
	for _, r := range perm {
		out = append(out, t.data[r*t.cols:(r+1)*t.cols]...)
	}
	return out
}

func permuteCols(t testTensor, perm []int) []float32 {
	out := make([]float32, 0, len(t.data))
	for r := 0; r < t.rows; r++ {
		for _, c := range perm {
			out = append(out, t.data[r*t.cols+c])
		}
	}
	return out
}

func writeTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestSafeTensors(t *testing.T, path string, tensors map[string]testTensor) {
	t.Helper()
	header := map[string]any{}
	var payload bytes.Buffer
	for _, name := range keysOf(tensors) {
		tt := tensors[name]
		start := payload.Len()
		if err := binary.Write(&payload, binary.LittleEndian, tt.data); err != nil {
			t.Fatal(err)
		}
		header[name] = map[string]any{
			"dtype":        "F32",
			"shape":        []int{tt.rows, tt.cols},
			"data_offsets": []int{start, payload.Len()},
		}
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	var file bytes.Buffer
	_ = binary.Write(&file, binary.LittleEndian, uint64(len(headerJSON)))
	file.Write(headerJSON)
	file.Write(payload.Bytes())
	if err := os.WriteFile(path, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type testGGUFTensor struct {
	dims []uint64
	data []float32
}

// readTestGGUF parses the subset of GGUF that ConvertLoRA writes: string,
// uint32 and float32 metadata and F32 tensors.
func readTestGGUF(t *testing.T, path string) (map[string]any, map[string]testGGUFTensor) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	var magic, version uint32
	var nTensors, nKV uint64
	for _, v := range []any{&magic, &version, &nTensors, &nKV} {
		if err := binary.Read(r, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	readString := func() string {
		var n uint64
		_ = binary.Read(r, binary.LittleEndian, &n)
		b := make([]byte, n)
		_, _ = io.ReadFull(r, b)
		return string(b)
	}
	kvs := map[string]any{}
	for i := uint64(0); i < nKV; i++ {
		key := readString()
		var typ uint32
		_ = binary.Read(r, binary.LittleEndian, &typ)
		switch typ {
		case ggufValString:
			kvs[key] = readString()
		case ggufValUint32:
			var v uint32
			_ = binary.Read(r, binary.LittleEndian, &v)
			kvs[key] = v
		case ggufValFloat32:
			var v float32
			_ = binary.Read(r, binary.LittleEndian, &v)
			kvs[key] = v
		default:
			t.Fatalf("unexpected metadata type %d for %s", typ, key)
		}
	}
	type info struct {
		name   string
		dims   []uint64
		offset uint64
	}
	var infos []info
	for i := uint64(0); i < nTensors; i++ {
		in := info{name: readString()}
		var nDims uint32
		_ = binary.Read(r, binary.LittleEndian, &nDims)
		in.dims = make([]uint64, nDims)
		_ = binary.Read(r, binary.LittleEndian, in.dims)
		var typ uint32
		_ = binary.Read(r, binary.LittleEndian, &typ)
		if GGMLType(typ) != GGMLTypeF32 {
			t.Fatalf("tensor %s has type %d, want F32", in.name, typ)
		}
		_ = binary.Read(r, binary.LittleEndian, &in.offset)
		infos = append(infos, in)
	}
	dataStart := alignUp(uint64(len(raw)-r.Len()), ggufAlignment)
	out := map[string]testGGUFTensor{}
	for _, in := range infos {
		n := uint64(1)
		for _, d := range in.dims {
			n *= d
		}
		data := make([]float32, n)
		start := dataStart + in.offset
		for i := range data {
			data[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[start+uint64(i)*4:]))
		}
		out[in.name] = testGGUFTensor{dims: in.dims, data: data}
	}
	return kvs, out
}

func assertTensor(t *testing.T, got map[string]testGGUFTensor, name string, dims []uint64, want []float32) {
	t.Helper()
	tensor, ok := got[name]
	if !ok {
		t.Fatalf("missing tensor %s; have %v", name, keys(got))
	}
	if !reflect.DeepEqual(tensor.dims, dims) {
		t.Fatalf("%s dims = %v, want %v", name, tensor.dims, dims)
	}
	if !reflect.DeepEqual(tensor.data, want) {
		t.Fatalf("%s data = %v, want %v", name, tensor.data, want)
	}
}

func keys(m map[string]testGGUFTensor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOf(m map[string]testTensor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
