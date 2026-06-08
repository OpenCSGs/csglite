package inference

import "testing"

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	if opts.Temperature <= 0 || opts.Temperature > 1.0 {
		t.Errorf("Temperature = %f, want (0, 1.0]", opts.Temperature)
	}
	if opts.TopP <= 0 || opts.TopP > 1.0 {
		t.Errorf("TopP = %f, want (0, 1.0]", opts.TopP)
	}
	if opts.TopK <= 0 {
		t.Errorf("TopK = %d, want > 0", opts.TopK)
	}
	if opts.MaxTokens != -1 {
		t.Errorf("MaxTokens = %d, want -1", opts.MaxTokens)
	}
	if opts.NumCtx <= 0 {
		t.Errorf("NumCtx = %d, want > 0", opts.NumCtx)
	}
}
