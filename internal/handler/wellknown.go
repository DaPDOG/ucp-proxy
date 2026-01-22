package handler

import (
	"net/http"
)

// handleWellKnown returns the UCP discovery profile.
// GET /.well-known/ucp
func (h *Handler) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	profile, err := h.adapter.GetProfile(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, profile)
}

// handleHealth returns a simple health check response.
// GET /health, GET /healthz
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

type healthResponse struct {
	Status string `json:"status"`
}
