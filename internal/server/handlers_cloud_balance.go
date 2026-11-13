package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
)

type cloudAccountBalance struct {
	// Nil means the community's billing service did not return a balance.
	// Zero is a valid balance. The value is in minor currency units.
	Balance     *float64 `json:"balance"`
	RechargeURL string   `json:"recharge_url"`
	UserUUID    string   `json:"user_uuid"`
}

func (s *Server) handleCloudAccountBalance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.TrimSpace(s.cfg.Token) == "" {
		writeError(w, http.StatusUnauthorized, "Community login required.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	client := csghub.NewClient(s.cfg.ServerURL, s.cfg.Token)
	user, err := client.GetCurrentUser(ctx)
	if err != nil || user == nil {
		writeError(w, http.StatusUnauthorized, "Community login expired. Please sign in again.")
		return
	}
	resp := cloudAccountBalance{
		RechargeURL: strings.TrimRight(s.cfg.DisplayURL(), "/") + "/settings/recharge-payment",
		UserUUID:    user.UUID,
	}
	if balance, err := client.GetAccountBalance(ctx, user.UUID); err == nil {
		resp.Balance = &balance
	}
	// Billing may be disabled or unavailable on a community. Keep the account
	// dialog and recharge link usable without inventing a zero balance.
	writeJSON(w, http.StatusOK, resp)
}
