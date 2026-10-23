package ctxcompress

import (
	"fmt"
	"regexp"
	"strings"
)

// searchLine matches grep/ripgrep output: path, line number, then ':' for a
// match or '-' for a context line. The path must look like one (a slash or
// an extension) so timestamps such as "12:30:45" are not mistaken for it.
var searchLine = regexp.MustCompile(`^((?:[A-Za-z]:[\\/])?[^\s:]*[/\\.][^\s:]*)([:-])(\d+)([:-])(.*)$`)

// searchPathOnly matches `grep` output without line numbers. The path needs
// a directory separator so "key: value" lines are not taken for matches.
var searchPathOnly = regexp.MustCompile(`^((?:[A-Za-z]:[\\/])?[^\s:]*[/\\][^\s:]*[^\s:/\\]):(.*)$`)

type searchMatch struct {
	path string
	// rest is everything after the path, starting with the line number when
	// there is one ("12:content"), otherwise the content itself.
	rest string
	ok   bool
}

func parseSearchLine(line string) searchMatch {
	if m := searchLine.FindStringSubmatch(line); m != nil && m[2] == m[4] {
		return searchMatch{path: m[1], rest: m[3] + m[4] + m[5], ok: true}
	}
	if m := searchPathOnly.FindStringSubmatch(line); m != nil {
		return searchMatch{path: m[1], rest: m[2], ok: true}
	}
	return searchMatch{}
}

// looksLikeSearch wants most lines to parse as matches and at least one file
// to appear more than once, since grouping is what saves the bytes.
func looksLikeSearch(lines []string) bool {
	nonEmpty, parsed, repeats := 0, 0, 0
	previous := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || line == "--" {
			continue
		}
		nonEmpty++
		match := parseSearchLine(line)
		if !match.ok {
			continue
		}
		parsed++
		if match.path == previous {
			repeats++
		}
		previous = match.path
	}
	return parsed >= 5 && parsed*10 >= nonEmpty*6 && repeats > 0
}

// groupHeader is the file name compressSearch prints above a group.
var groupHeader = regexp.MustCompile(`^(?:[A-Za-z]:[\\/])?[^\s:]*[/\\.][^\s:]*$`)

// looksLikeGroupedSearch recognises compressSearch output — file names each
// followed by indented matches — so a second pass leaves it alone rather than
// treating it as a log.
func looksLikeGroupedSearch(lines []string) bool {
	nonEmpty, grouped, ungrouped := 0, 0, 0
	inGroup := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			inGroup = false
			continue
		}
		nonEmpty++
		switch {
		case inGroup && strings.HasPrefix(line, "  "):
			grouped++
		case groupHeader.MatchString(line) && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  "):
			inGroup = true
			grouped++
		default:
			inGroup = false
			if parseSearchLine(line).ok {
				ungrouped++
			}
		}
	}
	return grouped >= 3 && (grouped+ungrouped)*2 >= nonEmpty
}

const (
	searchMaxLineDivisor = 4
	searchPerFileKeep    = 12
	searchPerFileFirst   = 4
	searchPerFileLast    = 2
)

// compressSearch prints each file's name once above its matches instead of
// on every line, and cuts over-long matches. With sampling it
// also keeps at most searchPerFileKeep matches per file, preferring the first,
// the last and those mentioning errors.
func compressSearch(lines []string, opts Options) string {
	// A match longer than a normal line is usually minified code or a binary
	// file; sampling cuts those shorter than safe mode does.
	maxLine := opts.MaxLineBytes
	if opts.Sample {
		maxLine /= searchMaxLineDivisor
	}
	lines = trimLongLines(lines, maxLine)
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		first := parseSearchLine(lines[i])
		if !first.ok {
			out = append(out, lines[i])
			i++
			continue
		}
		var group []string
		j := i
		for ; j < len(lines); j++ {
			match := parseSearchLine(lines[j])
			if !match.ok || match.path != first.path {
				break
			}
			group = append(group, match.rest)
		}
		if len(group) == 1 {
			out = append(out, lines[i])
		} else {
			if opts.Sample {
				group = sampleSearchGroup(group)
			}
			out = append(out, first.path)
			for _, rest := range group {
				out = append(out, "  "+rest)
			}
		}
		i = j
	}
	return joinLines(foldRepeats(out))
}

func sampleSearchGroup(group []string) []string {
	if len(group) <= searchPerFileKeep {
		return group
	}
	keep := make([]bool, len(group))
	kept := 0
	mark := func(i int) {
		if !keep[i] {
			keep[i] = true
			kept++
		}
	}
	for i := 0; i < searchPerFileFirst; i++ {
		mark(i)
	}
	for i := len(group) - searchPerFileLast; i < len(group); i++ {
		mark(i)
	}
	for i, rest := range group {
		if kept >= searchPerFileKeep {
			break
		}
		if isErrorLine(rest) {
			mark(i)
		}
	}
	if remaining := searchPerFileKeep - kept; remaining > 0 {
		step := max(1, len(group)/(remaining+1))
		for i := step; i < len(group) && kept < searchPerFileKeep; i += step {
			mark(i)
		}
	}
	out := make([]string, 0, kept+1)
	for i, rest := range group {
		if keep[i] {
			out = append(out, rest)
		}
	}
	return append(out, fmt.Sprintf("[… %d more matches in this file]", len(group)-kept))
}
