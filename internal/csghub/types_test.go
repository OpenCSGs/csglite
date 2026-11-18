package csghub

import (
	"encoding/json"
	"testing"
)

func TestModelDeserialization(t *testing.T) {
	raw := `{
		"id": 367,
		"name": "csg-wukong-1B",
		"nickname": "csg-wukong-1B",
		"description": "test model",
		"likes": 12,
		"downloads": 16846,
		"path": "OpenCSG/csg-wukong-1B",
		"repository_id": 818,
		"private": false,
		"license": "apache-2.0",
		"default_branch": "main",
		"tags": [{"name": "safetensors", "category": "framework", "built_in": true}],
		"repository": {
			"http_clone_url": "https://opencsg.com/models/OpenCSG/csg-wukong-1B.git",
			"ssh_clone_url": "git@hub.opencsg.com:models/OpenCSG/csg-wukong-1B.git"
		},
		"metadata": {
			"model_params": 1.1,
			"tensor_type": "F16",
			"architecture": "LlamaForCausalLM"
		}
	}`

	var m Model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if m.ID != 367 {
		t.Errorf("ID = %d, want 367", m.ID)
	}
	if m.Name != "csg-wukong-1B" {
		t.Errorf("Name = %q, want %q", m.Name, "csg-wukong-1B")
	}
	if m.Path != "OpenCSG/csg-wukong-1B" {
		t.Errorf("Path = %q, want %q", m.Path, "OpenCSG/csg-wukong-1B")
	}
	if m.Downloads != 16846 {
		t.Errorf("Downloads = %d, want 16846", m.Downloads)
	}
	if m.License != "apache-2.0" {
		t.Errorf("License = %q, want %q", m.License, "apache-2.0")
	}
	if m.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", m.DefaultBranch, "main")
	}
	if len(m.Tags) != 1 {
		t.Fatalf("Tags len = %d, want 1", len(m.Tags))
	}
	if m.Tags[0].Name != "safetensors" {
		t.Errorf("Tags[0].Name = %q, want %q", m.Tags[0].Name, "safetensors")
	}
	if m.Metadata.Architecture != "LlamaForCausalLM" {
		t.Errorf("Metadata.Architecture = %q, want %q", m.Metadata.Architecture, "LlamaForCausalLM")
	}
}

func TestRepoFileDeserialization(t *testing.T) {
	raw := `{
		"name": "model.safetensors",
		"type": "file",
		"size": 2200119664,
		"path": "model.safetensors",
		"sha": "c796d9a6d0677968fbe2df54fe1411ae44268b43",
		"lfs": true,
		"lfs_sha256": "c937f6bbe8b1461b1b402195d6b906b1c88ac6b852ef656569314136beab4748",
		"lfs_pointer_size": 135,
		"lfs_relative_path": "c9/37/f6bbe8b1461b1b402195d6b906b1c88ac6b852ef656569314136beab4748"
	}`

	var f RepoFile
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if f.Name != "model.safetensors" {
		t.Errorf("Name = %q, want %q", f.Name, "model.safetensors")
	}
	if !f.LFS {
		t.Error("LFS = false, want true")
	}
	if f.Size != 2200119664 {
		t.Errorf("Size = %d, want 2200119664", f.Size)
	}
	if f.LFSSHA256 != "c937f6bbe8b1461b1b402195d6b906b1c88ac6b852ef656569314136beab4748" {
		t.Errorf("LFSSHA256 = %q", f.LFSSHA256)
	}
}

func TestAPIResponseDeserialization(t *testing.T) {
	raw := `{"msg": "OK", "data": {"id": 1, "name": "test"}}`
	var resp APIResponse[Model]
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if resp.Msg != "OK" {
		t.Errorf("Msg = %q, want %q", resp.Msg, "OK")
	}
	if resp.Data.Name != "test" {
		t.Errorf("Data.Name = %q, want %q", resp.Data.Name, "test")
	}
}

func TestListResponseDeserialization(t *testing.T) {
	raw := `{"msg": "OK", "data": [{"id": 1, "name": "a"}, {"id": 2, "name": "b"}], "total": 100}`
	var resp ListResponse[Model]
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if resp.Total != 100 {
		t.Errorf("Total = %d, want 100", resp.Total)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("Data len = %d, want 2", len(resp.Data))
	}
}
