package chathistory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

const conversationsDir = "conversations"

type Store struct {
	dir string
	mu  sync.RWMutex
}

func NewStore(appHome string) *Store {
	dir := filepath.Join(appHome, conversationsDir)
	os.MkdirAll(dir, 0o755)
	return &Store{dir: dir}
}

func (s *Store) List() ([]api.ConversationMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading conversations dir: %w", err)
	}

	var metas []api.ConversationMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var conv api.Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}
		metas = append(metas, conversationMeta(conv))
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

func (s *Store) Search(query string) ([]api.ConversationMeta, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return s.List()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading conversations dir: %w", err)
	}

	var metas []api.ConversationMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var conv api.Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}
		if conversationMatches(conv, needle) {
			metas = append(metas, conversationMeta(conv))
		}
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

func (s *Store) Get(id string) (*api.Conversation, error) {
	if !isValidID(id) {
		return nil, fmt.Errorf("invalid conversation id")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.filePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("conversation %q not found", id)
		}
		return nil, err
	}
	var conv api.Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("decoding conversation: %w", err)
	}
	return &conv, nil
}

func (s *Store) Save(conv *api.Conversation) error {
	if conv.ID == "" || !isValidID(conv.ID) {
		return fmt.Errorf("invalid conversation id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding conversation: %w", err)
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	tmpFile := s.filePath(conv.ID) + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpFile, s.filePath(conv.ID))
}

func (s *Store) Delete(id string) error {
	if !isValidID(id) {
		return fmt.Errorf("invalid conversation id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	err := os.Remove(s.filePath(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) filePath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func conversationMeta(conv api.Conversation) api.ConversationMeta {
	return api.ConversationMeta{
		ID:        conv.ID,
		Title:     conv.Title,
		Model:     conv.Model,
		CreatedAt: conv.CreatedAt,
		UpdatedAt: conv.UpdatedAt,
		MsgCount:  len(conv.Messages),
	}
}

func conversationMatches(conv api.Conversation, needle string) bool {
	if containsFolded(conv.Title, needle) || containsFolded(conv.Model, needle) {
		return true
	}
	for _, msg := range conv.Messages {
		if containsFolded(messageSearchText(msg.Content), needle) {
			return true
		}
	}
	return false
}

func containsFolded(text, needle string) bool {
	return strings.Contains(strings.ToLower(text), needle)
}

func messageSearchText(content interface{}) string {
	var parts []string
	appendSearchText(&parts, content)
	return strings.Join(parts, " ")
}

func appendSearchText(parts *[]string, value interface{}) {
	switch v := value.(type) {
	case string:
		*parts = append(*parts, v)
	case []interface{}:
		for _, item := range v {
			appendSearchText(parts, item)
		}
	case map[string]interface{}:
		partType, _ := v["type"].(string)
		if partType != "" && partType != "text" {
			return
		}
		if text, ok := v["text"].(string); ok {
			*parts = append(*parts, text)
		}
	}
}

func isValidID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func NewConversation() *api.Conversation {
	now := time.Now()
	return &api.Conversation{
		ID:        generateID(),
		Title:     "New Chat",
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []api.Message{},
	}
}

func generateID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), randomHex(8))
}

func randomHex(n int) string {
	b := make([]byte, n)
	f, err := os.Open("/dev/urandom")
	if err != nil {
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i * 8))
		}
	} else {
		f.Read(b)
		f.Close()
	}
	const hex = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}
