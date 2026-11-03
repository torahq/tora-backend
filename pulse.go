package main

import (
	"strconv"
	"time"
)

type perpMeta struct {
	Universe []struct {
		Name       string `json:"name"`
		IsDelisted bool   `json:"isDelisted"`
	} `json:"universe"`
}

type perpCtx struct {
	DayNtlVlm    string `json:"dayNtlVlm"`
	OpenInterest string `json:"openInterest"`
	MarkPx       string `json:"markPx"`
	PrevDayPx    string `json:"prevDayPx"`
}

// Mover is one market's 24h move. Change is a fraction: 0.179 means +17.9%.
type Mover struct {
	Coin   string  `json:"coin"`
	Change float64 `json:"change"`
}

// Pulse summarizes Hyperliquid's core perp markets over the last 24h.
type Pulse struct {
	Volume24h    float64   `json:"volume24h"`
	OpenInterest float64   `json:"openInterest"`
	Markets      int       `json:"markets"`
	Gainer       *Mover    `json:"gainer"`
	Loser        *Mover    `json:"loser"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// moverMinVolume keeps thin markets out of the top movers.
const moverMinVolume = 1_000_000

func f64(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func computePulse(meta perpMeta, ctxs []perpCtx, now time.Time) Pulse {
	p := Pulse{UpdatedAt: now.UTC()}
	for i, c := range ctxs {
		if i >= len(meta.Universe) {
			break
		}
		vol, mark := f64(c.DayNtlVlm), f64(c.MarkPx)
		p.Volume24h += vol
		p.OpenInterest += f64(c.OpenInterest) * mark
		if meta.Universe[i].IsDelisted {
			continue
		}
		p.Markets++
		prev := f64(c.PrevDayPx)
		if vol < moverMinVolume || prev <= 0 {
			continue
		}
		m := Mover{Coin: meta.Universe[i].Name, Change: mark/prev - 1}
		if p.Gainer == nil || m.Change > p.Gainer.Change {
			g := m
			p.Gainer = &g
		}
		if p.Loser == nil || m.Change < p.Loser.Change {
			l := m
			p.Loser = &l
		}
	}
	return p
}
