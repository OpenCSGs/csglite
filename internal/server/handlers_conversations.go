package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/opencsgs/csghub-lite/internal/chathistory"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func (s *Server) handleConversationsList(w http.ResponseWriter, r *http.Request) {
	metas, err := s.conversations.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	if metas == nil {
		metas = []api.ConversationMeta{}
	}
	writeJSON(w, http.StatusOK, api.ConversationsListResponse{Conversations: metas})
}

func (s *Server) handleConversationGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conv, err := s.conversations.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

func (s *Server) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	conv := chathistory.NewConversation()

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
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
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
	if err := s.conversations.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
