package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderModelAllowlistSaveLoadAndNormalize(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ResetProviderModelAllowlist()
	t.Cleanup(ResetProviderModelAllowlist)

	if err := ReplaceProviderModelAllowlist(" provider1 ", []string{" a ", "b", "a", ""}); err != nil {
		t.Fatalf("replace allowlist: %v", err)
	}
	got := GetProviderModelAllowlist("provider1")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("allowlist = %#v, want normalized a,b", got)
	}

	ResetProviderModelAllowlist()
	got = GetProviderModelAllowlist("provider1")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("loaded allowlist = %#v, want persisted a,b", got)
	}
}

func TestProviderModelAllowlistStoresDisplayNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ResetProviderModelAllowlist()
	t.Cleanup(ResetProviderModelAllowlist)

	if err := ReplaceProviderModelSelections(" provider1 ", []ProviderModelSelection{
		{Model: " a ", DisplayName: " Renamed A "},
		{Model: "b"},
		{Model: "a", DisplayName: "Duplicate"},
	}); err != nil {
		t.Fatalf("replace allowlist: %v", err)
	}
	got := GetProviderModelSelections("provider1")
	if len(got) != 2 || got[0].Model != "a" || got[0].DisplayName != "Renamed A" || got[1].Model != "b" || got[1].DisplayName != "" {
		t.Fatalf("selections = %#v, want renamed a and default b", got)
	}

	ResetProviderModelAllowlist()
	got = GetProviderModelSelections("provider1")
	if len(got) != 2 || got[0].DisplayName != "Renamed A" {
		t.Fatalf("loaded selections = %#v, want persisted display name", got)
	}
}

func TestProviderModelAllowlistRemoveAndDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ResetProviderModelAllowlist()
	t.Cleanup(ResetProviderModelAllowlist)

	if err := ReplaceProviderModelAllowlist("provider1", []string{"a", "b"}); err != nil {
		t.Fatalf("replace allowlist: %v", err)
	}
	removed, err := RemoveProviderModelAllowlist("provider1", "a")
	if err != nil {
		t.Fatalf("remove allowlist: %v", err)
	}
	if !removed {
		t.Fatal("removed = false, want true")
	}
	got := GetProviderModelAllowlist("provider1")
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("allowlist after remove = %#v, want b", got)
	}
	if err := DeleteProviderModelAllowlist("provider1"); err != nil {
		t.Fatalf("delete allowlist: %v", err)
	}
	if got := GetProviderModelAllowlist("provider1"); len(got) != 0 {
		t.Fatalf("allowlist after delete = %#v, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".csghub-lite", ProviderModelAllowlistFile)); err != nil {
		t.Fatalf("allowlist file was not persisted: %v", err)
	}
}
