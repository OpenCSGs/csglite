package ggufpick

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// quantRanks: higher value = higher numerical precision / less aggressive quantization.
// Unknown tokens return -1 from quantRankFromStem.
var quantRanks = map[string]int{
	"f32":     1000,
	"bf16":    990,
	"f16":     980,
	"fp16":    980,
	"q8_0":    920,
	"q8_1":    915,
	"q6_k":    880,
	"q5_k_m":  860,
	"q5_k_s":  855,
	"q5_k":    850,
	"q5_1":    840,
	"q5_0":    835,
	"q4_k_m":  800,
	"q4_k_s":  795,
	"q4_k":    790,
	"q4_1":    785,
	"q4_0":    780,
	"q3_k_l":  750,
	"q3_k_m":  745,
	"q3_k_s":  740,
	"q3_k_xl": 738,
	"q3_k":    735,
	"q2_k":    700,
	"tq2_0":   680,
	"tq1_0":   670,
	"iq4_nl":  650,
	"iq4_xs":  640,
	"iq3_m":   620,
	"iq3_s":   610,
	"iq3_xs":  600,
	"iq3_xxs": 590,
	"iq2_m":   570,
	"iq2_xs":  560,
	"iq2_xxs": 550,
	"iq1_m":   520,
	"iq1_s":   510,
}

var shardSuffixRe = regexp.MustCompile(`-\d+-of-\d+$`)

// KnownQuantLabels returns the GGUF quantization labels recognized by the picker,
// ordered from higher precision to lower precision.
func KnownQuantLabels() []string {
	labels := make([]string, 0, len(quantRanks))
	for label := range quantRanks {
		labels = append(labels, strings.ToUpper(label))
	}
	sort.Slice(labels, func(i, j int) bool {
		li := strings.ToLower(labels[i])
		lj := strings.ToLower(labels[j])
		if quantRanks[li] == quantRanks[lj] {
			return labels[i] < labels[j]
		}
		return quantRanks[li] > quantRanks[lj]
	})
	return labels
}

// IsMMProjGGUF reports whether name looks like a multimodal projector GGUF.
func IsMMProjGGUF(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".gguf") && strings.Contains(lower, "mmproj")
}

// IsWeightGGUF is a non-mmproj .gguf file (main model weights).
func IsWeightGGUF(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	if !strings.HasSuffix(lower, ".gguf") {
		return false
	}
	return !strings.Contains(lower, "mmproj")
}

// QuantRank returns a precision rank for a weight GGUF basename; higher is better.
// Returns -1 if no known quantization token is found.
func QuantRank(basename string) int {
	stem := normalizeGGUFStem(filepath.Base(basename))
	if stem == "" {
		return -1
	}
	return quantRankFromStem(stem)
}

// QuantRankFromRepoPath ranks using the repo-relative path: the filename is used first;
// if it has no known quant token, any parent directory whose name matches a quant (e.g. Q8_0/) is used.
// This supports layouts like Q8_0/model.gguf vs Q4_0/model.gguf.
func QuantRankFromRepoPath(relPath string) int {
	// Repo tree paths from git APIs use '/'; normalize '\' so tests and Windows paths work.
	relPath = strings.ReplaceAll(relPath, `\`, `/`)
	relPath = filepath.ToSlash(relPath)
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return -1
	}
	parts := strings.Split(relPath, "/")
	if len(parts) == 0 {
		return -1
	}
	base := parts[len(parts)-1]
	fromName := QuantRank(base)
	if fromName >= 0 {
		return fromName
	}
	maxDir := -1
	for _, seg := range parts[:len(parts)-1] {
		seg = strings.ToLower(strings.TrimSpace(seg))
		if seg == "" {
			continue
		}
		if r, ok := quantRanks[seg]; ok && r > maxDir {
			maxDir = r
		}
	}
	return maxDir
}

// QuantLabelFromRepoPath extracts the canonical GGUF quantization label from a repo-relative path.
// The filename takes precedence; if it has no known quant token, parent directories are considered.
func QuantLabelFromRepoPath(relPath string) string {
	// Repo tree paths from git APIs use '/'; normalize '\' so tests and Windows paths work.
	relPath = strings.ReplaceAll(relPath, `\`, `/`)
	relPath = filepath.ToSlash(relPath)
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return ""
	}
	parts := strings.Split(relPath, "/")
	if len(parts) == 0 {
		return ""
	}
	base := parts[len(parts)-1]
	if label := QuantLabel(base); label != "" {
		return label
	}
	best := ""
	bestRank := -1
	for _, seg := range parts[:len(parts)-1] {
		seg = strings.ToLower(strings.TrimSpace(seg))
		if seg == "" {
			continue
		}
		if rank, ok := quantRanks[seg]; ok && rank > bestRank {
			bestRank = rank
			best = strings.ToUpper(seg)
		}
	}
	return best
}

func normalizeGGUFStem(basename string) string {
	lower := strings.ToLower(basename)
	if !strings.HasSuffix(lower, ".gguf") {
		return ""
	}
	stem := basename[:len(basename)-len(".gguf")]
	stem = strings.ToLower(stem)
	stem = shardSuffixRe.ReplaceAllString(stem, "")
	// Some repos separate the quant token with dots instead of hyphens
	// (e.g. Qwen.Qwen3-VL-Embedding-2B.Q5_K_M.gguf); normalize so the
	// hyphen-based tokenizer below can find the quant label.
	stem = strings.ReplaceAll(stem, ".", "-")
	return stem
}

// QuantLabel returns the canonical GGUF quantization label from a weight filename.
func QuantLabel(basename string) string {
	stem := normalizeGGUFStem(filepath.Base(basename))
	if stem == "" {
		return ""
	}
	return quantLabelFromStem(stem)
}

func quantRankFromStem(stem string) int {
	tokens := strings.Split(stem, "-")
	if len(tokens) == 0 {
		return -1
	}
	// Try last 1..3 tokens joined with underscores (e.g. q8_0, q4_k_m).
	for n := 3; n >= 1; n-- {
		if len(tokens) < n {
			continue
		}
		cand := strings.Join(tokens[len(tokens)-n:], "_")
		if r, ok := quantRanks[cand]; ok {
			return r
		}
	}
	return -1
}

func quantLabelFromStem(stem string) string {
	tokens := strings.Split(stem, "-")
	if len(tokens) == 0 {
		return ""
	}
	// Try last 1..3 tokens joined with underscores (e.g. q8_0, q4_k_m).
	for n := 3; n >= 1; n-- {
		if len(tokens) < n {
			continue
		}
		cand := strings.Join(tokens[len(tokens)-n:], "_")
		if _, ok := quantRanks[cand]; ok {
			return strings.ToUpper(cand)
		}
	}
	return ""
}

// FileEntry is a minimal file description for GGUF download filtering.
type FileEntry struct {
	Path string
	Name string
	Size int64
}

// FilterWeightGGUFFiles keeps every shard of the highest-known-precision variant.
// If there is at most one weight file, or no file has a known quant token, entries are returned unchanged.
func FilterWeightGGUFFiles(entries []FileEntry) []FileEntry {
	if len(entries) <= 1 {
		return entries
	}
	ranks := make([]int, len(entries))
	maxRank := -1
	known := false
	for i, e := range entries {
		p := e.Path
		if p == "" {
			p = e.Name
		}
		r := QuantRankFromRepoPath(p)
		ranks[i] = r
		if r >= 0 {
			known = true
			if r > maxRank {
				maxRank = r
			}
		}
	}
	if !known {
		return entries
	}
	var out []FileEntry
	for i, e := range entries {
		if ranks[i] == maxRank {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return entries
	}
	return out
}

// FilterWeightGGUFFilesByQuant keeps every shard whose GGUF quantization label matches want (case-insensitive).
// want must be non-empty. Matching uses QuantLabelFromRepoPath on each entry path.
func FilterWeightGGUFFilesByQuant(entries []FileEntry, want string) []FileEntry {
	want = strings.TrimSpace(want)
	if want == "" || len(entries) == 0 {
		return nil
	}
	var out []FileEntry
	for _, e := range entries {
		p := e.Path
		if p == "" {
			p = e.Name
		}
		label := QuantLabelFromRepoPath(p)
		if label != "" && strings.EqualFold(label, want) {
			out = append(out, e)
		}
	}
	return out
}

// BestWeightGGUFName picks the highest-precision weight GGUF basename.
// Tie-breaker: lexicographic order on name for stability.
func BestWeightGGUFName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return names[0]
	}
	best := names[0]
	bestR := QuantRank(best)
	for _, n := range names[1:] {
		r := QuantRank(n)
		if r > bestR || (r == bestR && n < best) {
			best = n
			bestR = r
		}
	}
	return best
}

// BestWeightGGUFRelPath picks the highest-precision file by repo-relative or filesystem-relative path.
func BestWeightGGUFRelPath(relPaths []string) string {
	if len(relPaths) == 0 {
		return ""
	}
	if len(relPaths) == 1 {
		return relPaths[0]
	}
	best := relPaths[0]
	bestR := QuantRankFromRepoPath(best)
	for _, p := range relPaths[1:] {
		r := QuantRankFromRepoPath(p)
		pSlash := filepath.ToSlash(p)
		bestSlash := filepath.ToSlash(best)
		if r > bestR || (r == bestR && pSlash < bestSlash) {
			best = p
			bestR = r
		}
	}
	return best
}

// CollectWeightGGUFRelPaths returns relative paths to weight .gguf files under root (recursive walk).
func CollectWeightGGUFRelPaths(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		lower := strings.ToLower(d.Name())
		if !strings.HasSuffix(lower, ".gguf") || strings.Contains(lower, "mmproj") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
