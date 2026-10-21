package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/opencsgs/csglite/internal/chathistory"
	"github.com/opencsgs/csglite/pkg/api"
)

func (s *Server) handleConversationsList(w http.ResponseWriter, r *http.Request) {
	metas, err := s.conversations.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	metas = ownedConversations(r, metas)
	if metas == nil {
		metas = []api.ConversationMeta{}
	}
	writeJSON(w, http.StatusOK, api.ConversationsListResponse{Conversations: metas})
}

func (s *Server) handleConversationsSearch(w http.ResponseWriter, r *http.Request) {
	metas, err := s.conversations.Search(r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	metas = ownedConversations(r, metas)
	if metas == nil {
		metas = []api.ConversationMeta{}
	}
	writeJSON(w, http.StatusOK, api.ConversationsListResponse{Conversations: metas})
}

func (s *Server) handleConversationGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conv, err := s.conversations.Get(id)
	if err != nil || !ownsConversation(r, conv.OwnerID) {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

func (s *Server) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	conv := chathistory.NewConversation()
	conv.OwnerID, _ = ownerScope(r)

	if r.Body != nil && r.ContentLength > 0 {
		var patch api.Conversation
		if err := json.NewDecoder(r.Body).Decode(&patch); err == nil {
			if patch.Title != "" {
				conv.Title = patch.Title
			}
			if patch.Model != "" {
				conv.Model = patch.Model
			}
			if patch.Settings != nil {
				conv.Settings = patch.Settings
			}
			if len(patch.Messages) > 0 {
				conv.Messages = patch.Messages
				conv.UpdatedAt = time.Now()
			}
		}
	}

	if err := s.conversations.Save(conv); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, conv)
}

func (s *Server) handleConversationUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.conversations.Get(id)
	if err != nil || !ownsConversation(r, existing.OwnerID) {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	var patch api.Conversation
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if patch.Title != "" {
		existing.Title = patch.Title
	}
	if patch.Model != "" {
		existing.Model = patch.Model
	}
	if patch.Settings != nil {
		existing.Settings = patch.Settings
	}
	if patch.Messages != nil {
		existing.Messages = patch.Messages
	}
	existing.UpdatedAt = time.Now()

	if err := s.conversations.Save(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleConversationDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if conv, err := s.conversations.Get(id); err == nil && !ownsConversation(r, conv.OwnerID) {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	if err := s.conversations.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ownsConversation reports whether the caller may open a conversation. With
// accounts on, each user sees only their own; the local operator keeps the
// conversations that predate accounts.
func ownsConversation(r *http.Request, ownerID string) bool {
	owner, scoped := ownerScope(r)
	return !scoped || ownerID == owner
}

func ownedConversations(r *http.Request, metas []api.ConversationMeta) []api.ConversationMeta {
	if _, scoped := ownerScope(r); !scoped {
		return metas
	}
	out := metas[:0]
	for _, meta := range metas {
		if ownsConversation(r, meta.OwnerID) {
			out = append(out, meta)
		}
	}
	return out
}
