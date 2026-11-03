package main

import (
	"math"
	"testing"
	"time"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

const matchEventJSON = `{
  "slug":"ucl-fen-liv-2026-11-04",
  "startTime":"2026-11-04T17:45:00Z",
  "ended":false,
  "markets":[
    {"groupItemTitle":"Fenerbahçe SK","sportsMarketType":"moneyline","outcomes":"[\"Yes\", \"No\"]","outcomePrices":"[\"0.155\", \"0.845\"]","bestBid":0.15,"bestAsk":0.16},
    {"groupItemTitle":"Draw (Fenerbahçe SK vs. Liverpool FC)","sportsMarketType":"moneyline","outcomes":"[\"Yes\", \"No\"]","outcomePrices":"[\"0.195\", \"0.805\"]","bestBid":0.19,"bestAsk":0.2},
    {"groupItemTitle":"Liverpool FC","sportsMarketType":"moneyline","outcomes":"[\"Yes\", \"No\"]","outcomePrices":"[\"0.655\", \"0.345\"]","bestBid":0.65,"bestAsk":0.66}
  ]
}`

func TestPolyMatchEvent(t *testing.T) {
	var main polyEvent
	mustJSON(t, matchEventJSON, &main)
	ev, ok := polyMatchEvent(main, "UEFA Champions League")
	if !ok {
		t.Fatal("main 1X2 event must be kept")
	}
	if ev.Kickoff != time.Date(2026, 11, 4, 17, 45, 0, 0, time.UTC) {
		t.Errorf("kickoff = %s", ev.Kickoff)
	}
	if ev.URL != "https://polymarket.com/event/ucl-fen-liv-2026-11-04" {
		t.Errorf("url = %s", ev.URL)
	}
	if len(ev.Markets) != 3 || ev.Markets[1].Label != "draw" || ev.Markets[0].Label != "Fenerbahçe SK" {
		t.Fatalf("markets = %+v", ev.Markets)
	}
	if !approx(ev.Markets[0].Price, 0.155) || ev.Markets[0].Bid == nil || !approx(*ev.Markets[0].Bid, 0.15) {
		t.Errorf("price = %+v", ev.Markets[0])
	}

	var more polyEvent
	mustJSON(t, `{"slug":"ucl-fen-liv-2026-11-04-more-markets","startTime":"2026-11-04T17:45:00Z",`+
		`"markets":[{"groupItemTitle":"x","sportsMarketType":"moneyline"}]}`, &more)
	if _, ok := polyMatchEvent(more, "UEFA Champions League"); ok {
		t.Error("sibling more-markets event must be dropped")
	}

	var spreads polyEvent
	mustJSON(t, `{"slug":"ucl-fen-liv-2026-11-04","startTime":"2026-11-04T17:45:00Z",`+
		`"markets":[{"groupItemTitle":"x","sportsMarketType":"spreads"}]}`, &spreads)
	if _, ok := polyMatchEvent(spreads, "UEFA Champions League"); ok {
		t.Error("non-moneyline event must be dropped")
	}

	var ended polyEvent
	mustJSON(t, `{"slug":"ucl-fen-liv-2026-11-04","startTime":"2026-11-04T17:45:00Z","ended":true,`+
		`"markets":[{"groupItemTitle":"x","sportsMarketType":"moneyline"}]}`, &ended)
	if _, ok := polyMatchEvent(ended, "UEFA Champions League"); ok {
		t.Error("ended event must be dropped")
	}

	var noStart polyEvent
	mustJSON(t, `{"slug":"ucl-fen-liv-2026-11-04","markets":[{"groupItemTitle":"x","sportsMarketType":"moneyline"}]}`, &noStart)
	if _, ok := polyMatchEvent(noStart, "UEFA Champions League"); ok {
		t.Error("event without startTime must be dropped")
	}
}

func TestPolyPrice(t *testing.T) {
	var m polyMarket
	mustJSON(t, `{"groupItemTitle":"x","outcomes":"[\"Yes\", \"No\"]","outcomePrices":"[\"0.155\", \"0.845\"]"}`, &m)
	price, bid, ask, ok := polyPrice(m)
	if !ok || !approx(price, 0.155) || bid != nil || ask != nil {
		t.Errorf("string prices = %v %v %v %v", price, bid, ask, ok)
	}

	mustJSON(t, `{"groupItemTitle":"x","outcomes":"[\"Yes\", \"No\"]","outcomePrices":"[\"0.155\", \"0.845\"]","bestBid":0.15,"bestAsk":0.16}`, &m)
	price, bid, ask, ok = polyPrice(m)
	if !ok || !approx(price, 0.155) || bid == nil || ask == nil {
		t.Errorf("bid/ask mid = %v %v %v %v", price, bid, ask, ok)
	}

	// an empty book side is not a price: fall back to the displayed Yes price
	mustJSON(t, `{"groupItemTitle":"x","outcomes":"[\"Yes\", \"No\"]","outcomePrices":"[\"0.42\", \"0.58\"]","bestBid":0,"bestAsk":1}`, &m)
	price, _, _, ok = polyPrice(m)
	if !ok || !approx(price, 0.42) {
		t.Errorf("empty book = %v %v", price, ok)
	}

	mustJSON(t, `{"groupItemTitle":"x","outcomes":"[\"No\", \"Yes\"]","outcomePrices":"[\"0.3\", \"0.7\"]"}`, &m)
	price, _, _, ok = polyPrice(m)
	if !ok || !approx(price, 0.7) {
		t.Errorf("yes at index 1 = %v %v", price, ok)
	}

	mustJSON(t, `{"groupItemTitle":"x","outcomes":"bad","outcomePrices":"[]"}`, &m)
	if _, _, _, ok := polyPrice(m); ok {
		t.Error("bad outcome JSON must not price")
	}
}

func TestKalshiEvent(t *testing.T) {
	var raw kalshiEventJSON
	mustJSON(t, `{"event_ticker":"KXUCLGAME-26NOV04FENLFC","markets":[
	  {"ticker":"KXUCLGAME-26NOV04FENLFC-FEN","yes_sub_title":"Fenerbahce","yes_bid_dollars":"0.1500","yes_ask_dollars":"0.1600","last_price_dollars":"0.1500","close_time":"2026-11-06T17:45:00Z"},
	  {"ticker":"KXUCLGAME-26NOV04FENLFC-TIE","yes_sub_title":"Tie","yes_bid_dollars":"0.1800","yes_ask_dollars":"0.1900","last_price_dollars":"0.1800","close_time":"2026-11-06T17:45:00Z"},
	  {"ticker":"KXUCLGAME-26NOV04FENLFC-LFC","yes_sub_title":"Liverpool","yes_bid_dollars":"0.0000","yes_ask_dollars":"1.0000","last_price_dollars":"0.6500","close_time":"2026-11-06T17:45:00Z"}
	]}`, &raw)
	ev := kalshiEvent(raw, "UEFA Champions League", "KXUCLGAME")
	if ev.Kickoff != time.Date(2026, 11, 4, 17, 45, 0, 0, time.UTC) {
		t.Errorf("kickoff = %s", ev.Kickoff)
	}
	if ev.URL != "https://kalshi.com/markets/kxuclgame/m/kxuclgame-26nov04fenlfc" {
		t.Errorf("url = %s", ev.URL)
	}
	if len(ev.Markets) != 3 || ev.Markets[1].Label != "draw" {
		t.Fatalf("markets = %+v", ev.Markets)
	}
	last := ev.Markets[2]
	if !approx(last.Price, 0.65) || last.Bid != nil || last.Ask != nil {
		t.Errorf("empty book = %+v, want last price and nil book", last)
	}
	if !approx(ev.Markets[0].Price, 0.155) || ev.Markets[0].Bid == nil || ev.Markets[0].Ask == nil {
		t.Errorf("book mid = %+v", ev.Markets[0])
	}

	var dead kalshiEventJSON
	mustJSON(t, `{"event_ticker":"KXUCLGAME-26NOV04FENLFC","markets":[
	  {"ticker":"KXUCLGAME-26NOV04FENLFC-FEN","yes_sub_title":"Fenerbahce","close_time":"2026-11-06T17:45:00Z"}
	]}`, &dead)
	if ev := kalshiEvent(dead, "UEFA Champions League", "KXUCLGAME"); len(ev.Markets) != 0 {
		t.Errorf("market with no price must be skipped: %+v", ev.Markets)
	}
}

func TestFedBuckets(t *testing.T) {
	cases := []struct {
		venue, label, ticker, want string
	}{
		{"polymarket", "50+ bps decrease", "", "decrease"},
		{"polymarket", "25 bps decrease", "", "decrease"},
		{"polymarket", "No change", "", "hold"},
		{"polymarket", "25 bps increase", "", "increase"},
		{"polymarket", "50+ bps increase", "", "increase"},
		{"kalshi", "Cut >25bps", "KXFEDDECISION-26DEC-C26", "decrease"},
		{"kalshi", "Cut 25bps", "KXFEDDECISION-26DEC-C25", "decrease"},
		{"kalshi", "Fed maintains rate", "KXFEDDECISION-26DEC-H0", "hold"},
		{"kalshi", "Hike 25bps", "KXFEDDECISION-26DEC-H25", "increase"},
		{"kalshi", "Hike >25bps", "KXFEDDECISION-26DEC-H26", "increase"},
		{"kalshi", "Something", "KXFEDDECISION-26DEC-X1", ""},
	}
	for _, c := range cases {
		if got := fedBucket(c.venue, c.label, c.ticker); got != c.want {
			t.Errorf("fedBucket(%s, %q, %q) = %q, want %q", c.venue, c.label, c.ticker, got, c.want)
		}
	}

	// Polymarket cuts: 50+ bps 0.0035 plus 25 bps 0.0045
	poly := []venueMarket{
		{Label: fedBucket("polymarket", "50+ bps decrease", ""), Price: 0.0035},
		{Label: fedBucket("polymarket", "25 bps decrease", ""), Price: 0.0045},
		{Label: fedBucket("polymarket", "No change", ""), Price: 0.225, Bid: ptr(0.22), Ask: ptr(0.23)},
	}
	sums := sumBuckets(poly)
	if d := sums["decrease"]; !approx(d.Price, 0.008) || d.Parts != 2 || d.Bid != nil || d.Ask != nil {
		t.Errorf("poly decrease = %+v", d)
	}
	if h := sums["hold"]; !approx(h.Price, 0.225) || h.Parts != 1 || h.Bid == nil {
		t.Errorf("poly hold = %+v", h)
	}

	var raw kalshiEventJSON
	mustJSON(t, `{"event_ticker":"KXFEDDECISION-26DEC","markets":[
	  {"ticker":"KXFEDDECISION-26DEC-C25","yes_sub_title":"Cut 25bps","yes_bid_dollars":"0.0000","yes_ask_dollars":"0.0100","last_price_dollars":"0.0100"},
	  {"ticker":"KXFEDDECISION-26DEC-C26","yes_sub_title":"Cut >25bps","yes_bid_dollars":"0.0000","yes_ask_dollars":"0.0100","last_price_dollars":"0.0100"},
	  {"ticker":"KXFEDDECISION-26DEC-H0","yes_sub_title":"Fed maintains rate","yes_bid_dollars":"0.2300","yes_ask_dollars":"0.2500","last_price_dollars":"0.2400"},
	  {"ticker":"KXFEDDECISION-26DEC-H25","yes_sub_title":"Hike 25bps","yes_bid_dollars":"0.7300","yes_ask_dollars":"0.7500","last_price_dollars":"0.7500"},
	  {"ticker":"KXFEDDECISION-26DEC-H26","yes_sub_title":"Hike >25bps","yes_bid_dollars":"0.0100","yes_ask_dollars":"0.0200","last_price_dollars":"0.0200"}
	]}`, &raw)
	ev := kalshiEvent(raw, "fed|December 2026", "KXFEDDECISION")
	if raised := sumBuckets(ev.Markets)["increase"]; !approx(raised.Price, 0.755) || raised.Parts != 2 {
		t.Errorf("kalshi increase = %+v, want 0.755 in 2 parts", raised)
	}
	if cut := sumBuckets(ev.Markets)["decrease"]; !approx(cut.Price, 0.02) || cut.Parts != 2 {
		t.Errorf("kalshi decrease = %+v, want 0.02 in 2 parts", cut)
	}
	if hold := sumBuckets(ev.Markets)["hold"]; !approx(hold.Price, 0.24) || hold.Parts != 1 {
		t.Errorf("kalshi hold = %+v", hold)
	}
}

func ptr(f float64) *float64 { return &f }

const compareMetaJSON = `{
  "outcomes":[
    {"outcome":22153,"name":"template:sportsContestParticipant2","description":"participant:Fenerbahce"},
    {"outcome":22154,"name":"template:sportsContestDraw2","description":""},
    {"outcome":22155,"name":"template:sportsContestParticipant2","description":"participant:Liverpool"},
    {"outcome":19988,"name":"template:policyRateNoChange","description":""},
    {"outcome":19989,"name":"template:policyRateDecrease","description":""},
    {"outcome":19990,"name":"template:policyRateIncrease","description":""},
    {"outcome":300,"name":"template:sportsTournamentParticipant","description":"participant:Arsenal"},
    {"outcome":301,"name":"template:sportsTournamentParticipant","description":"participant:Manchester City"},
    {"outcome":400,"name":"template:sportsContestParticipant2","description":"participant:AEK Athens"},
    {"outcome":401,"name":"template:sportsContestDraw2","description":""},
    {"outcome":402,"name":"template:sportsContestParticipant2","description":"participant:Real Madrid"},
    {"outcome":500,"name":"template:sportsContestParticipant2","description":"participant:Celtic"},
    {"outcome":501,"name":"template:sportsContestDraw2","description":""},
    {"outcome":502,"name":"template:sportsContestParticipant2","description":"participant:Besiktas"}
  ],
  "questions":[
    {"question":566,"name":"template:sportsContestResult","description":"competition:UEFA Champions League|participantA:Fenerbahce|participantB:Liverpool|scheduledStart:20261104-1745","fallbackOutcome":22152,"namedOutcomes":[22153,22154,22155]},
    {"question":545,"name":"template:policyRateDecision","description":"decisionLabel:December 2026|scheduledDecision:20261209-1900","fallbackOutcome":19987,"namedOutcomes":[19988,19989,19990]},
    {"question":198,"name":"template:sportsTournamentWinner","description":"competition:English Premier League|season:2026/2027","fallbackOutcome":299,"namedOutcomes":[300,301]},
    {"question":601,"name":"template:sportsContestResult","description":"competition:UEFA Champions League|participantA:AEK Athens|participantB:Real Madrid|scheduledStart:20261103-1745","fallbackOutcome":399,"namedOutcomes":[400,401,402]},
    {"question":602,"name":"template:sportsContestResult","description":"competition:UEFA Europa League|participantA:Celtic|participantB:Besiktas|scheduledStart:20261105-2000","fallbackOutcome":499,"namedOutcomes":[500,501,502]}
  ]
}`

func TestBuildCompare(t *testing.T) {
	var meta outcomeMeta
	mustJSON(t, compareMetaJSON, &meta)

	var matchPoly polyEvent
	mustJSON(t, matchEventJSON, &matchPoly)
	// Polymarket lists Liverpool first: the pairing has to swap home and away
	matchPoly.Markets[0].GroupItemTitle = "Liverpool FC"
	matchPoly.Markets[0].OutcomePrices = `["0.655", "0.345"]`
	matchPoly.Markets[0].BestBid, matchPoly.Markets[0].BestAsk = ptr(0.65), ptr(0.66)
	matchPoly.Markets[2].GroupItemTitle = "Fenerbahçe SK"
	matchPoly.Markets[2].OutcomePrices = `["0.155", "0.845"]`
	matchPoly.Markets[2].BestBid, matchPoly.Markets[2].BestAsk = ptr(0.15), ptr(0.16)
	polyMatch, ok := polyMatchEvent(matchPoly, "UEFA Champions League")
	if !ok {
		t.Fatal("fixture match event must parse")
	}

	var matchKalshi kalshiEventJSON
	mustJSON(t, `{"event_ticker":"KXUCLGAME-26NOV04FENLFC","markets":[
	  {"ticker":"KXUCLGAME-26NOV04FENLFC-FEN","yes_sub_title":"Fenerbahce","yes_bid_dollars":"0.1500","yes_ask_dollars":"0.1600","last_price_dollars":"0.1500","close_time":"2026-11-06T17:45:00Z"},
	  {"ticker":"KXUCLGAME-26NOV04FENLFC-TIE","yes_sub_title":"Tie","yes_bid_dollars":"0.1800","yes_ask_dollars":"0.1900","last_price_dollars":"0.1800","close_time":"2026-11-06T17:45:00Z"},
	  {"ticker":"KXUCLGAME-26NOV04FENLFC-LFC","yes_sub_title":"Liverpool","yes_bid_dollars":"0.6500","yes_ask_dollars":"0.6600","last_price_dollars":"0.6500","close_time":"2026-11-06T17:45:00Z"}
	]}`, &matchKalshi)
	kalshiMatch := kalshiEvent(matchKalshi, "UEFA Champions League", "KXUCLGAME")

	fed := venueEvent{
		Venue:       "polymarket",
		Competition: "fed|December 2026",
		URL:         "https://polymarket.com/event/fed-decision-in-december-20260729232808632",
		Markets: []venueMarket{
			{Label: "decrease", Price: 0.0035},
			{Label: "decrease", Price: 0.0045},
			{Label: "hold", Price: 0.225},
			{Label: "increase", Price: 0.745},
			{Label: "increase", Price: 0.0185},
		},
	}
	winner := venueEvent{
		Venue:       "polymarket",
		Competition: "winner|English Premier League|2026/2027",
		URL:         "https://polymarket.com/event/epl-2027-champion-20260701200428749",
		Markets: []venueMarket{
			{Label: "Arsenal", Price: 0.695, Bid: ptr(0.69), Ask: ptr(0.70)},
			{Label: "Chelsea", Price: 0.0365},
			{Label: "Manchester City", Price: 0.105},
			{Label: "Manchester United", Price: 0.0045},
		},
	}

	mids := map[string]string{
		"#221530": "0.17", "#221540": "0.20", "#221550": "0.63",
		"#199880": "0.225", "#199890": "0.004", "#199900": "0.75",
		"#3000": "0.70", "#3010": "0.09",
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	got := buildCompare(meta, mids, []venueEvent{polyMatch, kalshiMatch, fed, winner}, now)

	if len(got) != 3 {
		t.Fatalf("got %d questions, want 3: %+v", len(got), got)
	}

	m := got[0]
	if m.Question != 566 || m.Family != "match" || m.Title != "Fenerbahce vs Liverpool" || m.Competition != "UEFA Champions League" {
		t.Fatalf("match question = %+v", m)
	}
	if m.StartsAt == nil || !m.StartsAt.Equal(time.Date(2026, 11, 4, 17, 45, 0, 0, time.UTC)) || !m.UpdatedAt.Equal(now) {
		t.Errorf("match times = %v %v", m.StartsAt, m.UpdatedAt)
	}
	if len(m.Outcomes) != 3 || m.Outcomes[0].Key != "a" || m.Outcomes[1].Key != "draw" || m.Outcomes[2].Key != "b" {
		t.Fatalf("match outcomes = %+v", m.Outcomes)
	}
	a := m.Outcomes[0]
	if a.Label != "Fenerbahce" || len(a.Quotes) != 3 {
		t.Fatalf("outcome a = %+v", a)
	}
	if a.Quotes[0].Venue != "hyperliquid" || !approx(a.Quotes[0].Price, 0.17) || a.Quotes[0].URL != nil {
		t.Errorf("hyperliquid quote = %+v", a.Quotes[0])
	}
	if a.Quotes[1].Venue != "polymarket" || a.Quotes[1].Label != "Fenerbahçe SK" || !approx(a.Quotes[1].Price, 0.155) || a.Quotes[1].URL == nil {
		t.Errorf("polymarket quote = %+v", a.Quotes[1])
	}
	if a.Quotes[2].Venue != "kalshi" || a.Quotes[2].Label != "Fenerbahce" || !approx(a.Quotes[2].Price, 0.155) {
		t.Errorf("kalshi quote = %+v", a.Quotes[2])
	}
	b := m.Outcomes[2]
	if b.Label != "Liverpool" || len(b.Quotes) != 3 || b.Quotes[1].Label != "Liverpool FC" || !approx(b.Quotes[1].Price, 0.655) {
		t.Errorf("outcome b = %+v", b)
	}
	if d := m.Outcomes[1]; len(d.Quotes) != 3 || d.Quotes[2].Label != "draw" || !approx(d.Quotes[1].Price, 0.195) {
		t.Errorf("draw = %+v", d)
	}

	fq := got[1]
	if fq.Family != "fed" || fq.Title != "Fed decision, December 2026" || fq.Competition != "Federal Open Market Committee" {
		t.Fatalf("fed question = %+v", fq)
	}
	if fq.StartsAt == nil || !fq.StartsAt.Equal(time.Date(2026, 12, 9, 19, 0, 0, 0, time.UTC)) {
		t.Errorf("fed startsAt = %v", fq.StartsAt)
	}
	if len(fq.Outcomes) != 3 || fq.Outcomes[0].Key != "decrease" || fq.Outcomes[1].Key != "hold" || fq.Outcomes[2].Key != "increase" {
		t.Fatalf("fed outcomes = %+v", fq.Outcomes)
	}
	if d := fq.Outcomes[0]; !approx(d.Quotes[0].Price, 0.004) || !approx(d.Quotes[1].Price, 0.008) || d.Quotes[1].Parts != 2 || d.Quotes[1].Bid != nil {
		t.Errorf("fed decrease = %+v", d)
	}
	if h := fq.Outcomes[1]; h.Quotes[1].Parts != 1 || h.Quotes[1].Bid != nil {
		t.Errorf("fed hold = %+v", h)
	}

	w := got[2]
	if w.Question != 198 || w.Family != "winner" || w.Title != "English Premier League 2026/2027 winner" || w.StartsAt != nil {
		t.Fatalf("winner question = %+v", w)
	}
	if len(w.Outcomes) != 2 || w.Outcomes[0].Key != "Arsenal" || w.Outcomes[1].Key != "Manchester City" {
		t.Fatalf("winner outcomes = %+v", w.Outcomes)
	}
	if q := w.Outcomes[0].Quotes; !approx(q[0].Price, 0.70) || len(q) != 2 || !approx(q[1].Price, 0.695) || q[1].Bid == nil {
		t.Errorf("arsenal quotes = %+v", w.Outcomes[0].Quotes)
	}
	if q := w.Outcomes[1].Quotes; len(q) != 2 || !approx(q[1].Price, 0.105) {
		t.Errorf("manchester city quotes = %+v", w.Outcomes[1].Quotes)
	}
}
