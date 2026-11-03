package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// pulseStaleAfter is three missed 15 s polls.
const pulseStaleAfter = 45 * time.Second

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func routes(s Store, now func() time.Time) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		pg, rd := s.Health(r.Context())
		code := http.StatusOK
		if !pg || !rd {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, map[string]bool{"ok": pg && rd, "postgres": pg, "redis": rd})
	})

	mux.HandleFunc("GET /v1/pulse", func(w http.ResponseWriter, r *http.Request) {
		p, ok, err := s.Pulse(r.Context())
		if err != nil {
			slog.Error("read pulse", "err", err)
		}
		if err != nil || !ok {
			writeJSON(w, http.StatusServiceUnavailable, errBody("pulse not ready"))
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Pulse
			Stale bool `json:"stale"`
		}{p, now().Sub(p.UpdatedAt) > pulseStaleAfter})
	})

	mux.HandleFunc("GET /v1/predict/featured", func(w http.ResponseWriter, r *http.Request) {
		m, ok, err := s.Featured(r.Context())
		if err != nil {
			slog.Error("read featured", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, errBody("markets unavailable"))
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, errBody("no featured market"))
			return
		}
		writeJSON(w, http.StatusOK, m)
	})

	return mux
}
