package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Predict compare: HIP-4 questions next to the same questions on Polymarket and Kalshi. The API
// contract lives in docs/superpowers/specs/2026-11-04-predict-compare-design.md.

type cmpQuote struct {
	Venue string   `json:"venue"`
	Label string   `json:"label"`
	Price float64  `json:"price"`
	Bid   *float64 `json:"bid"`
	Ask   *float64 `json:"ask"`
	Parts int      `json:"parts"`
	URL   *string  `json:"url"`
}

type cmpOutcome struct {
	Key    string     `json:"key"`
	Label  string     `json:"label"`
	Quotes []cmpQuote `json:"quotes"`
}

type cmpQuestion struct {
	Question    int          `json:"question"`
	Family      string       `json:"family"`
	Title       string       `json:"title"`
	Competition string       `json:"competition"`
	StartsAt    *time.Time   `json:"startsAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	Outcomes    []cmpOutcome `json:"outcomes"`
}

// venueEvent is one venue event already reduced to what matching needs.
type venueEvent struct {
	Venue       string    // polymarket or kalshi
	Competition string    // HIP-4 competition name it belongs to, for example "UEFA Champions League"
	Kickoff     time.Time // zero for winners and Fed
	URL         string
	Markets     []venueMarket
}

type venueMarket struct {
	Label string // team name, "draw", or the Fed bucket key decrease/hold/increase
	Price float64
	Bid   *float64
	Ask   *float64
}

type venueRefs struct{ Poly, Kalshi string } // Polymarket event slug, Kalshi event ticker

// Keys are HIP-4 "competition|season" (competition trimmed). Update when a season rolls over.
var winnerEvents = map[string]venueRefs{
	"English Premier League|2026/2027":        {"epl-2027-champion-20260701200428749", "KXPREMIERLEAGUE-27"},
	"UEFA Champions League|2026/2027":         {"uefa-champions-league-2027-champion-20260701202025549", "KXUCL-27"},
	"NBA|2026/2027":                           {"nba-2027-champion", "KXNBA-27"},
	"USA National Football League (NFL)|2027": {"pro-football-2027-champion-20260729185915366", "KXSB-27"},
}

// Keys are HIP-4 decisionLabel.
var fedEvents = map[string]venueRefs{
	"December 2026": {"fed-decision-in-december-20260729232808632", "KXFEDDECISION-26DEC"},
}

// HIP-4 competition -> Polymarket tag and Kalshi series for match discovery.
var matchSources = map[string]struct{ PolyTag, KalshiSeries string }{
	"UEFA Champions League": {"ucl", "KXUCLGAME"},
	"UEFA Europa League":    {"uel", "KXUELGAME"},
}

var (
	polyMatchSlug = regexp.MustCompile(`^(ucl|uel)-[a-z0-9]+-[a-z0-9]+-\d{4}-\d{2}-\d{2}$`)
	matchWindow   = 30 * time.Minute
)

// polyEvent is the part of a gamma event the compare build reads. The gamma API also nests its
// markets here, which is why kalshiEventJSON uses the same shape.
type polyEvent struct {
	Slug      string       `json:"slug"`
	StartTime string       `json:"startTime"`
	Ended     bool         `json:"ended"`
	Markets   []polyMarket `json:"markets"`
}

type polyMarket struct {
	GroupItemTitle   string   `json:"groupItemTitle"`
	SportsMarketType string   `json:"sportsMarketType"`
	Outcomes         string   `json:"outcomes"`
	OutcomePrices    string   `json:"outcomePrices"`
	BestBid          *float64 `json:"bestBid"`
	BestAsk          *float64 `json:"bestAsk"`
}

type kalshiEventJSON struct {
	EventTicker string         `json:"event_ticker"`
	Markets     []kalshiMarket `json:"markets"`
}

type kalshiMarket struct {
	Ticker           string `json:"ticker"`
	YesSubTitle      string `json:"yes_sub_title"`
	YesBidDollars    string `json:"yes_bid_dollars"`
	YesAskDollars    string `json:"yes_ask_dollars"`
	LastPriceDollars string `json:"last_price_dollars"`
	CloseTime        string `json:"close_time"`
}

// polyMatchEvent keeps Polymarket's main 1X2 event for a match. Sibling events ("-more-markets",
// halftime, corners, player props) fail the slug or market-type test, and finished events are dropped.
func polyMatchEvent(e polyEvent, competition string) (venueEvent, bool) {
	kick, err := time.Parse(time.RFC3339, e.StartTime)
	if err != nil || e.Ended || !polyMatchSlug.MatchString(e.Slug) || len(e.Markets) == 0 {
		return venueEvent{}, false
	}
	ms := make([]venueMarket, 0, len(e.Markets))
	for _, m := range e.Markets {
		if m.SportsMarketType != "moneyline" {
			return venueEvent{}, false
		}
		price, bid, ask, ok := polyPrice(m)
		if !ok {
			continue
		}
		label := m.GroupItemTitle
		if strings.HasPrefix(label, "Draw") {
			label = "draw"
		}
		ms = append(ms, venueMarket{Label: label, Price: price, Bid: bid, Ask: ask})
	}
	if len(ms) == 0 {
		return venueEvent{}, false
	}
	return venueEvent{
		Venue:       "polymarket",
		Competition: competition,
		Kickoff:     kick.UTC(),
		URL:         "https://polymarket.com/event/" + e.Slug,
		Markets:     ms,
	}, true
}

// polyMarkets reads winner and Fed events: one market per outcome, labelled by groupItemTitle.
func polyMarkets(e polyEvent) []venueMarket {
	out := make([]venueMarket, 0, len(e.Markets))
	for _, m := range e.Markets {
		price, bid, ask, ok := polyPrice(m)
		if !ok {
			continue
		}
		out = append(out, venueMarket{Label: m.GroupItemTitle, Price: price, Bid: bid, Ask: ask})
	}
	return out
}

// polyPrice reads a Polymarket binary market. outcomePrices is a JSON string of decimal strings and
// the Yes price is at the index of "Yes" in outcomes (also a JSON string). The mid of bestBid and
// bestAsk wins when the book has both sides inside 0 and 1.
func polyPrice(m polyMarket) (price float64, bid, ask *float64, ok bool) {
	var outcomes, prices []string
	if json.Unmarshal([]byte(m.Outcomes), &outcomes) != nil || json.Unmarshal([]byte(m.OutcomePrices), &prices) != nil {
		return 0, nil, nil, false
	}
	i := slices.Index(outcomes, "Yes")
	if i < 0 || i >= len(prices) {
		return 0, nil, nil, false
	}
	yes, err := strconv.ParseFloat(prices[i], 64)
	if err != nil {
		return 0, nil, nil, false
	}
	price, bid, ask = yes, m.BestBid, m.BestAsk
	if bid != nil && ask != nil && *bid > 0 && *bid <= *ask && *ask < 1 {
		price = (*bid + *ask) / 2
	}
	return price, bid, ask, true
}

// kalshiEvent reads a Kalshi event with nested markets. Fed buckets are carried in the market ticker
// suffix, so their labels become decrease/hold/increase. A match series reports its kickoff as
// close_time minus 48 hours.
func kalshiEvent(e kalshiEventJSON, competition, series string) venueEvent {
	ev := venueEvent{
		Venue:       "kalshi",
		Competition: competition,
		URL: fmt.Sprintf("https://kalshi.com/markets/%s/m/%s",
			strings.ToLower(series), strings.ToLower(e.EventTicker)),
	}
	if _, ok := matchSources[competition]; ok && len(e.Markets) > 0 {
		if t, err := time.Parse(time.RFC3339, e.Markets[0].CloseTime); err == nil {
			ev.Kickoff = t.Add(-48 * time.Hour).UTC()
		}
	}
	fed := strings.HasPrefix(strings.ToUpper(series), "KXFEDDECISION")
	for _, m := range e.Markets {
		label := m.YesSubTitle
		switch {
		case fed:
			label = fedBucket("kalshi", label, m.Ticker)
			if label == "" {
				continue
			}
		case label == "Tie":
			label = "draw"
		}
		price, bid, ask, ok := kalshiPrice(m)
		if !ok {
			continue
		}
		ev.Markets = append(ev.Markets, venueMarket{Label: label, Price: price, Bid: bid, Ask: ask})
	}
	return ev
}

// kalshiPrice reads Kalshi's dollar strings. A zero bid or an ask of 1 is an empty book side, not a
// price, so it becomes null. Without both sides the last trade is the price.
func kalshiPrice(m kalshiMarket) (price float64, bid, ask *float64, ok bool) {
	if v, err := strconv.ParseFloat(m.YesBidDollars, 64); err == nil && v > 0 && v < 1 {
		bid = &v
	}
	if v, err := strconv.ParseFloat(m.YesAskDollars, 64); err == nil && v > 0 && v < 1 {
		ask = &v
	}
	if bid != nil && ask != nil && *bid <= *ask {
		return (*bid + *ask) / 2, bid, ask, true
	}
	v, err := strconv.ParseFloat(m.LastPriceDollars, 64)
	if err != nil || v <= 0 {
		return 0, nil, nil, false
	}
	return v, bid, ask, true
}

// fedBucket maps a venue's finer Fed market to a HIP-4 bucket. Polymarket spells the bucket out;
// Kalshi uses the ticker suffix (C25/C26 cuts, H0 hold, H25/H26 hikes).
func fedBucket(venue, label, ticker string) string {
	switch venue {
	case "polymarket":
		lower := strings.ToLower(label)
		switch {
		case strings.Contains(lower, "decrease"):
			return "decrease"
		case strings.Contains(lower, "no change"):
			return "hold"
		case strings.Contains(lower, "increase"):
			return "increase"
		}
	case "kalshi":
		suffix := ticker[strings.LastIndex(ticker, "-")+1:]
		switch {
		case strings.HasPrefix(suffix, "C"):
			return "decrease"
		case suffix == "H0":
			return "hold"
		case strings.HasPrefix(suffix, "H"):
			return "increase"
		}
	}
	return ""
}

// sumBuckets adds finer venue markets into HIP-4 buckets (cuts = 25 bps + 50+ bps). A sum is not a
// tradable quote, so it keeps no bid or ask and says how many markets it added.
func sumBuckets(ms []venueMarket) map[string]cmpQuote {
	sums := map[string]*cmpQuote{}
	for _, m := range ms {
		q, ok := sums[m.Label]
		if !ok {
			q = &cmpQuote{Label: m.Label, Bid: m.Bid, Ask: m.Ask}
			sums[m.Label] = q
		}
		q.Price += m.Price
		q.Parts++
		if q.Parts > 1 {
			q.Bid, q.Ask = nil, nil
		}
	}
	out := make(map[string]cmpQuote, len(sums))
	for k, q := range sums {
		if q.Parts > 1 {
			q.Label = fmt.Sprintf("%s, %d markets", k, q.Parts)
		}
		out[k] = *q
	}
	return out
}

// buildCompare turns HIP-4 questions and already-normalized venue events into the API payload. A
// question survives only when at least one non-Hyperliquid quote matched.
func buildCompare(meta outcomeMeta, mids map[string]string, events []venueEvent, now time.Time) []cmpQuestion {
	byID := make(map[int]hlOutcome, len(meta.Outcomes))
	for _, o := range meta.Outcomes {
		byID[o.Outcome] = o
	}
	var out []cmpQuestion
	for _, q := range meta.Questions {
		if len(q.SettledNamedOutcomes) > 0 {
			continue
		}
		f := parseFields(q.Description)
		var (
			cq cmpQuestion
			ok bool
		)
		switch q.Name {
		case "template:sportsContestResult":
			cq, ok = compareMatch(q, f, byID, mids, events, now)
		case "template:policyRateDecision":
			cq, ok = compareFed(q, f, byID, mids, events)
		case "template:sportsTournamentWinner":
			cq, ok = compareWinner(q, f, byID, mids, events)
		}
		if ok {
			cq.UpdatedAt = now.UTC()
			out = append(out, cq)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].StartsAt, out[j].StartsAt
		switch {
		case a == nil && b == nil:
			return out[i].Title < out[j].Title
		case a == nil:
			return false
		case b == nil:
			return true
		case a.Equal(*b):
			return out[i].Title < out[j].Title
		}
		return a.Before(*b)
	})
	return out
}

// hlQuote prices a HIP-4 outcome from allMids. Outcome n trades as "#10n".
func hlQuote(o hlOutcome, label string, mids map[string]string) (cmpQuote, bool) {
	s, ok := mids[fmt.Sprintf("#%d", 10*o.Outcome)]
	if !ok {
		return cmpQuote{}, false
	}
	p, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return cmpQuote{}, false
	}
	return cmpQuote{Venue: "hyperliquid", Label: label, Price: p, Parts: 1}, true
}

func addQuote(o *cmpOutcome, q cmpQuote) {
	if o != nil {
		o.Quotes = append(o.Quotes, q)
	}
}

func hasVenueQuote(outcomes []cmpOutcome) bool {
	for _, o := range outcomes {
		for _, q := range o.Quotes {
			if q.Venue != "hyperliquid" {
				return true
			}
		}
	}
	return false
}

func within(d, limit time.Duration) bool { return d <= limit && d >= -limit }

// compareMatch pairs an upcoming HIP-4 football match with venue events of the same competition whose
// kickoff is within 30 minutes and whose two team names fit both participants.
func compareMatch(q hlQuestion, f map[string]string, byID map[int]hlOutcome, mids map[string]string, events []venueEvent, now time.Time) (cmpQuestion, bool) {
	comp := f["competition"]
	if _, ok := matchSources[comp]; !ok {
		return cmpQuestion{}, false
	}
	kick, ok := parseStamp(f["scheduledStart"])
	if !ok || !kick.After(now) {
		return cmpQuestion{}, false
	}
	partA, partB := f["participantA"], f["participantB"]

	slots := map[string]*cmpOutcome{}
	participants := 0
	for _, id := range q.NamedOutcomes {
		o, ok := byID[id]
		if !ok {
			continue
		}
		var key, label string
		switch strip(o.Name) {
		case "sportsContestDraw2":
			key, label = "draw", "Draw"
		case "sportsContestParticipant2":
			label = parseFields(o.Description)["participant"]
			key = "a"
			switch pickBest(label, []string{partA, partB}) {
			case 1:
				key = "b"
			case -1:
				if participants > 0 {
					key = "b"
				}
			}
			participants++
		default:
			continue
		}
		if _, seen := slots[key]; seen {
			continue
		}
		hl, ok := hlQuote(o, label, mids)
		if !ok {
			continue
		}
		slots[key] = &cmpOutcome{Key: key, Label: label, Quotes: []cmpQuote{hl}}
	}

	type pick struct {
		ev         venueEvent
		a, b, draw *venueMarket
		score      int
	}
	best := map[string]pick{}
	tied := map[string]bool{}
	for _, e := range events {
		if e.Competition != comp || !within(e.Kickoff.Sub(kick), matchWindow) {
			continue
		}
		var teams []venueMarket
		var draw *venueMarket
		for i := range e.Markets {
			if m := &e.Markets[i]; m.Label == "draw" {
				draw = m
			} else {
				teams = append(teams, *m)
			}
		}
		if len(teams) != 2 {
			continue
		}
		score, swapped, ok := pairTeams(partA, partB, teams[0].Label, teams[1].Label)
		if !ok {
			continue
		}
		aMkt, bMkt := teams[0], teams[1]
		if swapped {
			aMkt, bMkt = teams[1], teams[0]
		}
		cur, seen := best[e.Venue]
		switch {
		case !seen || score > cur.score:
			best[e.Venue] = pick{ev: e, a: &aMkt, b: &bMkt, draw: draw, score: score}
			delete(tied, e.Venue)
		case score == cur.score:
			tied[e.Venue] = true
		}
	}

	for _, v := range []string{"polymarket", "kalshi"} {
		p, ok := best[v]
		if !ok || tied[v] {
			continue
		}
		u := p.ev.URL
		addQuote(slots["a"], cmpQuote{Venue: v, Label: p.a.Label, Price: p.a.Price, Bid: p.a.Bid, Ask: p.a.Ask, Parts: 1, URL: &u})
		if p.draw != nil {
			addQuote(slots["draw"], cmpQuote{Venue: v, Label: p.draw.Label, Price: p.draw.Price, Bid: p.draw.Bid, Ask: p.draw.Ask, Parts: 1, URL: &u})
		}
		addQuote(slots["b"], cmpQuote{Venue: v, Label: p.b.Label, Price: p.b.Price, Bid: p.b.Bid, Ask: p.b.Ask, Parts: 1, URL: &u})
	}

	var outcomes []cmpOutcome
	for _, key := range []string{"a", "draw", "b"} {
		if o, ok := slots[key]; ok {
			outcomes = append(outcomes, *o)
		}
	}
	if !hasVenueQuote(outcomes) {
		return cmpQuestion{}, false
	}
	at := kick.UTC()
	return cmpQuestion{
		Question:    q.Question,
		Family:      "match",
		Title:       partA + " vs " + partB,
		Competition: comp,
		StartsAt:    &at,
		UpdatedAt:   now.UTC(),
		Outcomes:    outcomes,
	}, true
}

// compareFed adds up the venues' 25 bps and 50+ bps markets into HIP-4's three buckets.
func compareFed(q hlQuestion, f map[string]string, byID map[int]hlOutcome, mids map[string]string, events []venueEvent) (cmpQuestion, bool) {
	label := f["decisionLabel"]
	if _, ok := fedEvents[label]; !ok {
		return cmpQuestion{}, false
	}
	keys := map[string]string{
		"policyRateDecrease": "decrease",
		"policyRateNoChange": "hold",
		"policyRateIncrease": "increase",
	}
	labels := map[string]string{"decrease": "Decrease", "hold": "No change", "increase": "Increase"}
	slots := map[string]*cmpOutcome{}
	for _, id := range q.NamedOutcomes {
		o, ok := byID[id]
		if !ok {
			continue
		}
		key, ok := keys[strip(o.Name)]
		if !ok {
			continue
		}
		hl, ok := hlQuote(o, labels[key], mids)
		if !ok {
			continue
		}
		slots[key] = &cmpOutcome{Key: key, Label: labels[key], Quotes: []cmpQuote{hl}}
	}

	comp := "fed|" + label
	for _, e := range events {
		if e.Competition != comp {
			continue
		}
		u := e.URL
		for key, s := range sumBuckets(e.Markets) {
			s.Venue, s.URL = e.Venue, &u
			addQuote(slots[key], s)
		}
	}

	var outcomes []cmpOutcome
	for _, key := range []string{"decrease", "hold", "increase"} {
		if o, ok := slots[key]; ok {
			outcomes = append(outcomes, *o)
		}
	}
	if !hasVenueQuote(outcomes) {
		return cmpQuestion{}, false
	}
	cq := cmpQuestion{
		Question:    q.Question,
		Family:      "fed",
		Title:       "Fed decision, " + label,
		Competition: "Federal Open Market Committee",
		Outcomes:    outcomes,
	}
	if at, ok := parseStamp(f["scheduledDecision"]); ok {
		at = at.UTC()
		cq.StartsAt = &at
	}
	return cq, true
}

// compareWinner matches only HIP-4's named participants against a venue's full winner list.
func compareWinner(q hlQuestion, f map[string]string, byID map[int]hlOutcome, mids map[string]string, events []venueEvent) (cmpQuestion, bool) {
	comp := strings.TrimSpace(f["competition"])
	key := comp + "|" + f["season"]
	if _, ok := winnerEvents[key]; !ok {
		return cmpQuestion{}, false
	}

	var outcomes []cmpOutcome
	for _, id := range q.NamedOutcomes {
		o, ok := byID[id]
		if !ok || strip(o.Name) != "sportsTournamentParticipant" {
			continue
		}
		name := parseFields(o.Description)["participant"]
		hl, ok := hlQuote(o, name, mids)
		if !ok {
			continue
		}
		outcomes = append(outcomes, cmpOutcome{Key: name, Label: name, Quotes: []cmpQuote{hl}})
	}

	comp = "winner|" + key
	for _, e := range events {
		if e.Competition != comp {
			continue
		}
		labels := make([]string, len(e.Markets))
		for i, m := range e.Markets {
			labels[i] = m.Label
		}
		u := e.URL
		for i := range outcomes {
			idx := pickBest(outcomes[i].Key, labels)
			if idx < 0 {
				continue
			}
			m := e.Markets[idx]
			addQuote(&outcomes[i], cmpQuote{Venue: e.Venue, Label: m.Label, Price: m.Price, Bid: m.Bid, Ask: m.Ask, Parts: 1, URL: &u})
		}
	}

	if !hasVenueQuote(outcomes) {
		return cmpQuestion{}, false
	}
	return cmpQuestion{
		Question:    q.Question,
		Family:      "winner",
		Title:       strings.TrimSpace(f["competition"]) + " " + f["season"] + " winner",
		Competition: strings.TrimSpace(f["competition"]),
		Outcomes:    outcomes,
	}, true
}
