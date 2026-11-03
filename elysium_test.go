package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func w64(n int) string { return fmt.Sprintf("%064x", n) }

// mcReturn ABI-encodes a Multicall3 Result[] return value.
func mcReturn(results []mcResult) string {
	heads := make([]string, len(results))
	var bodies strings.Builder
	offset := 32 * len(results)
	for i, r := range results {
		data := strings.TrimPrefix(r.Data, "0x")
		pad := strings.Repeat("0", (64-len(data)%64)%64)
		heads[i] = w64(offset)
		offset += 96 + (len(data)+len(pad))/2 // bool, bytes offset, length, data
		success := 0
		if r.OK {
			success = 1
		}
		bodies.WriteString(w64(success))
		bodies.WriteString(w64(0x40))
		bodies.WriteString(w64(len(data) / 2))
		bodies.WriteString(data)
		bodies.WriteString(pad)
	}
	return "0x" + w64(0x20) + w64(len(results)) + strings.Join(heads, "") + bodies.String()
}

// getList serves path and decodes a JSON array body.
func getList(t *testing.T, s Store, path string) (int, []any) {
	t.Helper()
	rec := httptest.NewRecorder()
	routes(s, func() time.Time { return clock }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body []any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: body is not a JSON array: %q", path, rec.Body.String())
	}
	return rec.Code, body
}

func TestEncodeAggregate3(t *testing.T) {
	const token = "0x7d29d8047b905000459c0e80c34a26ceedcb47b2"
	got := encodeAggregate3([]mcCall{{Target: token, Data: "0x313ce567"}})
	want := "0x82ad56cb" + w64(0x20) + w64(1) + w64(0x20) +
		strings.Repeat("0", 24) + strings.TrimPrefix(token, "0x") +
		w64(1) + w64(0x60) + w64(4) + "313ce567" + strings.Repeat("0", 56)
	if got != want {
		t.Errorf("calldata\n got %s\nwant %s", got, want)
	}
}

func TestDecodeAggregate3(t *testing.T) {
	tokens := "0x" + strings.Repeat("0", 63) + "6"
	res, err := decodeAggregate3(mcReturn([]mcResult{{OK: true, Data: tokens}, {OK: false, Data: "0x"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || !res[0].OK || res[0].Data != tokens {
		t.Fatalf("results = %+v", res)
	}
	if res[1].OK || res[1].Data != "0x" {
		t.Errorf("failed result = %+v", res[1])
	}
	if n, ok := uintWord(res[0].Data); !ok || n != 6 {
		t.Errorf("uintWord = %d %v", n, ok)
	}
}

func TestFetchTokensBatchesMetadata(t *testing.T) {
	const (
		t1 = "0x1111111111111111111111111111111111111111"
		t2 = "0x2222222222222222222222222222222222222222"
	)
	results := []mcResult{
		{OK: true, Data: wInt(6)}, {OK: true, Data: abiString("USDC")}, {OK: true, Data: abiString("USD Coin")},
		{OK: true, Data: wInt(18)}, {OK: true, Data: bytes32String("WHYPE")}, {OK: true, Data: abiString("Wrapped HYPE")},
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": mcReturn(results)})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	e := ely{URL: srv.URL, Client: srv.Client()}
	tokens, err := e.fetchTokens(context.Background(), fakeStore{}, []string{t1, t2, zeroAddr})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want one batched call", calls)
	}
	if len(tokens) != 2 {
		t.Fatalf("tokens = %+v", tokens)
	}
	if tokens[0].Symbol != "USDC" || tokens[0].Name != "USD Coin" || tokens[0].Decimals != 6 {
		t.Errorf("token 1 = %+v", tokens[0])
	}
	if tokens[1].Symbol != "WHYPE" || tokens[1].Name != "Wrapped HYPE" || tokens[1].Decimals != 18 {
		t.Errorf("token 2 = %+v", tokens[1])
	}
}

func TestElysiumTokensEndpoint(t *testing.T) {
	tokens := []elyTokenRow{
		{elyToken: elyToken{Address: zeroAddr, Symbol: "HYPE", Name: "Hyperliquid", Decimals: 18}, Pools: 3},
		{elyToken: elyToken{Address: "0x7d29d8047b905000459c0e80c34a26ceedcb47b2", Symbol: "USDC", Name: "USD Coin", Decimals: 6}, Pools: 2},
	}
	code, list := getList(t, fakeStore{tokens: tokens}, "/v1/elysium/tokens")
	if code != 200 || len(list) != 2 {
		t.Fatalf("list: %d %v", code, list)
	}
	row, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("row is not an object: %v", list[0])
	}
	if row["address"] != zeroAddr || row["symbol"] != "HYPE" || row["name"] != "Hyperliquid" || row["decimals"] != 18.0 || row["pools"] != 3.0 {
		t.Errorf("row = %v", row)
	}
	if _, ok := row["block"]; ok {
		t.Errorf("token row must not expose block: %v", row)
	}

	code, list = getList(t, fakeStore{tokens: tokens}, "/v1/elysium/tokens?q=usdc&limit=5")
	if code != 200 || len(list) != 2 {
		t.Errorf("query: %d %v", code, list)
	}

	code, list = getList(t, fakeStore{}, "/v1/elysium/tokens")
	if code != 200 || len(list) != 0 {
		t.Errorf("empty must be an empty array: %d %v", code, list)
	}

	code, body := get(t, fakeStore{err: errors.New("pg down")}, "/v1/elysium/tokens")
	if code != 503 || body["error"] != "elysium tokens unavailable" {
		t.Errorf("store error: %d %v", code, body)
	}
}

func TestElyCallRetriesRateLimit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`))
	}))
	defer srv.Close()

	e := ely{URL: srv.URL, Client: srv.Client()}
	var got string
	if err := e.call(context.Background(), "eth_blockNumber", nil, &got); err != nil {
		t.Fatalf("call: %v", err)
	}
	if calls != 2 || got != "0x10" {
		t.Fatalf("calls = %d, result = %q", calls, got)
	}
}

func TestElysiumPoolsEndpoint(t *testing.T) {
	const (
		usdc  = "0x7d29d8047b905000459c0e80c34a26ceedcb47b2"
		weth  = "0xff5681eb95bbacfbb334f80fcdbc31fb59f22332"
		whype = "0xcd57f65c2b0e5881cfc2e609f7cd53b746e1f234"
	)
	bad := []string{
		"/v1/elysium/pools",
		"/v1/elysium/pools?tokens=" + usdc,
		"/v1/elysium/pools?tokens=" + strings.Join([]string{usdc, weth, whype, usdc, weth, whype, usdc, weth, whype, usdc, weth}, ","),
		"/v1/elysium/pools?tokens=" + usdc + "," + strings.ToUpper(weth),
		"/v1/elysium/pools?tokens=" + usdc + ",nope",
		"/v1/elysium/pools?tokens=" + usdc + ",0x1234",
		"/v1/elysium/pools?tokens=" + usdc + ",",
	}
	for _, path := range bad {
		code, body := get(t, fakeStore{}, path)
		if code != 400 || body["error"] != "pass 2 to 10 token addresses" {
			t.Errorf("%s: %d %v", path, code, body)
		}
	}

	pools := []elyPool{{
		Venue: "uniswap-v4", Kind: "v4", Pool: "0x" + strings.Repeat("ab", 32),
		Token0: usdc, Token1: whype, Fee: 3000, TickSpacing: 60, Hooks: zeroAddr,
	}}
	path := "/v1/elysium/pools?tokens=" + usdc + "," + weth + "," + whype
	code, list := getList(t, fakeStore{pools: pools}, path)
	if code != 200 || len(list) != 1 {
		t.Fatalf("pools: %d %v", code, list)
	}
	row, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("pool is not an object: %v", list[0])
	}
	if row["venue"] != "uniswap-v4" || row["kind"] != "v4" || row["fee"] != 3000.0 || row["tickSpacing"] != 60.0 || row["hooks"] != zeroAddr {
		t.Errorf("pool = %v", row)
	}
	if _, ok := row["block"]; ok {
		t.Errorf("pool must not expose block: %v", row)
	}

	code, list = getList(t, fakeStore{}, path)
	if code != 200 || len(list) != 0 {
		t.Errorf("empty must be an empty array: %d %v", code, list)
	}

	code, body := get(t, fakeStore{err: errors.New("pg down")}, path)
	if code != 503 || body["error"] != "elysium pools unavailable" {
		t.Errorf("store error: %d %v", code, body)
	}
}
