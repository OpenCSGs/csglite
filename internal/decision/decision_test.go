package decision

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCard = "---\nbase_model: org/JEV-9B\ntags:\n- qwen3_5\n- typed-decisions\n---\n# card\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSystem1 writes a decision head with 2 noul, 6 score and 16 choice slots.
func writeSystem1(t *testing.T, dir string) {
	t.Helper()
	ids := make([]int, 24)
	bias := make([]float64, 24)
	verbalizers := []string{"false", "true", "0", "1", "2", "3", "4", "5"}
	for i := range ids {
		ids[i] = 1000 + i
		bias[i] = float64(i) / 100
	}
	for i := 0; i < 16; i++ {
		verbalizers = append(verbalizers, string(rune('A'+i)))
	}
	head, _ := json.Marshal(map[string]any{
		"bias":           bias,
		"verbalizer_ids": ids,
		"slots": map[string]any{
			"ranges":           map[string][2]int{"noul": {0, 2}, "score": {2, 8}, "choice": {8, 24}},
			"verbalizers":      verbalizers,
			"template_version": "bare-v1",
		},
	})
	writeFile(t, filepath.Join(dir, "adapter_vllm", "decision_head.json"), string(head))
	writeFile(t, filepath.Join(dir, "calibration.json"), `{"per_kind":{"noul":1.0,"choice":0.5,"score":2.0}}`)
}

func TestIsDecisionModelReadsCardTags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), testCard)
	if !IsDecisionModel(dir) {
		t.Fatal("typed-decisions tag not detected")
	}
	writeFile(t, filepath.Join(dir, "README.md"), "---\ntags:\n- JEV\n---\n")
	if !IsDecisionModel(dir) {
		t.Fatal("jev tag should match case-insensitively")
	}
	writeFile(t, filepath.Join(dir, "README.md"), "---\ntags:\n- jevons\n- text-generation\n---\n")
	if IsDecisionModel(dir) {
		t.Fatal("tags only containing jev as a substring must not match")
	}
}

func TestFindProfileFallsBackToLocalBaseModel(t *testing.T) {
	gguf := t.TempDir()
	writeFile(t, filepath.Join(gguf, "README.md"), testCard)

	if _, err := FindProfile(gguf, func(string) (string, bool) { return "", false }); !errors.Is(err, ErrNoSystem1) || !strings.Contains(err.Error(), "org/JEV-9B") {
		t.Fatalf("want ErrNoSystem1 naming the base model, got %v", err)
	}

	base := t.TempDir()
	writeSystem1(t, base)
	var asked string
	profile, err := FindProfile(gguf, func(repo string) (string, bool) { asked = repo; return base, true })
	if err != nil {
		t.Fatalf("FindProfile: %v", err)
	}
	if asked != "org/JEV-9B" || profile.AssetsDir != base {
		t.Fatalf("asked %q, assets %q", asked, profile.AssetsDir)
	}
	if profile.NativeChoiceOptions() != 16 || profile.Temperatures["choice"] != 0.5 || profile.Protocol() != "jev-bare-v1" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

func TestFindProfileRejectsMalformedHead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "adapter_vllm", "decision_head.json"),
		`{"bias":[0,0],"verbalizer_ids":[1,2],"slots":{"ranges":{"noul":[0,2],"score":[0,2],"choice":[0,2]}}}`)
	if _, err := FindProfile(dir, nil); err == nil {
		t.Fatal("expected score slot validation error")
	}
}

type fakeScorer struct {
	prompts []string
	ids     [][]int
	// logprob returns the read-out for token position i of a pass.
	logprob func(prompt string, i int) float64
}

func (f *fakeScorer) ScoreDecisionTokens(_ context.Context, prompt string, ids []int) (map[int]float64, int, error) {
	f.prompts = append(f.prompts, prompt)
	f.ids = append(f.ids, ids)
	out := map[int]float64{}
	for i, id := range ids {
		out[id] = f.logprob(prompt, i)
	}
	return out, 10, nil
}

func loadTestProfile(t *testing.T) *Profile {
	t.Helper()
	dir := t.TempDir()
	writeSystem1(t, dir)
	p, err := FindProfile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDecideBuildsTrainedPromptAndCalibratedDistribution(t *testing.T) {
	p := loadTestProfile(t)
	lp := []float64{-1.2, -0.4, -2.0}
	scorer := &fakeScorer{logprob: func(_ string, i int) float64 { return lp[i] }}

	resp, err := p.Decide(context.Background(), scorer, "org/JEV-9B-GGUF", Request{
		Kind:     "choice",
		State:    json.RawMessage(`"stock low"`),
		Question: "What now?",
		Options:  []string{"warn", "wait", "order"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	wantPrompt := "[kind] choice\n[state] stock low\n[question] What now?\n[options]\nA) warn\nB) wait\nC) order\n[decision]:"
	if scorer.prompts[0] != wantPrompt {
		t.Fatalf("prompt = %q, want %q", scorer.prompts[0], wantPrompt)
	}
	if got := scorer.ids[0]; len(got) != 3 || got[0] != 1008 || got[2] != 1010 {
		t.Fatalf("choice token ids = %v, want 1008..1010", got)
	}
	// softmax((lp + bias) / T) with choice bias 0.08.. and T = 0.5.
	z := []float64{(lp[0] + 0.08) / 0.5, (lp[1] + 0.09) / 0.5, (lp[2] + 0.10) / 0.5}
	want := softmax(z)
	for i := range want {
		if math.Abs(resp.Probabilities[i]-want[i]) > 1e-12 {
			t.Fatalf("probabilities = %v, want %v", resp.Probabilities, want)
		}
	}
	if resp.Choice != "wait" || resp.ChoiceIndex != 1 || resp.Adaptation != "native" || resp.NumModelRequests != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.TotalTokens != 11 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestDecideNoulAndScoreUseFixedOptions(t *testing.T) {
	p := loadTestProfile(t)
	scorer := &fakeScorer{logprob: func(string, int) float64 { return -1 }}
	resp, err := p.Decide(context.Background(), scorer, "m", Request{Kind: "noul", Question: "ok?", Options: []string{"ignored"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(resp.Options, ",") != "false,true" || !strings.HasSuffix(scorer.prompts[0], "[options]\nfalse\ntrue\n[decision]:") {
		t.Fatalf("noul options %v prompt %q", resp.Options, scorer.prompts[0])
	}
	resp, err = p.Decide(context.Background(), scorer, "m", Request{Kind: "score", Question: "how good?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Options) != 6 || scorer.ids[1][0] != 1002 {
		t.Fatalf("score options %v ids %v", resp.Options, scorer.ids[1])
	}
}

func TestDecideTournamentForMoreOptionsThanSlots(t *testing.T) {
	p := loadTestProfile(t)
	opts := make([]string, 20)
	for i := range opts {
		opts[i] = "city" + string(rune('a'+i))
	}
	target := "cityq" // index 16, in the second group
	scorer := &fakeScorer{logprob: func(prompt string, i int) float64 {
		label := string(rune('A' + i))
		if strings.Contains(prompt, label+") "+target+"\n") {
			return -0.01
		}
		return -6
	}}
	resp, err := p.Decide(context.Background(), scorer, "m", Request{Kind: "choice", Question: "which?", Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choice != target || resp.Adaptation != "tournament" || resp.NumModelRequests != 3 {
		t.Fatalf("choice %q adaptation %q requests %d", resp.Choice, resp.Adaptation, resp.NumModelRequests)
	}
	sum := 0.0
	for _, v := range resp.Probabilities {
		sum += v
	}
	if math.Abs(sum-1) > 1e-9 || len(resp.Probabilities) != 20 {
		t.Fatalf("probabilities sum %v over %d options", sum, len(resp.Probabilities))
	}
	if _, err := p.Decide(context.Background(), scorer, "m", Request{Kind: "choice", Question: "q", Options: opts, Strategy: "single"}); err == nil {
		t.Fatal("single strategy over 16 options should be refused")
	}
}

func TestDecideRejectsUnsupportedRequests(t *testing.T) {
	p := loadTestProfile(t)
	scorer := &fakeScorer{logprob: func(string, int) float64 { return -1 }}
	tests := []Request{
		{Kind: "rank", Question: "q"},
		{Kind: "noul"},
		{Kind: "choice", Question: "q", Options: []string{"only"}},
		{Kind: "noul", Question: "q", Thinking: "on"},
		{Kind: "noul", Question: "q", State: json.RawMessage(`["look", {"image": "data:image/png;base64,AA=="}]`)},
		{Kind: "noul", Question: "q", State: json.RawMessage(`42`)},
	}
	for _, req := range tests {
		_, err := p.Decide(context.Background(), scorer, "m", req)
		var reqErr *RequestError
		if !errors.As(err, &reqErr) {
			t.Fatalf("request %+v: want RequestError, got %v", req, err)
		}
	}
	if len(scorer.prompts) != 0 {
		t.Fatalf("invalid requests reached the model: %v", scorer.prompts)
	}
}

func TestStateTextMatchesPythonJSONDumps(t *testing.T) {
	tests := map[string]string{
		`{"b": 1, "a": [1.5, "é", true, null], "c": {"x": "q\"\n"}}`: `{"b": 1, "a": [1.5, "é", true, null], "c": {"x": "q\"\n"}}`,
		`{"n":2.0,"big":1e5,"tiny":1e-5,"i":-3}`:                     `{"n": 2.0, "big": 100000.0, "tiny": 1e-05, "i": -3}`,
		`"plain text"`:                                               `plain text`,
		`["Stock: ", {"type": "text", "text": "low"}, {"k": 1}]`:     `Stock: low{"k": 1}`,
	}
	for in, want := range tests {
		got, err := stateText(json.RawMessage(in))
		if err != nil {
			t.Fatalf("stateText(%s): %v", in, err)
		}
		if got != want {
			t.Fatalf("stateText(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitGroupsBalancesSizes(t *testing.T) {
	got := splitGroups(20, 16)
	if len(got) != 2 || len(got[0]) != 10 || len(got[1]) != 10 || got[1][0] != 10 {
		t.Fatalf("splitGroups(20,16) = %v", got)
	}
	if got := splitGroups(33, 16); len(got) != 3 || len(got[0]) != 11 {
		t.Fatalf("splitGroups(33,16) = %v", got)
	}
}
