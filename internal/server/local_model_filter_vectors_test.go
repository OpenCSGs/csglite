package server

import (
	"encoding/json"
	"flag"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/localinference"
	"github.com/opencsgs/csglite/pkg/api"
)

// updateLocalModelFilterVectors refreshes the criteria snapshot in the vector
// file from marketplaceLocalModelFilterCapabilities(). The cases stay
// hand-authored: only the vocabulary the server is expected to consume is
// regenerated.
var updateLocalModelFilterVectors = flag.Bool("update-local-model-filter-vectors", false,
	"rewrite the criteria snapshot in testdata/local_model_filter_vectors.json")

type localModelFilterVectorFile struct {
	Version  int                            `json:"version"`
	Note     string                         `json:"note"`
	Hardware localModelFilterVectorHardware `json:"hardware"`
	Criteria json.RawMessage                `json:"criteria"`
	Cases    []localModelFilterVectorCase   `json:"cases"`
}

type localModelFilterVectorHardware struct {
	FreeDiskBytes int64 `json:"free_disk_bytes"`
	RAMBytes      int64 `json:"ram_bytes"`
}

type localModelFilterVectorCase struct {
	Name       string                         `json:"name"`
	Repository string                         `json:"repository"`
	Tags       []localModelFilterVectorTag    `json:"tags"`
	Metadata   localModelFilterVectorMetadata `json:"metadata"`
	Stats      localModelFilterVectorStats    `json:"stats"`
	LiteInput  localModelFilterVectorLiteIn   `json:"lite_input"`
	LiteExpect localModelFilterVectorLiteOut  `json:"lite_expect"`
}

type localModelFilterVectorTag struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

type localModelFilterVectorMetadata struct {
	Architecture     string  `json:"architecture"`
	ClassName        string  `json:"class_name"`
	ModelParams      float64 `json:"model_params"`
	ModelParamsValid bool    `json:"model_params_valid"`
	MiniGPUMemoryGB  float64 `json:"mini_gpu_memory_gb"`
	// Quantizations stays raw so the vectors can describe the malformed JSONB
	// shapes (object, string) the SQL must tolerate as well as the normal array.
	Quantizations json.RawMessage `json:"quantizations,omitempty"`
}

type localModelFilterVectorStats struct {
	TotalSize int64 `json:"total_size"`
}

type localModelFilterVectorLiteIn struct {
	Format       string `json:"format"`
	Architecture string `json:"architecture"`
	ClassName    string `json:"class_name"`
	LibraryName  string `json:"library_name"`
	PipelineTag  string `json:"pipeline_tag"`
}

type localModelFilterVectorLiteOut struct {
	Supported bool   `json:"supported"`
	Mode      string `json:"mode"`
}

func localModelFilterVectorPath() string {
	return filepath.Join("testdata", "local_model_filter_vectors.json")
}

func readLocalModelFilterVectors(t *testing.T) localModelFilterVectorFile {
	t.Helper()
	raw, err := os.ReadFile(localModelFilterVectorPath())
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var file localModelFilterVectorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return file
}

// TestLocalModelFilterVectorCriteriaSnapshot keeps the criteria block in the
// vector file generated from the live capability tables. Run
// `go test ./internal/server -run TestLocalModelFilterVectorCriteriaSnapshot -update-local-model-filter-vectors`
// after changing a vocabulary table.
func TestLocalModelFilterVectorCriteriaSnapshot(t *testing.T) {
	file := readLocalModelFilterVectors(t)
	caps := marketplaceLocalModelFilterCapabilities()
	want, err := json.MarshalIndent(caps, "", "  ")
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	if *updateLocalModelFilterVectors {
		file.Criteria = want
		raw, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			t.Fatalf("marshal vectors: %v", err)
		}
		if err := os.WriteFile(localModelFilterVectorPath(), append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("write vectors: %v", err)
		}
		return
	}
	var got any
	if err := json.Unmarshal(file.Criteria, &got); err != nil {
		t.Fatalf("parse criteria snapshot: %v", err)
	}
	var wantAny any
	if err := json.Unmarshal(want, &wantAny); err != nil {
		t.Fatalf("parse live capabilities: %v", err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(wantAny)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("criteria snapshot is stale; rerun with -update-local-model-filter-vectors")
	}
}

// TestLocalModelFilterVectors pins the Lite verdict for the vector cases. The
// expectation is the detail-page verdict, not the raw FromMarketplaceModel
// answer: the detail page also rejects non-text GGUF repositories, quantized
// repository names and models that do not fit.
func TestLocalModelFilterVectors(t *testing.T) {
	file := readLocalModelFilterVectors(t)
	limits := marketplaceLocalInferenceLimits{
		maxDownloadBytes: file.Hardware.FreeDiskBytes,
		maxMemoryBytes:   file.Hardware.RAMBytes,
		ramBytes:         file.Hardware.RAMBytes,
	}
	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			model := localModelFilterVectorModel(tc)
			support := localinference.FromMarketplaceModel(
				tc.LiteInput.Format,
				tc.LiteInput.Architecture,
				tc.LiteInput.ClassName,
				tc.Repository,
				tc.LiteInput.LibraryName,
				tc.LiteInput.PipelineTag,
			)
			if support.Supported && strings.EqualFold(tc.LiteInput.Format, "gguf") &&
				marketplaceLooksLikeNonTextGGUF(marketplaceModelRepoName(model)) {
				support = marketplaceVectorUnsupported(support)
			}
			if support.Supported && !strings.EqualFold(tc.LiteInput.Format, "gguf") &&
				marketplaceLooksLikeQuantizedWeights(marketplaceModelFullName(model)) {
				support = marketplaceVectorUnsupported(support)
			}
			if support.Supported && support.Mode != "direct" &&
				!marketplaceLocalInferenceSizeAllowed(support, model, limits) {
				support = marketplaceVectorUnsupported(support)
			}
			if support.Supported != tc.LiteExpect.Supported {
				t.Fatalf("supported = %v, want %v (support=%+v)", support.Supported, tc.LiteExpect.Supported, support)
			}
			if got := support.Mode; got != tc.LiteExpect.Mode {
				t.Fatalf("mode = %q, want %q", got, tc.LiteExpect.Mode)
			}
		})
	}
}

func marketplaceVectorUnsupported(support api.LocalInferenceSupport) api.LocalInferenceSupport {
	support.Supported = false
	support.Mode = "none"
	return support
}

func localModelFilterVectorModel(tc localModelFilterVectorCase) csghub.Model {
	valid := tc.Metadata.ModelParamsValid
	model := csghub.Model{
		Name:     path.Base(tc.Repository),
		Path:     tc.Repository,
		RepoSize: tc.Stats.TotalSize,
		Metadata: csghub.ModelMetadata{
			Architecture:     tc.Metadata.Architecture,
			ClassName:        tc.Metadata.ClassName,
			ModelParams:      tc.Metadata.ModelParams,
			ModelParamsValid: &valid,
			MiniGPUMemoryGB:  tc.Metadata.MiniGPUMemoryGB,
		},
	}
	for _, tag := range tc.Tags {
		model.Tags = append(model.Tags, csghub.Tag{Name: tag.Name, Category: tag.Category})
	}
	return model
}
