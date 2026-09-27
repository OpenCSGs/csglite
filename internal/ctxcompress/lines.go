package ctxcompress

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ansiEscape matches terminal colour and cursor sequences (CSI and OSC),
// which cost tokens and mean nothing to a model.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// normalizeLines splits command output into lines the way a terminal would
// show them: escape sequences dropped, a carriage-return progress bar reduced
// to its final state, and trailing blanks removed.
func normalizeLines(text string) []string {
	if strings.Contains(text, "\x1b") {
		text = ansiEscape.ReplaceAllString(text, "")
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, "\r") {
			line = strings.TrimRight(line, "\r")
			if idx := strings.LastIndex(line, "\r"); idx >= 0 {
				line = line[idx+1:]
			}
		}
		lines[i] = strings.TrimRight(line, " \t")
	}
	return lines
}

// trimLongLines cuts every line longer than maxBytes down to its start and
// end. Such lines are minified bundles, base64 or binary matches that a model
// cannot use in full anyway.
func trimLongLines(lines []string, maxBytes int) []string {
	out := lines
	copied := false
	for i, line := range lines {
		if len(line) <= maxBytes {
			continue
		}
		if !copied {
			out = append([]string(nil), lines...)
			copied = true
		}
		out[i] = trimLine(line, maxBytes)
	}
	return out
}

func trimLine(line string, maxBytes int) string {
	head := cutRunes(line, maxBytes/2, false)
	tail := cutRunes(line, maxBytes/4, true)
	omitted := len(line) - len(head) - len(tail)
	return fmt.Sprintf("%s …[%d bytes omitted]… %s", head, omitted, tail)
}

// cutRunes returns at most n bytes from the start (or, with fromEnd, the end)
// of s without splitting a UTF-8 sequence.
func cutRunes(s string, n int, fromEnd bool) string {
	if n >= len(s) {
		return s
	}
	if !fromEnd {
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		return s[:n]
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// foldRepeats replaces three or more identical consecutive lines with one
// copy and a count.
func foldRepeats(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		run := j - i
		if run >= 3 && strings.TrimSpace(lines[i]) != "" {
			out = append(out, lines[i], fmt.Sprintf("[… previous line repeated %d more times]", run-1))
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return out
}

// foldBlankRuns keeps at most one empty line in a row.
func foldBlankRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if line == "" && i > 0 && lines[i-1] == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

const (
	similarRunMin  = 6
	similarRunKeep = 2
)

// foldSimilar shortens runs of consecutive lines that differ only in their
// numbers — progress output, download counters, polling loops — to the first
// and last two with a count in between. It drops the numbers of the folded
// lines, so only aggressive mode uses it.
func foldSimilar(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		template := lineTemplate(lines[i])
		j := i + 1
		for j < len(lines) && template != "" && lineTemplate(lines[j]) == template {
			j++
		}
		run := j - i
		if run >= similarRunMin {
			out = append(out, lines[i:i+similarRunKeep]...)
			out = append(out, fmt.Sprintf("[… %d similar lines omitted]", run-2*similarRunKeep))
			out = append(out, lines[j-similarRunKeep:j]...)
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return out
}

var (
	templateUUID   = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	templateHex    = regexp.MustCompile(`\b(?:0x)?[0-9a-fA-F]{8,}\b`)
	templateNumber = regexp.MustCompile(`\d+(?:[.,:]\d+)*`)
)

// lineTemplate is the line with its variable parts masked. Blank lines,
// lines without any number and lines reporting an error have no template, so
// only numeric churn folds and every error line survives on its own.
func lineTemplate(line string) string {
	if strings.TrimSpace(line) == "" || !strings.ContainsAny(line, "0123456789") || isErrorLine(line) {
		return ""
	}
	masked := templateUUID.ReplaceAllString(line, "U")
	masked = templateHex.ReplaceAllString(masked, "H")
	masked = templateNumber.ReplaceAllString(masked, "0")
	if !strings.ContainsFunc(masked, func(r rune) bool { return r != '0' && r != 'U' && r != 'H' && r != ' ' }) {
		// A line that is nothing but numbers is data, not churn.
		return ""
	}
	return masked
}

// errorKeywords mark the lines of command output a model most needs: they
// survive head/tail sampling wherever they sit.
var errorKeywords = []string{
	"error", "fail", "fatal", "panic", "exception", "traceback", "warn",
	"denied", "refused", "timeout", "timed out", "cannot", "can't", "unable",
	"not found", "no such", "undefined", "unexpected", "invalid", "segfault",
	"assert", "abort", "critical", "missing", "conflict", "rejected", "错误", "失败",
}

func isErrorLine(line string) bool {
	lower := strings.ToLower(line)
	for _, keyword := range errorKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// sampleLines keeps the head and tail of output longer than maxLines, plus
// the lines around every error in between, within the same line budget.
func sampleLines(lines []string, maxLines int) []string {
	// Prefix headers are added after sampling, so they do not count.
	if len(lines) <= maxLines || len(lines)-countFactoredHeaders(lines) <= maxLines {
		return lines
	}
	head := maxLines / 4
	tail := maxLines * 3 / 8
	budget := maxLines - head - tail - 1

	middle := lines[head : len(lines)-tail]
	keep := make([]bool, len(middle))
	for i, line := range middle {
		if !isErrorLine(line) {
			continue
		}
		for k := max(0, i-1); k <= min(len(middle)-1, i+1); k++ {
			keep[k] = true
		}
	}

	out := append([]string(nil), lines[:head]...)
	omitted := 0
	flush := func() {
		if omitted > 0 {
			out = append(out, fmt.Sprintf("[… %d lines omitted]", omitted))
			omitted = 0
			budget--
		}
	}
	for i, line := range middle {
		// Each kept line costs one slot and may need a marker before it.
		if keep[i] && budget >= 2 {
			flush()
			out = append(out, line)
			budget--
			continue
		}
		omitted++
	}
	flush()
	return append(out, lines[len(lines)-tail:]...)
}

func compressLog(lines []string, opts Options) string {
	lines = trimLongLines(lines, opts.MaxLineBytes)
	lines = foldBlankRuns(lines)
	lines = foldRepeats(lines)
	if opts.Sample {
		// Folding lines that differ only in their numbers loses those
		// numbers, so it is a sampling step, not a redundancy one.
		lines = foldSimilar(lines)
		lines = sampleLines(lines, opts.MaxLines)
		lines = factorPrefixes(lines)
	}
	return joinLines(lines)
}

// compressProse handles fetched pages and other free text, where long lines
// are paragraphs rather than noise, so only far longer ones are cut.
func compressProse(lines []string, opts Options) string {
	lines = trimLongLines(lines, opts.MaxLineBytes*4)
	lines = foldBlankRuns(lines)
	lines = foldRepeats(lines)
	return joinLines(lines)
}

// looksLikeDiff spots unified diffs and git patches.
func looksLikeDiff(lines []string) bool {
	hunks := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			return true
		}
		if strings.HasPrefix(line, "@@ -") && strings.Contains(line, " @@") {
			hunks++
		}
	}
	return hunks > 0 && containsPrefixPair(lines, "--- ", "+++ ")
}

func containsPrefixPair(lines []string, first, second string) bool {
	for i := 0; i+1 < len(lines); i++ {
		if strings.HasPrefix(lines[i], first) && strings.HasPrefix(lines[i+1], second) {
			return true
		}
	}
	return false
}

// looksLikeLog separates line-oriented command output from prose: many
// lines, and short ones on average.
func looksLikeLog(lines []string) bool {
	nonEmpty, total := 0, 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		nonEmpty++
		total += len(line)
	}
	return nonEmpty >= 8 && total/nonEmpty <= 240
}

const (
	prefixRunMin   = 5
	prefixMinBytes = 16
)

const prefixHeaderFormat = "[the next %d lines each start with: %s]"

var prefixHeader = regexp.MustCompile(`^\[the next (\d+) lines each start with: ".*"\]$`)

// factorPrefixes writes a prefix shared by a run of lines — the job and
// step columns of a CI log, a deep directory in a file list — once above
// the run instead of on every line. The prefix always ends at a space, tab
// or slash so the remainder reads naturally.
func factorPrefixes(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		// A run factored by an earlier pass stays as it is.
		if m := prefixHeader.FindStringSubmatch(lines[i]); m != nil {
			n, _ := strconv.Atoi(m[1])
			end := min(len(lines), i+1+n)
			out = append(out, lines[i:end]...)
			i = end
			continue
		}
		prefix, end := "", i+1
		if lines[i] != "" {
			common := lines[i]
			for j := i + 1; j < len(lines); j++ {
				next := commonPrefix(common, lines[j])
				if cutAtSeparator(next) == "" || len(cutAtSeparator(next)) < prefixMinBytes {
					break
				}
				common, end = next, j+1
			}
			prefix = cutAtSeparator(common)
		}
		if end-i < prefixRunMin || len(prefix) < prefixMinBytes {
			out = append(out, lines[i])
			i++
			continue
		}
		out = append(out, fmt.Sprintf(prefixHeaderFormat, end-i, strconv.Quote(prefix)))
		for _, line := range lines[i:end] {
			out = append(out, line[len(prefix):])
		}
		i = end
	}
	return out
}

func hasFactoredPrefix(lines []string) bool {
	return countFactoredHeaders(lines) > 0
}

func countFactoredHeaders(lines []string) int {
	n := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "[the next ") && prefixHeader.MatchString(line) {
			n++
		}
	}
	return n
}

func commonPrefix(a, b string) string {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// cutAtSeparator shortens a common prefix to end just after its last space,
// tab or slash.
func cutAtSeparator(prefix string) string {
	idx := strings.LastIndexAny(prefix, " \t/\\")
	if idx < 0 {
		return ""
	}
	return prefix[:idx+1]
}
