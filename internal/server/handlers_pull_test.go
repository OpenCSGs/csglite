package server

import (
	"testing"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestPullProgressResponseCarriesSpeed(t *testing.T) {
	p := csghub.SnapshotProgress{
		FileName:          "model-00001-of-00002.safetensors",
		BytesTotal:        40,
		BytesCompleted:    12,
		BytesTotalAll:     100,
		BytesCompletedAll: 30,
	}
	got := pullProgressResponse(p, 1536.9)
	want := api.PullResponse{
		Status:           "downloading model-00001-of-00002.safetensors",
		Digest:           "model-00001-of-00002.safetensors",
		Total:            40,
		Completed:        12,
		TotalBytes:       100,
		CompletedBytes:   30,
		SpeedBytesPerSec: 1536,
	}
	if got != want {
		t.Fatalf("response = %+v, want %+v", got, want)
	}
}
