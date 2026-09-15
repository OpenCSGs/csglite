package realtime

import (
	"context"
	"strings"
	"unicode"
)

// Responder writes the reply to what the caller said. It delivers the text as
// the model produces it, so synthesis can begin on the opening clause while the
// rest of the answer is still being written -- which is the whole point of
// running the model here rather than in the client: a client has to wait for
// the complete reply before it can ask for it to be spoken.
type Responder interface {
	// Reply generates the next assistant turn for history with model, calling
	// onText for each piece of text as it arrives. It must return promptly
	// when ctx is cancelled, and stop when onText returns an error.
	Reply(ctx context.Context, model string, history []Message, onText func(text string) error) error
}

// Message is one turn of the conversation a Responder is given.
type Message struct {
	Role    string
	Content string
}

// historyLimit bounds how many turns of the conversation are kept for the
// model. A voice conversation is short-lived and its turns are short; what the
// limit guards against is a long call growing the prompt without end.
const historyLimit = 20

const (
	// sentenceEnders release the text before them as a piece to synthesise.
	sentenceEnders = "。！？!?;；\n"
	// clauseEnders may release the opening piece only, once it holds at least
	// firstClauseMin characters: "好的，" is worth speaking on its own when the
	// alternative is silence until the sentence it opens is complete, while a
	// bare "嗯，" is too little to start on.
	clauseEnders   = "，,、"
	firstClauseMin = 3
	// sentenceMax forces a release in text that never punctuates, so a run-on
	// answer is still spoken before the model finishes it.
	sentenceMax = 60
)

// sentenceSplitter cuts a text stream into pieces worth synthesising on their
// own. It is fed the text in whatever fragments the model emits and hands back
// complete sentences; before the first piece has gone out it also breaks at a
// clause, since the wait for the first audio is the one the caller notices.
type sentenceSplitter struct {
	buf      []rune
	released bool
}

// push appends text and reports the pieces it completed.
func (p *sentenceSplitter) push(text string) []string {
	var out []string
	for _, r := range text {
		p.buf = append(p.buf, r)
		switch {
		case strings.ContainsRune(sentenceEnders, r):
			out = p.release(out)
		case r == '.':
			// A full stop ends a sentence unless it sits inside a number, where
			// "3.14" must stay one piece.
			if n := len(p.buf); n < 2 || !unicode.IsDigit(p.buf[n-2]) {
				out = p.release(out)
			}
		case !p.released && strings.ContainsRune(clauseEnders, r) && len(p.buf) >= firstClauseMin:
			out = p.release(out)
		case len(p.buf) >= sentenceMax:
			out = p.release(out)
		}
	}
	return out
}

// flush returns whatever is left once the model has finished.
func (p *sentenceSplitter) flush() string {
	text := strings.TrimSpace(string(p.buf))
	p.buf = p.buf[:0]
	return text
}

func (p *sentenceSplitter) release(out []string) []string {
	text := strings.TrimSpace(string(p.buf))
	p.buf = p.buf[:0]
	if text == "" {
		return out
	}
	p.released = true
	return append(out, text)
}
