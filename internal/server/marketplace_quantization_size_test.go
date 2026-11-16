package server

import (
	"testing"

	"github.com/opencsgs/csglite/internal/csghub"
)

func TestMarketplaceQuantizationSizeRequiresEveryShard(t *testing.T) {
	quants := summarizeMarketplaceQuantizations([]csghub.RepoFile{
		{Path: "model-Q8_0-00001-of-00002.gguf", Size: 1000},
		{Path: "model-Q8_0-00002-of-00002.gguf", Size: 0},
		{Path: "model-Q4_K_M.gguf", Size: 500},
		{Path: "mmproj-f16.gguf", Size: 200},
		{Path: "mtp-f16.gguf", Size: 300},
	})
	if len(quants) != 2 || quants[0].Name != "Q8_0" || quants[0].Size != 0 || quants[1].Size != 500 {
		t.Fatalf("unknown shards or auxiliary tensors corrupted weight sizes: %#v", quants)
	}
}
