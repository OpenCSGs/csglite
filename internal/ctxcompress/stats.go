package ctxcompress

// Kind is the content type Text detected for a block and routed it by.
type Kind string

const (
	KindSkipped  Kind = "skipped"
	KindFileRead Kind = "file_read"
	KindCode     Kind = "code"
	KindJSON     Kind = "json"
	KindSearch   Kind = "search"
	KindDiff     Kind = "diff"
	KindLog      Kind = "log"
	KindText     Kind = "text"
	// KindDuplicate is a tool result replaced by a pointer to an identical
	// earlier one.
	KindDuplicate Kind = "duplicate"
)

// KindStats counts the tool results of one kind.
type KindStats struct {
	Blocks      int `json:"blocks"`
	Compressed  int `json:"compressed"`
	BytesBefore int `json:"bytes_before"`
	BytesAfter  int `json:"bytes_after"`
	// TokensSaved is an estimate; see EstimateTokens.
	TokensSaved int `json:"tokens_saved"`
}

// Stats totals what CompressRequest did to the tool results of one request.
type Stats struct {
	Blocks      int                `json:"blocks"`
	Compressed  int                `json:"compressed"`
	BytesBefore int                `json:"bytes_before"`
	BytesAfter  int                `json:"bytes_after"`
	TokensSaved int                `json:"tokens_saved"`
	ByKind      map[Kind]KindStats `json:"by_kind,omitempty"`
}

func (s *Stats) add(kind Kind, before, after string) {
	changed := before != after
	saved := 0
	if changed {
		saved = EstimateTokens(before) - EstimateTokens(after)
	}
	s.Blocks++
	s.BytesBefore += len(before)
	s.BytesAfter += len(after)
	s.TokensSaved += saved
	if s.ByKind == nil {
		s.ByKind = map[Kind]KindStats{}
	}
	entry := s.ByKind[kind]
	entry.Blocks++
	entry.BytesBefore += len(before)
	entry.BytesAfter += len(after)
	entry.TokensSaved += saved
	if changed {
		s.Compressed++
		entry.Compressed++
	}
	s.ByKind[kind] = entry
}

// EstimateTokens approximates a tokenizer without loading one: 3.5 ASCII
// characters or 1.2 characters of other scripts per token. On captured
// coding-agent traffic it is within 2% of o200k counts overall, and for text
// that is mostly Chinese.
func EstimateTokens(text string) int {
	ascii, other := 0, 0
	for _, r := range text {
		if r < 0x80 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii*12 + other*35) / 42
}
