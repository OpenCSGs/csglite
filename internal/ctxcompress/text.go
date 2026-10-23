// Package ctxcompress shrinks the tool output inside LLM chat requests before
// they are sent to a model.
//
// It is a model-free port of the deterministic parts of Headroom
// (github.com/headroomlabs-ai/headroom): content is routed by type, JSON is
// minified, runs of identical lines are counted, over-long lines are cut and
// search results are grouped by file. With Options.Sample it also drops
// content: long arrays, logs and search results keep a representative subset,
// and lines differing only in their numbers are folded. Source files a tool
// read back are never changed, because a coding agent edits them by exact
// string match.
//
// Every transform is deterministic and idempotent: the same text always
// compresses to the same bytes, and compressing the output again changes
// nothing. The first keeps prompt caches warm across turns; the second makes a
// request that passes through two gateways, such as a LAN cluster hop, safe.
package ctxcompress

import (
	"strings"
	"unicode/utf8"
)

// Options tunes Text. The zero value selects the defaults.
type Options struct {
	// MinBytes is the smallest tool result worth compressing.
	MinBytes int
	// MaxLineBytes is the length above which a single line is cut down.
	MaxLineBytes int
	// MaxLines is the line count above which sampled command output keeps
	// only its head, its tail and the lines that report errors. On captured
	// agent traffic 150 keeps every error line and still halves long build
	// and test logs.
	MaxLines int
	// Sample enables the lossy transforms: long JSON arrays, long command
	// output and long search results keep a representative subset instead of
	// everything. Without it only redundancy is removed.
	Sample bool
}

const (
	defaultMinBytes     = 512
	defaultMaxLineBytes = 2000
	defaultMaxLines     = 150
)

func (o Options) withDefaults() Options {
	if o.MinBytes <= 0 {
		o.MinBytes = defaultMinBytes
	}
	if o.MaxLineBytes <= 0 {
		o.MaxLineBytes = defaultMaxLineBytes
	}
	if o.MaxLines <= 0 {
		o.MaxLines = defaultMaxLines
	}
	return o
}

// Text compresses one tool result. toolName is the name of the tool that
// produced it when the request says so, and only steers detection.
func Text(text, toolName string, opts Options) (string, Kind) {
	opts = opts.withDefaults()
	if len(text) < opts.MinBytes || !utf8.ValidString(text) {
		return text, KindSkipped
	}
	if isFileReadTool(toolName) || looksLikeFileRead(text) {
		return text, KindFileRead
	}
	if first, _, _ := strings.Cut(text, "\n"); isTableHeader(first) {
		return text, KindJSON // already compressed
	}
	if out, ok := compressJSON(text, opts); ok {
		return keepSmaller(text, out), KindJSON
	}

	lines := normalizeLines(text)
	switch {
	case hasFactoredPrefix(lines):
		// Only compressLog writes prefix headers, so this is its output.
		return keepSmaller(text, compressLog(lines, opts)), KindLog
	case looksLikeDiff(lines):
		// Every line of a diff is load-bearing for a review or a patch, so it
		// only loses what normalizeLines strips and over-long lines.
		return keepSmaller(text, joinLines(trimLongLines(lines, opts.MaxLineBytes))), KindDiff
	case looksLikeGroupedSearch(lines):
		return text, KindSearch // already compressed
	case looksLikeSearch(lines):
		return keepSmaller(text, compressSearch(lines, opts)), KindSearch
	case looksLikeCode(lines):
		return text, KindCode
	case looksLikeLog(lines):
		return keepSmaller(text, compressLog(lines, opts)), KindLog
	default:
		return keepSmaller(text, compressProse(lines, opts)), KindText
	}
}

// keepSmaller returns the original unless the rewrite is strictly shorter,
// so a transform that happens to add bytes never ships.
func keepSmaller(original, rewritten string) string {
	if len(rewritten) < len(original) {
		return rewritten
	}
	return original
}

// fileReadTools are the tool names coding agents use to read a file back.
// Their output is quoted verbatim in later edits, so it is never rewritten.
var fileReadTools = map[string]bool{
	"read": true, "read_file": true, "readfile": true, "view": true,
	"view_file": true, "open_file": true, "cat": true, "str_replace_editor": true,
	"str_replace_based_edit_tool": true, "notebookread": true, "read_many_files": true,
	"edit": true, "write": true, "multiedit": true, "apply_patch": true,
	"notebookedit": true, "str_replace": true,
}

func isFileReadTool(name string) bool {
	return fileReadTools[strings.ToLower(strings.TrimSpace(name))]
}

// looksLikeFileRead spots `cat -n` style output — a line number, then a tab
// or an arrow — which is how agents show file contents whatever the tool is
// called.
func looksLikeFileRead(text string) bool {
	total, numbered := 0, 0
	for _, line := range firstLines(text, 30) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		total++
		if isNumberedLine(line) {
			numbered++
		}
	}
	return total >= 3 && numbered*10 >= total*8
}

func isNumberedLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits == len(trimmed) {
		return digits > 0
	}
	rest := trimmed[digits:]
	return rest[0] == '\t' || strings.HasPrefix(rest, "→") || strings.HasPrefix(rest, "│")
}

func firstLines(text string, n int) []string {
	lines := make([]string, 0, n)
	for len(lines) < n && text != "" {
		line, rest, found := strings.Cut(text, "\n")
		lines = append(lines, line)
		if !found {
			break
		}
		text = rest
	}
	return lines
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

var codeKeywords = []string{
	"func ", "def ", "class ", "import ", "from ", "return", "if ", "for ", "while ",
	"const ", "let ", "var ", "package ", "#include", "public ", "private ", "static ",
	"export ", "type ", "interface ", "struct ", "fn ", "impl ", "use ", "async ", "await ",
	"try", "catch", "else", "switch ", "case ", "//", "/*", "* ", "# ", "@",
}

// looksLikeCode spots a source file printed without line numbers, as by
// `cat` or `sed -n` through a shell tool. Such output is protected like a
// file read: an agent copies it into edits byte for byte.
func looksLikeCode(lines []string) bool {
	nonEmpty, code := 0, 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		nonEmpty++
		if strings.ContainsAny(trimmed[len(trimmed)-1:], "{};:") {
			code++
			continue
		}
		for _, keyword := range codeKeywords {
			if strings.HasPrefix(trimmed, keyword) {
				code++
				break
			}
		}
	}
	return nonEmpty >= 8 && code*2 >= nonEmpty
}
