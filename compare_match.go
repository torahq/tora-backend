package main

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Matching across venues has to cope with "FC Bayern Munich" vs "FC Bayern München", "SK Slavia Praha"
// vs "Slavia Prague" and Kalshi's city-only names ("Oklahoma City" for Oklahoma City Thunder). The
// rules are deliberately strict: a tie or a weak score means no match, never a guess.

var (
	diacritics = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	letterFix  = strings.NewReplacer("ø", "o", "ł", "l", "ı", "i", "đ", "d", "ß", "ss")
	nonAlnum   = regexp.MustCompile(`[^a-z0-9]+`)
	numOnly    = regexp.MustCompile(`^\d+$`)
)

var stopWords = map[string]bool{
	"fc": true, "cf": true, "sc": true, "sk": true, "kv": true, "afc": true, "ac": true,
	"as": true, "ssc": true, "bv": true, "tsg": true, "rc": true, "fk": true, "jk": true,
	"tc": true, "sl": true, "club": true, "de": true, "del": true, "la": true, "the": true,
	"and": true,
}

// aliases expand one token into several so both sides of a venue pair reduce to the same words.
var aliases = map[string][]string{
	"psg":            {"paris", "saint", "germain"},
	"munchen":        {"munich"},
	"prague":         {"praha"},
	"internazionale": {"inter"},
	"milano":         {"milan"},
	"utd":            {"united"},
	"spurs":          {"tottenham"},
}

// nameTokens lowercases, strips diacritics, drops punctuation, stop words and pure numbers, then
// expands aliases: "FC Bayern München" and "Bayern Munich" both become ["bayern","munich"].
func nameTokens(s string) []string {
	s = strings.ToLower(s)
	if t, _, err := transform.String(diacritics, s); err == nil {
		s = t
	}
	s = letterFix.Replace(s)
	var out []string
	for _, tok := range strings.Fields(nonAlnum.ReplaceAllString(s, " ")) {
		if stopWords[tok] || numOnly.MatchString(tok) {
			continue
		}
		if alias, ok := aliases[tok]; ok {
			out = append(out, alias...)
			continue
		}
		out = append(out, tok)
	}
	return out
}

// nameScore adds, for each token of a, its best match in b: equal 2, one a prefix of the other 1.
func nameScore(a, b string) int {
	bs := nameTokens(b)
	score := 0
	for _, x := range nameTokens(a) {
		best := 0
		for _, y := range bs {
			switch {
			case x == y:
				best = 2
			case best == 0 && (strings.HasPrefix(x, y) || strings.HasPrefix(y, x)):
				best = 1
			}
			if best == 2 {
				break
			}
		}
		score += best
	}
	return score
}

// pickBest returns the index of the unique best candidate scoring at least 2, or -1. A tie for the
// top score is not a match.
func pickBest(name string, candidates []string) int {
	best, bestScore, tie := -1, 1, false
	for i, c := range candidates {
		switch s := nameScore(name, c); {
		case s > bestScore:
			best, bestScore, tie = i, s, false
		case s == bestScore && best >= 0:
			tie = true
		}
	}
	if tie {
		return -1
	}
	return best
}

// pairTeams scores both ways of assigning a and b to t1 and t2. An assignment counts only when each
// side scores at least 1; the higher total wins and swapped reports that t1 landed on b.
func pairTeams(a, b, t1, t2 string) (score int, swapped bool, ok bool) {
	directA, directB := nameScore(a, t1), nameScore(b, t2)
	revA, revB := nameScore(a, t2), nameScore(b, t1)
	direct := directA >= 1 && directB >= 1
	rev := revA >= 1 && revB >= 1
	switch {
	case direct && (!rev || directA+directB >= revA+revB):
		return directA + directB, false, true
	case rev:
		return revA + revB, true, true
	}
	return 0, false, false
}
