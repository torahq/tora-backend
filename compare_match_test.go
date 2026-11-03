package main

import (
	"slices"
	"testing"
)

func TestNameTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"FC Bayern München", []string{"bayern", "munich"}},
		{"FK Bodø/Glimt", []string{"bodo", "glimt"}},
		{"Paris Saint-Germain FC", []string{"paris", "saint", "germain"}},
		{"PSG", []string{"paris", "saint", "germain"}},
		{"SK Slavia Praha", []string{"slavia", "praha"}},
		{"Slavia Prague", []string{"slavia", "praha"}},
		{"Como 1907", []string{"como"}},
		{"Club Atlético de Madrid", []string{"atletico", "madrid"}},
		{"TSG 1899 Hoffenheim", []string{"hoffenheim"}},
		{"Be`er Sheva", []string{"be", "er", "sheva"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := nameTokens(c.in); !slices.Equal(got, c.want) {
			t.Errorf("nameTokens(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNameScore(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"FC Bayern Munich", "FC Bayern München", 4},
		{"Paris Saint Germain", "PSG", 6},
		{"Real Madrid", "Liverpool", 0},
		{"Los Angeles", "Los Angeles C", 4},
		{"Manchester City", "Manchester United FC", 2},
	}
	for _, c := range cases {
		if got := nameScore(c.a, c.b); got != c.want {
			t.Errorf("nameScore(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPickBest(t *testing.T) {
	cases := []struct {
		name       string
		candidates []string
		want       int
	}{
		{"PSV Eindhoven", []string{"Eindhoven", "Club Brugge"}, 0},
		{"SK Slavia Praha", []string{"Slavia Prague", "Arsenal"}, 0},
		{"Sabah Baku", []string{"Sabah Masazir", "Viking"}, 0},
		{"Paris Saint Germain", []string{"PSG", "Villarreal"}, 0},
		{"Paris Saint Germain", []string{"Paris Saint-Germain FC", "Villarreal CF"}, 0},
		{"FC Bayern Munich", []string{"Club Atlético de Madrid", "FC Bayern München"}, 1},
		{"LOSC Lille", []string{"Lille OSC", "FK Bodø/Glimt"}, 0},
		{"FK Bodo/Glimt", []string{"Lille OSC", "FK Bodø/Glimt"}, 1},
		{"Inter Milan", []string{"Feyenoord Rotterdam", "FC Internazionale Milano"}, 1},
		{"Los Angeles Rams", []string{"Los Angeles C", "Los Angeles R", "Buffalo"}, 1},
		{"Oklahoma City Thunder", []string{"New York", "Oklahoma City", "Boston"}, 1},
		{"Manchester City", []string{"Manchester City FC", "Manchester United FC"}, 0},
		{"Los Angeles", []string{"Los Angeles C", "Los Angeles R"}, -1},
		{"Real Madrid", []string{"Liverpool", "Arsenal"}, -1},
	}
	for _, c := range cases {
		if got := pickBest(c.name, c.candidates); got != c.want {
			t.Errorf("pickBest(%q, %v) = %d, want %d", c.name, c.candidates, got, c.want)
		}
	}
}

func TestPairTeams(t *testing.T) {
	score, swapped, ok := pairTeams("Atletico Madrid", "FC Bayern Munich", "FC Bayern München", "Club Atlético de Madrid")
	if !ok || !swapped || score != 8 {
		t.Errorf("swapped = %d %v %v, want 8 true true", score, swapped, ok)
	}

	score, swapped, ok = pairTeams("Fenerbahce", "Liverpool", "Fenerbahce", "Liverpool")
	if !ok || swapped || score != 4 {
		t.Errorf("direct = %d %v %v, want 4 false true", score, swapped, ok)
	}

	if _, _, ok := pairTeams("Real Madrid", "Barcelona", "Liverpool", "Arsenal"); ok {
		t.Error("unrelated teams must not pair")
	}
	if _, _, ok := pairTeams("Real Madrid", "Barcelona", "Real Madrid", "Barcelona FC"); !ok {
		t.Error("partial names must pair")
	}
}
