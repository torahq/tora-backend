package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
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

	tokenAddrRe := regexp.MustCompile(`^0x[0-9a-f]{40}$`)

	mux.HandleFunc("GET /v1/elysium/tokens", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		limit = min(limit, 200)
		rows, err := s.ElysiumTokens(r.Context(), r.URL.Query().Get("q"), limit)
		if err != nil {
			slog.Error("read elysium tokens", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, errBody("elysium tokens unavailable"))
			return
		}
		if rows == nil {
			rows = []elyTokenRow{}
		}
		writeJSON(w, http.StatusOK, rows)
	})

	mux.HandleFunc("GET /v1/elysium/pools", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Query().Get("tokens"), ",")
		ok := len(parts) >= 2 && len(parts) <= 10
		for _, p := range parts {
			if !tokenAddrRe.MatchString(p) {
				ok = false
				break
			}
		}
		if !ok {
			writeJSON(w, http.StatusBadRequest, errBody("pass 2 to 10 token addresses"))
			return
		}
		pools, err := s.ElysiumPools(r.Context(), parts)
		if err != nil {
			slog.Error("read elysium pools", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, errBody("elysium pools unavailable"))
			return
		}
		if pools == nil {
			pools = []elyPool{}
		}
		writeJSON(w, http.StatusOK, pools)
	})

	return mux
}
