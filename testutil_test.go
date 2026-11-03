package main

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, s string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), out); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
}
