package main

import (
	"testing"
	"time"
)

const metaFixture = `{"outcomes":[
 {"outcome":20835,"name":"template:binaryPrice","description":"perp:ETH|priceDescription:the Pyth ETH/USD price|seconds:90|threshold:2114|time:20261106-0800","sideSpecs":[{"name":"template:Yes"},{"name":"template:No"}]},
 {"outcome":24064,"name":"Recurring","description":"class:priceBinary|underlying:BTC|expiry:20261104-0600|targetPrice:76637|period:1d","sideSpecs":[{"name":"Yes"},{"name":"No"}]},
 {"outcome":100,"name":"Paris","description":"","sideSpecs":[{"name":"Yes"},{"name":"No"}]},
 {"outcome":101,"name":"Lyon","description":"","sideSpecs":[{"name":"Yes"},{"name":"No"}]},
 {"outcome":102,"name":"Other","description":"","sideSpecs":[{"name":"Yes"},{"name":"No"}]},
 {"outcome":200,"name":"template:binaryPrice","description":"perp:BTC|threshold:1|time:20200101-0000","sideSpecs":[{"name":"Yes"},{"name":"No"}]},
 {"outcome":300,"name":"No price","description":"","sideSpecs":[{"name":"Yes"},{"name":"No"}]},
 {"outcome":400,"name":"Three sides","description":"","sideSpecs":[{"name":"A"},{"name":"B"},{"name":"C"}]}
],
"questions":[{"question":1,"name":"template:sportsContestWinner","description":"competition:Ligue 1|stage:Final|participantA:Paris|participantB:Lyon|scheduledStart:20261201-2000","fallbackOutcome":102,"namedOutcomes":[100,101],"settledNamedOutcomes":[101]}]}`

const templatesFixture = `[
 {"id":"binaryPrice","name":"{perp} above {threshold} at {time}?"},
 {"id":"sportsContestWinner","name":"{competition} {stage}: {participantA} v {participantB}"}
]`

var midsFixture = map[string]string{"#208350": "0.62", "#240640": "0.4", "#1000": "0.55", "#1010": "0.3", "#1020": "0.1", "#2000": "0.5", "#4000": "0.2"}

func TestToMarkets(t *testing.T) {
	var meta outcomeMeta
	mustJSON(t, metaFixture, &meta)
	var tpl []template
	mustJSON(t, templatesFixture, &tpl)
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

	got := toMarkets(meta, tpl, midsFixture, now)

	want := []struct {
		id, title, category string
		closes              time.Time
		yes                 float64
	}{
		{"24064", "BTC above 76,637 at Nov 4, 06:00 UTC?", "crypto", time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC), 0.4},
		{"20835", "ETH above 2,114 at Nov 6, 08:00 UTC?", "crypto", time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC), 0.62},
		{"100", "Ligue 1 Final: Paris v Lyon: Paris", "sports", time.Date(2026, 12, 1, 20, 0, 0, 0, time.UTC), 0.55},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d markets, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Venue != "hyperliquid" || g.VenueID != w.id || g.Title != w.title || g.Category != w.category || g.Yes != w.yes || g.URL != "/predict" {
			t.Errorf("market %d = %+v, want %+v", i, g, w)
		}
		if g.ClosesAt == nil || !g.ClosesAt.Equal(w.closes) {
			t.Errorf("market %d closes = %v, want %v", i, g.ClosesAt, w.closes)
		}
	}
}

func TestPriceText(t *testing.T) {
	cases := []struct{ key, in, want string }{
		{"threshold", "2114.50", "2,114.5"},
		{"targetPrice", "76637", "76,637"},
		{"price", "0.000012345678", "0.00001235"},
		{"season", "2026", "2026"},
		{"threshold", "abc", "abc"},
	}
	for _, c := range cases {
		if got := priceText(c.key, c.in); got != c.want {
			t.Errorf("priceText(%q, %q) = %q, want %q", c.key, c.in, got, c.want)
		}
	}
}

func TestFillTemplateUnknownKeyPrintsName(t *testing.T) {
	if got := fillTemplate("{perp} at {time}", map[string]string{"perp": "SOL"}); got != "SOL at time" {
		t.Errorf("got %q", got)
	}
}

func TestRecurringBucket(t *testing.T) {
	f := parseFields("class:priceBucket|underlying:HYPE|expiry:20261105-0000|priceThresholds:30,35")
	for bucket, want := range []string{"HYPE below 30 at Nov 5, 00:00 UTC?", "HYPE between 30 and 35 at Nov 5, 00:00 UTC?", "HYPE above 35 at Nov 5, 00:00 UTC?"} {
		got, ok := recurringTitle(f, bucket)
		if !ok || got != want {
			t.Errorf("bucket %d = %q, want %q", bucket, got, want)
		}
	}
}

func TestDexUnderlyingIsNotCrypto(t *testing.T) {
	var meta outcomeMeta
	mustJSON(t, `{"outcomes":[
	 {"outcome":23725,"name":"template:binaryPrice","description":"perp:xyz:GOLD|threshold:3947.6|time:20261103-2100","sideSpecs":[{"name":"Yes"},{"name":"No"}]},
	 {"outcome":9,"name":"Recurring","description":"class:priceBinary|underlying:xyz:TSLA|expiry:20261104-0600|targetPrice:400|period:1d","sideSpecs":[{"name":"Yes"},{"name":"No"}]}
	],"questions":[]}`, &meta)
	var tpl []template
	mustJSON(t, templatesFixture, &tpl)
	got := toMarkets(meta, tpl, map[string]string{"#237250": "0.24", "#90": "0.5"}, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Title != "GOLD above 3,947.6 at Nov 3, 21:00 UTC?" || got[0].Category != "other" {
		t.Errorf("gold = %q %q", got[0].Title, got[0].Category)
	}
	if got[1].Title != "TSLA above 400 at Nov 4, 06:00 UTC?" || got[1].Category != "other" {
		t.Errorf("tsla = %q %q", got[1].Title, got[1].Category)
	}
}
