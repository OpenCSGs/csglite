package ctxcompress

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Array sampling follows Headroom's SmartCrusher: keep the boundaries, every
// item that reports an error or looks structurally unusual, and an even
// spread of the rest.
const (
	sampleMinItems = 30
	sampleKeep     = 15
	sampleFirst    = 5
	sampleLast     = 3
)

// jsonErrorKeywords is SmartCrusher's list: an item whose compact JSON
// contains one of them is always kept.
var jsonErrorKeywords = []string{
	"error", "exception", "failed", "failure", "critical", "fatal", "crash",
	"panic", "abort", "timeout", "denied", "rejected",
}

const omittedItemsFormat = "[… %d of %d items omitted]"

// sampleJSON shortens every long array in the document in place, replacing
// the dropped items with one marker string at the end.
func sampleJSON(value *jsonValue) {
	switch value.kind {
	case 'o':
		for _, field := range value.fields {
			sampleJSON(field)
		}
	case 'a':
		for _, item := range value.items {
			sampleJSON(item)
		}
		if kept, total := sampleArray(value.items); len(kept) < total {
			value.items = append(kept, &jsonValue{kind: 's', str: fmt.Sprintf(omittedItemsFormat, total-len(kept), total)})
		}
	}
}

// sampleArray returns the items to keep, in their original order.
func sampleArray(items []*jsonValue) ([]*jsonValue, int) {
	n := len(items)
	if n < sampleMinItems {
		return items, n
	}
	encoded := make([]string, n)
	for i, item := range items {
		encoded[i] = item.compact()
	}

	keep := map[int]bool{}
	for i := 0; i < sampleFirst; i++ {
		keep[i] = true
	}
	for i := n - sampleLast; i < n; i++ {
		keep[i] = true
	}
	for i, text := range encoded {
		lower := strings.ToLower(text)
		for _, keyword := range jsonErrorKeywords {
			if strings.Contains(lower, keyword) {
				keep[i] = true
				break
			}
		}
	}
	for _, i := range structuralOutliers(items) {
		keep[i] = true
	}
	for _, i := range lengthOutliers(encoded) {
		keep[i] = true
	}

	// Spread the remaining budget evenly, skipping duplicates of kept items.
	seen := map[string]bool{}
	for i := range keep {
		seen[encoded[i]] = true
	}
	if remaining := sampleKeep - len(keep); remaining > 0 {
		step := max(1, n/(remaining+1))
		for i := step; i < n && remaining > 0; i += step {
			if keep[i] || seen[encoded[i]] {
				continue
			}
			keep[i] = true
			seen[encoded[i]] = true
			remaining--
		}
	}

	indices := make([]int, 0, len(keep))
	for i := range keep {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	kept := make([]*jsonValue, 0, len(indices))
	for _, i := range indices {
		kept = append(kept, items[i])
	}
	return kept, n
}

// structuralOutliers finds objects carrying a field that fewer than a fifth
// of the items have, and objects whose value in a common field is one of its
// rare values (a "failed" status among "ok"s).
func structuralOutliers(items []*jsonValue) []int {
	n := len(items)
	frequency := map[string]int{}
	for _, item := range items {
		if item.kind != 'o' {
			return nil
		}
		for _, key := range item.keys {
			frequency[key]++
		}
	}
	var out []int
	for i, item := range items {
		for _, key := range item.keys {
			if frequency[key]*5 < n {
				out = append(out, i)
				break
			}
		}
	}

	var common []string
	for key, count := range frequency {
		if count*5 >= n*4 {
			common = append(common, key)
		}
	}
	sort.Strings(common)
	for _, key := range common {
		counts := map[string]int{}
		for _, item := range items {
			if cell := item.field(key); cell != nil && cell.kind != 'o' && cell.kind != 'a' {
				counts[cell.compact()]++
			}
		}
		if len(counts) < 2 || len(counts) > 50 {
			continue
		}
		type valueCount struct {
			value string
			count int
		}
		ranked := make([]valueCount, 0, len(counts))
		total := 0
		for value, count := range counts {
			ranked = append(ranked, valueCount{value, count})
			total += count
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ranked[i].count != ranked[j].count {
				return ranked[i].count > ranked[j].count
			}
			return ranked[i].value < ranked[j].value
		})
		// The smallest set of values covering 80% of items is "normal".
		covered, top := 0, 0
		for top < len(ranked) && covered*5 < total*4 {
			covered += ranked[top].count
			top++
		}
		if top > 5 || top == len(ranked) {
			continue
		}
		normal := map[string]bool{}
		for _, entry := range ranked[:top] {
			normal[entry.value] = true
		}
		for i, item := range items {
			if cell := item.field(key); cell != nil && cell.kind != 'o' && cell.kind != 'a' && !normal[cell.compact()] {
				out = append(out, i)
			}
		}
	}
	return out
}

// lengthOutliers finds items whose encoded size is more than two standard
// deviations from the mean — the one record with a stack trace in it.
func lengthOutliers(encoded []string) []int {
	n := float64(len(encoded))
	mean := 0.0
	for _, text := range encoded {
		mean += float64(len(text))
	}
	mean /= n
	variance := 0.0
	for _, text := range encoded {
		d := float64(len(text)) - mean
		variance += d * d
	}
	sigma := math.Sqrt(variance / n)
	if sigma == 0 {
		return nil
	}
	var out []int
	for i, text := range encoded {
		if math.Abs(float64(len(text))-mean) > 2*sigma {
			out = append(out, i)
		}
	}
	return out
}
