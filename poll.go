package main

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// every runs fn now and then on each tick until ctx ends. A failure is logged; the last good data stays in place.
func every(ctx context.Context, d time.Duration, name string, fn func(context.Context) error) {
	run := func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := fn(c); err != nil && ctx.Err() == nil {
			slog.Warn("poll failed", "loop", name, "err", err)
		}
	}
	run()
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// templateCache keeps outcome templates for an hour. Only the outcomes loop uses it, so it needs no lock.
type templateCache struct {
	list []template
	at   time.Time
}

func (c *templateCache) get(ctx context.Context, hl HL) ([]template, error) {
	if c.list != nil && time.Since(c.at) < time.Hour {
		return c.list, nil
	}
	l, err := hl.Templates(ctx)
	if err != nil {
		if c.list != nil {
			return c.list, nil // stale templates beat no titles
		}
		return nil, err
	}
	c.list, c.at = l, time.Now()
	return l, nil
}

func pollPulse(hl HL, s Store) func(context.Context) error {
	return func(ctx context.Context) error {
		p, err := hl.Pulse(ctx, time.Now())
		if err != nil {
			return err
		}
		return s.SavePulse(ctx, p)
	}
}

func pollOutcomes(hl HL, s Store) func(context.Context) error {
	var tpl templateCache
	return func(ctx context.Context) error {
		templates, err := tpl.get(ctx, hl)
		if err != nil {
			return err
		}
		meta, err := hl.OutcomeMeta(ctx)
		if err != nil {
			return err
		}
		mids, err := hl.Mids(ctx)
		if err != nil {
			return err
		}
		ms := toMarkets(meta, templates, mids, time.Now())
		if len(ms) == 0 {
			return errors.New("no tradable outcome markets")
		}
		return s.UpsertMarkets(ctx, ms)
	}
}
