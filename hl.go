package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// HL reads Hyperliquid's public info API. Market data always comes from mainnet.
type HL struct {
	URL    string
	Client *http.Client
}

func (h HL) post(ctx context.Context, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("hyperliquid %s: %s", b, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (h HL) Pulse(ctx context.Context, now time.Time) (Pulse, error) {
	var parts []json.RawMessage
	if err := h.post(ctx, map[string]string{"type": "metaAndAssetCtxs"}, &parts); err != nil {
		return Pulse{}, err
	}
	if len(parts) != 2 {
		return Pulse{}, fmt.Errorf("metaAndAssetCtxs: want 2 parts, got %d", len(parts))
	}
	var meta perpMeta
	var ctxs []perpCtx
	if err := json.Unmarshal(parts[0], &meta); err != nil {
		return Pulse{}, err
	}
	if err := json.Unmarshal(parts[1], &ctxs); err != nil {
		return Pulse{}, err
	}
	if len(ctxs) == 0 {
		return Pulse{}, errors.New("metaAndAssetCtxs: no markets")
	}
	return computePulse(meta, ctxs, now), nil
}
