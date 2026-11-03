package main

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type hlOutcome struct {
	Outcome     int    `json:"outcome"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SideSpecs   []struct {
		Name string `json:"name"`
	} `json:"sideSpecs"`
}

type hlQuestion struct {
	Question             int    `json:"question"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	FallbackOutcome      int    `json:"fallbackOutcome"`
	NamedOutcomes        []int  `json:"namedOutcomes"`
	SettledNamedOutcomes []int  `json:"settledNamedOutcomes"`
}

type outcomeMeta struct {
	Outcomes  []hlOutcome  `json:"outcomes"`
	Questions []hlQuestion `json:"questions"`
}

type template struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Market is one yes/no prediction market, normalized across venues.
type Market struct {
	Venue     string     `json:"venue"`
	VenueID   string     `json:"venueId"`
	Title     string     `json:"title"`
	Category  string     `json:"category"` // crypto, sports or other
	ClosesAt  *time.Time `json:"closesAt"`
	Yes       float64    `json:"yes"` // 0..1
	URL       string     `json:"url"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

func strip(s string) string { return strings.TrimPrefix(s, "template:") }

// parseFields reads "k:v|k:v" descriptions. Values may contain colons (URLs).
func parseFields(desc string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(desc, "|") {
		if i := strings.Index(part, ":"); i > 0 {
			out[part[:i]] = part[i+1:]
		}
	}
	return out
}

var stampRe = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})$`)

// parseStamp reads "20261106-0800" (UTC).
func parseStamp(s string) (time.Time, bool) {
	m := stampRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	return time.Date(n(1), time.Month(n(2)), n(3), n(4), n(5), 0, 0, time.UTC), true
}

// stampLabel prints "Nov 6, 08:00 UTC".
func stampLabel(t time.Time) string { return t.UTC().Format("Jan 2, 15:04") + " UTC" }

var (
	numRe       = regexp.MustCompile(`^\d+(\.\d+)?$`)
	priceKeyRe  = regexp.MustCompile(`(?i)threshold|price`)
	placeholder = regexp.MustCompile(`\{(\w+)\}`)
	priceIDRe   = regexp.MustCompile(`(?i)price`)
)

// priceText adds thousands separators to prices and thresholds (at most 8 decimals). Other values stay as written.
func priceText(key, v string) string {
	if !priceKeyRe.MatchString(key) || !numRe.MatchString(v) {
		return v
	}
	f, _ := strconv.ParseFloat(v, 64)
	s := strconv.FormatFloat(math.Round(f*1e8)/1e8, 'f', -1, 64)
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if frac != "" {
		b.WriteString("." + frac)
	}
	return b.String()
}

// fillTemplate fills "{key}" from fields. Dates become labels; unknown keys print their name.
func fillTemplate(pattern string, fields map[string]string) string {
	return placeholder.ReplaceAllStringFunc(pattern, func(m string) string {
		k := m[1 : len(m)-1]
		v, ok := fields[k]
		if !ok {
			return k
		}
		if t, ok := parseStamp(v); ok {
			return stampLabel(t)
		}
		return priceText(k, v)
	})
}

// recurringTitle names Hyperliquid's recurring price markets ("class:priceBinary|..." or "class:priceBucket|...").
// bucket is the outcome's index in a priceBucket question (0 below, 1 between, 2 above), -1 otherwise.
func recurringTitle(fields map[string]string, bucket int) (string, bool) {
	when, ok := parseStamp(fields["expiry"])
	coin := fields["underlying"]
	if coin == "" || !ok {
		return "", false
	}
	at := stampLabel(when)
	switch fields["class"] {
	case "priceBinary":
		if fields["targetPrice"] != "" {
			return fmt.Sprintf("%s above %s at %s?", coin, priceText("price", fields["targetPrice"]), at), true
		}
	case "priceBucket":
		lohi := strings.Split(fields["priceThresholds"], ",")
		if len(lohi) < 2 || lohi[0] == "" || lohi[1] == "" {
			return "", false
		}
		lo, hi := priceText("price", lohi[0]), priceText("price", lohi[1])
		switch bucket {
		case 0:
			return fmt.Sprintf("%s below %s at %s?", coin, lo, at), true
		case 1:
			return fmt.Sprintf("%s between %s and %s at %s?", coin, lo, hi, at), true
		case 2:
			return fmt.Sprintf("%s above %s at %s?", coin, hi, at), true
		}
	}
	return "", false
}

func category(templateID string) string {
	switch {
	case priceIDRe.MatchString(templateID):
		return "crypto"
	case strings.HasPrefix(strings.ToLower(templateID), "sports"):
		return "sports"
	}
	return "other"
}

// toMarkets mirrors toOutcomeMarkets in src/lib/outcomes.ts: tradable binary HIP-4 markets with readable titles.
// Fallback and settled outcomes, markets past their close and markets with no Yes price are left out. Soonest close first.
func toMarkets(meta outcomeMeta, templates []template, mids map[string]string, now time.Time) []Market {
	tpl := map[string]template{}
	for _, t := range templates {
		tpl[t.ID] = t
	}
	fallback, settled := map[int]bool{}, map[int]bool{}
	questionOf := map[int]*hlQuestion{}
	for i := range meta.Questions {
		q := &meta.Questions[i]
		fallback[q.FallbackOutcome] = true
		for _, o := range q.SettledNamedOutcomes {
			settled[o] = true
		}
		for _, o := range q.NamedOutcomes {
			questionOf[o] = q
		}
	}
	title := func(name, desc string) string {
		if strings.HasPrefix(name, "template:") {
			if t, ok := tpl[strip(name)]; ok {
				return fillTemplate(t.Name, parseFields(desc))
			}
		}
		return strip(name)
	}

	var out []Market
	for _, o := range meta.Outcomes {
		if fallback[o.Outcome] || settled[o.Outcome] || len(o.SideSpecs) != 2 {
			continue
		}
		q := questionOf[o.Outcome]
		fields := map[string]string{}
		if q != nil {
			for k, v := range parseFields(q.Description) {
				fields[k] = v
			}
		}
		for k, v := range parseFields(o.Description) {
			fields[k] = v
		}
		var closes *time.Time
		for _, k := range []string{"time", "expiry", "resolutionDeadline", "scheduledStart"} {
			if v, ok := fields[k]; ok {
				if t, ok := parseStamp(v); ok {
					closes = &t
				}
				break
			}
		}
		if closes != nil && closes.Before(now) {
			continue
		}
		mid, ok := mids[fmt.Sprintf("#%d", 10*o.Outcome)]
		if !ok {
			continue
		}
		yes, err := strconv.ParseFloat(mid, 64)
		if err != nil {
			continue
		}

		m := Market{Venue: "hyperliquid", VenueID: strconv.Itoa(o.Outcome), ClosesAt: closes, Yes: yes, URL: "/predict"}
		rt, recurring := "", false
		if strings.Contains(o.Description, "class:") {
			rt, recurring = recurringTitle(parseFields(o.Description), -1)
		} else if q != nil && strings.Contains(q.Description, "class:") {
			rt, recurring = recurringTitle(parseFields(q.Description), slices.Index(q.NamedOutcomes, o.Outcome))
		}
		own := title(o.Name, o.Description)
		switch {
		case recurring:
			m.Title, m.Category = rt, "crypto"
		case q != nil:
			m.Title = title(q.Name, q.Description) + ": " + own
		default:
			m.Title = own
		}
		if !recurring {
			id := o.Name
			if q != nil && strings.HasPrefix(q.Name, "template:") {
				id = q.Name
			}
			m.Category = category(strip(id))
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].ClosesAt, out[j].ClosesAt
		if a == nil || b == nil {
			return a != nil
		}
		return a.Before(*b)
	})
	return out
}
