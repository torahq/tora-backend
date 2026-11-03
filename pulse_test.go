package main

import (
	"math"
	"testing"
	"time"
)

func TestComputePulse(t *testing.T) {
	var meta perpMeta
	mustJSON(t, `{"universe":[{"name":"BTC"},{"name":"ETH"},{"name":"OLD","isDelisted":true},{"name":"TINY"}]}`, &meta)
	var ctxs []perpCtx
	mustJSON(t, `[
		{"dayNtlVlm":"2000000","openInterest":"10","markPx":"100","prevDayPx":"80"},
		{"dayNtlVlm":"3000000","openInterest":"5","markPx":"90","prevDayPx":"100"},
		{"dayNtlVlm":"0","openInterest":"0","markPx":"1","prevDayPx":"1"},
		{"dayNtlVlm":"500","openInterest":"1","markPx":"50","prevDayPx":"10"}
	]`, &ctxs)
	now := time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC)

	p := computePulse(meta, ctxs, now)

	if p.Volume24h != 5_000_500 {
		t.Errorf("volume = %v, want 5000500", p.Volume24h)
	}
	if p.OpenInterest != 1500 { // 10*100 + 5*90 + 0 + 1*50
		t.Errorf("open interest = %v, want 1500", p.OpenInterest)
	}
	if p.Markets != 3 {
		t.Errorf("markets = %d, want 3 (delisted left out)", p.Markets)
	}
	if p.Gainer == nil || p.Gainer.Coin != "BTC" || math.Abs(p.Gainer.Change-0.25) > 1e-9 {
		t.Errorf("gainer = %+v, want BTC +0.25 (TINY is below the volume floor)", p.Gainer)
	}
	if p.Loser == nil || p.Loser.Coin != "ETH" || math.Abs(p.Loser.Change+0.1) > 1e-9 {
		t.Errorf("loser = %+v, want ETH -0.1", p.Loser)
	}
	if !p.UpdatedAt.Equal(now) {
		t.Errorf("updatedAt = %v", p.UpdatedAt)
	}
}

func TestComputePulseNoLiquidMarkets(t *testing.T) {
	var meta perpMeta
	mustJSON(t, `{"universe":[{"name":"TINY"}]}`, &meta)
	var ctxs []perpCtx
	mustJSON(t, `[{"dayNtlVlm":"10","openInterest":"1","markPx":"2","prevDayPx":"1"}]`, &ctxs)
	p := computePulse(meta, ctxs, time.Now())
	if p.Gainer != nil || p.Loser != nil {
		t.Errorf("movers must be nil without liquid markets, got %+v %+v", p.Gainer, p.Loser)
	}
}
