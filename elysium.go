package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ely reads Elysium's JSON-RPC endpoint.
type ely struct {
	URL    string
	Client *http.Client
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	elyWindow     = 50_000 // blocks per eth_getLogs call
	elyMinWindow  = 1_000  // smallest window a halving retry tries
	elyMaxWindows = 40     // windows per venue per poll, so a run stays short
	elySleep      = 100 * time.Millisecond
)

func hexQ(n int64) string { return fmt.Sprintf("0x%x", n) }

func hexInt(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 64)
}

// Token metadata is batched through Multicall3. A single Signal window holds thousands of new
// tokens, so one eth_call per field would never finish inside a poll's time budget.
const (
	multicall3 = "0xca11bde05977b3631167028862be2a173976ca11"
	mcChunk    = 200 // tokens per aggregate3 call
)

type mcCall struct {
	Target string
	Data   string
}

type mcResult struct {
	OK   bool
	Data string
}

// encodeAggregate3 builds Multicall3.aggregate3((address,bool,bytes)[]) calldata by hand.
func encodeAggregate3(calls []mcCall) string {
	heads := make([]string, len(calls))
	var bodies strings.Builder
	offset := 32 * len(calls)
	for i, c := range calls {
		data := strings.TrimPrefix(strings.ToLower(c.Data), "0x")
		pad := strings.Repeat("0", (64-len(data)%64)%64)
		heads[i] = fmt.Sprintf("%064x", offset)
		offset += 128 + (len(data)+len(pad))/2
		bodies.WriteString(strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(c.Target), "0x"))
		bodies.WriteString(fmt.Sprintf("%064x", 1))    // allowFailure
		bodies.WriteString(fmt.Sprintf("%064x", 0x60)) // bytes offset inside the tuple
		bodies.WriteString(fmt.Sprintf("%064x", len(data)/2))
		bodies.WriteString(data)
		bodies.WriteString(pad)
	}
	var b strings.Builder
	b.WriteString("0x82ad56cb") // aggregate3((address,bool,bytes)[])
	b.WriteString(fmt.Sprintf("%064x", 0x20))
	b.WriteString(fmt.Sprintf("%064x", len(calls)))
	for _, h := range heads {
		b.WriteString(h)
	}
	b.WriteString(bodies.String())
	return b.String()
}

// decodeAggregate3 reads Result[] (bool success, bytes returnData) from aggregate3's return.
func decodeAggregate3(h string) ([]mcResult, error) {
	s := hexBody(h)
	if s == "" {
		return nil, errors.New("empty multicall result")
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) < 64 {
		return nil, errors.New("short multicall result")
	}
	word := func(off int) []byte {
		if off < 0 || off+32 > len(b) {
			return nil
		}
		return b[off : off+32]
	}
	num := func(w []byte) (int, bool) {
		if len(w) != 32 {
			return 0, false
		}
		n := new(big.Int).SetBytes(w)
		if !n.IsInt64() {
			return 0, false
		}
		return int(n.Int64()), true
	}
	arr, ok := num(word(0))
	if !ok || arr+32 > len(b) {
		return nil, errors.New("bad multicall array offset")
	}
	n, ok := num(word(arr))
	if !ok || arr+32+n*32 > len(b) {
		return nil, errors.New("bad multicall count")
	}
	out := make([]mcResult, 0, n)
	for i := 0; i < n; i++ {
		rel, ok := num(word(arr + 32 + i*32))
		if !ok {
			return nil, errors.New("bad multicall element offset")
		}
		t := arr + 32 + rel
		successW := word(t)
		dataRel, ok := num(word(t + 32))
		if successW == nil || !ok {
			return nil, errors.New("bad multicall element")
		}
		dp := t + dataRel
		size, ok := num(word(dp))
		if !ok || dp+32+size > len(b) {
			return nil, errors.New("bad multicall result data")
		}
		out = append(out, mcResult{OK: successW[31] == 1, Data: "0x" + hex.EncodeToString(b[dp+32:dp+32+size])})
	}
	return out, nil
}

func (e ely) multicall(ctx context.Context, calls []mcCall) ([]mcResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	var out string
	if err := e.call(ctx, "eth_call", []any{map[string]any{"to": multicall3, "data": encodeAggregate3(calls)}, "latest"}, &out); err != nil {
		return nil, err
	}
	return decodeAggregate3(out)
}

// errRateLimited marks a 429 from the node; call retries it briefly.
var errRateLimited = errors.New("rate limited")

// call posts one JSON-RPC 2.0 request. A JSON-RPC error becomes a Go error with its message.
func (e ely) call(ctx context.Context, method string, params []any, out any) error {
	for attempt := 0; ; attempt++ {
		err := e.callOnce(ctx, method, params, out)
		if err == nil {
			return nil
		}
		if attempt >= 2 || !errors.Is(err, errRateLimited) {
			return err
		}
		if err := pause(ctx, time.Second); err != nil {
			return err
		}
	}
}

func (e ely) callOnce(ctx context.Context, method string, params []any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("elysium %s: %w: %s", method, errRateLimited, res.Status)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("elysium %s: %s", method, res.Status)
	}
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return err
	}
	if r.Error != nil {
		return fmt.Errorf("elysium %s: %s", method, r.Error.Message)
	}
	if out == nil || len(r.Result) == 0 {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

// deployBlock binary-searches the first block that holds code at addr.
func (e ely) deployBlock(ctx context.Context, addr string, head int64) (int64, error) {
	lo, hi := int64(0), head
	for lo < hi {
		mid := lo + (hi-lo)/2
		var code string
		if err := e.call(ctx, "eth_getCode", []any{addr, hexQ(mid)}, &code); err != nil {
			return 0, err
		}
		if code != "" && code != "0x" {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, nil
}

// logs fetches creation logs in newest possible windows. A window the node calls too wide is halved,
// down to elyMinWindow. It returns the last block covered.
func (e ely) logs(ctx context.Context, emitter, topic string, from, to int64) ([]rpcLog, int64, error) {
	span := to - from
	if span < 0 {
		span = 0
	}
	for {
		var logs []rpcLog
		err := e.call(ctx, "eth_getLogs", []any{map[string]any{
			"address":   []string{emitter},
			"fromBlock": hexQ(from),
			"toBlock":   hexQ(from + span),
			"topics":    [][]string{{topic}},
		}}, &logs)
		if err == nil {
			return logs, from + span, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "exceeds") || span <= elyMinWindow {
			return nil, 0, err
		}
		span /= 2
	}
}

// pollElysium indexes every venue from its cursor, a few windows per run. Pools are upserted before
// the cursor moves, so a crash re-reads at most one window.
func pollElysium(e ely, s Store) func(context.Context) error {
	return func(ctx context.Context) error {
		var headHex string
		if err := e.call(ctx, "eth_blockNumber", nil, &headHex); err != nil {
			return err
		}
		head, err := hexInt(headHex)
		if err != nil {
			return err
		}
		for _, v := range elysiumVenues {
			topic := topicFor(v.Kind)
			if topic == "" {
				continue
			}
			cursor, ok, err := s.ElysiumCursor(ctx, v.ID)
			if err != nil {
				return err
			}
			if !ok {
				b, err := e.deployBlock(ctx, v.Emitter, head)
				if err != nil {
					return err
				}
				cursor = b
				if err := s.SetElysiumCursor(ctx, v.ID, cursor); err != nil {
					return err
				}
			}
			start, windows := cursor, 0
			for windows < elyMaxWindows && cursor <= head {
				end := min(cursor+elyWindow, head)
				logs, last, err := e.logs(ctx, v.Emitter, topic, cursor, end)
				if err != nil {
					return fmt.Errorf("%s logs: %w", v.ID, err)
				}
				pools, addrs := decodePools(v, logs)
				tokens, err := e.fetchTokens(ctx, s, addrs)
				if err != nil {
					return err
				}
				if err := s.UpsertElysium(ctx, pools, tokens); err != nil {
					return err
				}
				cursor = last + 1
				if err := s.SetElysiumCursor(ctx, v.ID, cursor); err != nil {
					return err
				}
				windows++
				if err := pause(ctx, elySleep); err != nil {
					return err
				}
			}
			if windows > 0 {
				slog.Info("elysium indexed", "venue", v.ID, "from", start, "cursor", cursor, "head", head)
			}
		}
		return nil
	}
}

// decodePools decodes creation logs and collects the token addresses they mention, minus native HYPE.
func decodePools(v elyVenue, logs []rpcLog) ([]elyPool, []string) {
	var pools []elyPool
	var addrs []string
	seen := map[string]bool{}
	for _, l := range logs {
		p, ok := decodePool(v, l)
		if !ok {
			continue
		}
		pools = append(pools, p)
		for _, a := range []string{p.Token0, p.Token1} {
			if a == zeroAddr || seen[a] {
				continue
			}
			seen[a] = true
			addrs = append(addrs, a)
		}
	}
	return pools, addrs
}

// fetchTokens reads metadata only for tokens the store does not know yet. The three calls are
// batched through Multicall3: a single Signal window holds thousands of new tokens, and one
// eth_call per field would never finish inside a poll's time budget.
func (e ely) fetchTokens(ctx context.Context, s Store, addrs []string) ([]elyToken, error) {
	if len(addrs) == 0 {
		return nil, nil
	}
	known, err := s.KnownTokens(ctx, addrs)
	if err != nil {
		return nil, err
	}
	var unknown []string
	for _, a := range addrs {
		if a != zeroAddr && !known[a] {
			unknown = append(unknown, a)
		}
	}
	var out []elyToken
	for i := 0; i < len(unknown); i += mcChunk {
		batch := unknown[i:min(i+mcChunk, len(unknown))]
		calls := make([]mcCall, 0, 3*len(batch))
		for _, a := range batch {
			calls = append(calls, mcCall{Target: a, Data: "0x313ce567"}, mcCall{Target: a, Data: "0x95d89b41"}, mcCall{Target: a, Data: "0x06fdde03"})
		}
		res, err := e.multicall(ctx, calls)
		if err != nil {
			return nil, err
		}
		if len(res) != len(calls) {
			return nil, fmt.Errorf("multicall: got %d of %d results", len(res), len(calls))
		}
		for j, a := range batch {
			dec, ok := uintWord(res[3*j].Data)
			if !res[3*j].OK || !ok {
				continue // no decimals(): not an ERC-20 the app can price
			}
			out = append(out, elyToken{
				Address:  a,
				Symbol:   decodeString(res[3*j+1].Data),
				Name:     decodeString(res[3*j+2].Data),
				Decimals: int(dec),
			})
		}
	}
	return out, nil
}

// uintWord reads a uint word such as decimals().
func uintWord(h string) (int64, bool) {
	s := hexBody(h)
	if s == "" {
		return 0, false
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 16); !ok || !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}

// pause waits d or until ctx ends.
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
