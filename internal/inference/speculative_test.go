package inference

import (
	"reflect"
	"testing"
)

func TestNormalizeSpeculativeConfigCombination(t *testing.T) {
	pMin := 0.75
	got, err := NormalizeSpeculativeConfig(SpeculativeConfig{
		Types:      []string{"ngram-mod", "draft-eagle3", "ngram-mod"},
		DraftModel: "/models/eagle3.gguf",
		DraftNMax:  16,
		DraftPMin:  &pMin,
	})
	if err != nil {
		t.Fatalf("NormalizeSpeculativeConfig() error = %v", err)
	}
	wantTypes := []string{"draft-eagle3", "ngram-mod"}
	if !reflect.DeepEqual(got.Types, wantTypes) {
		t.Fatalf("types = %v, want %v", got.Types, wantTypes)
	}
	wantArgs := []string{
		"--spec-type", "draft-eagle3,ngram-mod",
		"--spec-draft-model", "/models/eagle3.gguf",
		"--spec-draft-n-max", "16",
		"--spec-draft-p-min", "0.75",
	}
	if args := got.llamaArgs(); !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("llamaArgs() = %v, want %v", args, wantArgs)
	}
}

func TestNormalizeSpeculativeConfigRejectsMultipleDraftMethods(t *testing.T) {
	_, err := NormalizeSpeculativeConfig(SpeculativeConfig{
		Types:      []string{"draft-mtp", "draft-eagle3"},
		DraftModel: "/models/eagle3.gguf",
	})
	if err == nil {
		t.Fatal("expected multiple draft methods to be rejected")
	}
}

func TestNormalizeSpeculativeConfigAllowsIntegratedMTP(t *testing.T) {
	got, err := NormalizeSpeculativeConfig(SpeculativeConfig{Types: []string{"draft-mtp"}})
	if err != nil {
		t.Fatalf("NormalizeSpeculativeConfig() error = %v", err)
	}
	if got.DraftModel != "" || !got.Enabled() {
		t.Fatalf("integrated MTP config = %#v", got)
	}
}

func TestNormalizeSpeculativeConfigRequiresDraftModel(t *testing.T) {
	_, err := NormalizeSpeculativeConfig(SpeculativeConfig{Types: []string{"draft-dflash"}})
	if err == nil {
		t.Fatal("expected draft-dflash without a draft model to fail")
	}
}
