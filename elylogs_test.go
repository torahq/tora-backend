package main

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func wInt(n int64) string {
	if n < 0 {
		v := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(n))
		return fmt.Sprintf("0x%064x", v)
	}
	return fmt.Sprintf("0x%064x", n)
}

func tAddr(addr string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(addr, "0x")
}

func catWords(words ...string) string {
	var b strings.Builder
	b.WriteString("0x")
	for _, w := range words {
		b.WriteString(strings.TrimPrefix(w, "0x"))
	}
	return b.String()
}

func abiString(s string) string {
	body := hex.EncodeToString([]byte(s))
	return catWords(wInt(32), wInt(int64(len(s))), "0x"+body+strings.Repeat("0", (64-len(body)%64)%64))
}

func bytes32String(s string) string {
	body := hex.EncodeToString([]byte(s))
	return "0x" + body + strings.Repeat("0", 64-len(body))
}

func TestDecodePool(t *testing.T) {
	const (
		usdc    = "0x7d29d8047b905000459c0e80c34a26ceedcb47b2"
		dexWeth = "0xff5681eb95bbacfbb334f80fcdbc31fb59f22332"
		pair    = "0xfb3a6f0b6ec7837eaf298efe0e5dc534bf1d746a"
		k0      = "0x1111111111111111111111111111111111111111"
		k1      = "0x2222222222222222222222222222222222222222"
		pool60  = "0x3333333333333333333333333333333333333333"
		poolNeg = "0x4444444444444444444444444444444444444444"
		hook    = "0xda605da70bff4b32a9b379c614a6d9e4a09baaec"
		id      = "0xabababababababababababababababababababababababababababababababab"
	)
	dex := elyVenue{ID: "dex-da-costa", Kind: "v2", Emitter: "0xfa456ad175f0bdb9285960720b4b6e8f25deb3f9"}
	kalos := elyVenue{ID: "kalos", Kind: "v3", Emitter: "0x520bf060f58cee054f725877279328f5f73a7893"}
	signal := elyVenue{ID: "signal", Kind: "v4", Emitter: "0x889a96eedb1df35a012d7e48baf7f9c780f1243b"}

	v2 := rpcLog{
		Address:     dex.Emitter,
		Topics:      []string{topicPairCreated, tAddr(usdc), tAddr(dexWeth)},
		Data:        catWords(tAddr(pair), wInt(0)),
		BlockNumber: "0x4c1f7",
	}
	v3 := rpcLog{
		Address:     kalos.Emitter,
		Topics:      []string{topicPoolCreated, tAddr(k0), tAddr(k1), wInt(3000)},
		Data:        catWords(wInt(60), tAddr(pool60)),
		BlockNumber: "0x8",
	}
	v3Neg := rpcLog{
		Address:     kalos.Emitter,
		Topics:      []string{topicPoolCreated, tAddr(k0), tAddr(k1), wInt(500)},
		Data:        catWords(wInt(-60), tAddr(poolNeg)),
		BlockNumber: "0x9",
	}
	v4 := rpcLog{
		Address:     signal.Emitter,
		Topics:      []string{topicInitialize, id, tAddr(k0), tAddr(k1)},
		Data:        catWords(wInt(0), wInt(200), tAddr(hook), "0x"+strings.Repeat("0", 55)+"100000000", wInt(-60)),
		BlockNumber: "0xa",
	}
	v4Native := rpcLog{
		Address:     signal.Emitter,
		Topics:      []string{topicInitialize, id, tAddr(zeroAddr), tAddr(k1)},
		Data:        catWords(wInt(3000), wInt(60), tAddr(hook), "0x"+strings.Repeat("0", 55)+"100000000", wInt(0)),
		BlockNumber: "0xb",
	}

	cases := []struct {
		name  string
		venue elyVenue
		log   rpcLog
		want  elyPool
		ok    bool
	}{
		{
			name:  "v2 pair created",
			venue: dex,
			log:   v2,
			want: elyPool{
				Venue: "dex-da-costa", Kind: "v2", Pool: pair, Token0: usdc, Token1: dexWeth,
				Fee: 3000, Hooks: zeroAddr, Block: 311799,
			},
			ok: true,
		},
		{
			name:  "v3 pool created spacing 60",
			venue: kalos,
			log:   v3,
			want: elyPool{
				Venue: "kalos", Kind: "v3", Pool: pool60, Token0: k0, Token1: k1,
				Fee: 3000, TickSpacing: 60, Hooks: zeroAddr, Block: 8,
			},
			ok: true,
		},
		{
			name:  "v3 pool created negative spacing",
			venue: kalos,
			log:   v3Neg,
			want: elyPool{
				Venue: "kalos", Kind: "v3", Pool: poolNeg, Token0: k0, Token1: k1,
				Fee: 500, TickSpacing: -60, Hooks: zeroAddr, Block: 9,
			},
			ok: true,
		},
		{
			name:  "v4 initialize with hook and zero fee",
			venue: signal,
			log:   v4,
			want: elyPool{
				Venue: "signal", Kind: "v4", Pool: id, Token0: k0, Token1: k1,
				Fee: 0, TickSpacing: 200, Hooks: hook, Block: 10,
			},
			ok: true,
		},
		{
			name:  "v4 initialize with native currency0",
			venue: signal,
			log:   v4Native,
			want: elyPool{
				Venue: "signal", Kind: "v4", Pool: id, Token0: zeroAddr, Token1: k1,
				Fee: 3000, TickSpacing: 60, Hooks: hook, Block: 11,
			},
			ok: true,
		},
		{
			name:  "two topics rejected",
			venue: dex,
			log:   rpcLog{Topics: []string{topicPairCreated, tAddr(usdc)}, Data: v2.Data, BlockNumber: "0x1"},
		},
		{
			name:  "unknown topic rejected",
			venue: dex,
			log:   rpcLog{Topics: []string{"0x" + strings.Repeat("11", 32), tAddr(usdc), tAddr(dexWeth)}, Data: v2.Data, BlockNumber: "0x1"},
		},
		{
			name:  "short data rejected",
			venue: kalos,
			log:   rpcLog{Topics: v3.Topics, Data: "0x1234", BlockNumber: "0x1"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := decodePool(c.venue, c.log)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (pool %+v)", ok, c.ok, got)
			}
			if !c.ok {
				return
			}
			if got != c.want {
				t.Errorf("pool = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestDecodeString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"abi string", abiString("KALOS"), "KALOS"},
		{"bytes32", bytes32String("MKR"), "MKR"},
		{"empty", "0x", ""},
		{"short", "0xdeadbeef", ""},
		{"garbage", "0x" + strings.Repeat("ff", 32), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeString(c.in); got != c.want {
				t.Errorf("decodeString(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestWordAndToInt(t *testing.T) {
	data := catWords(wInt(60), tAddr("0x3333333333333333333333333333333333333333"))
	if got := word(data, 1); got != tAddr("0x3333333333333333333333333333333333333333") {
		t.Errorf("word 1 = %q", got)
	}
	if got := word(data, 2); got != "" {
		t.Errorf("short word = %q, want empty", got)
	}
	if got := toInt(wInt(-60), true); got != -60 {
		t.Errorf("signed -60 = %d", got)
	}
	if got := toInt(wInt(-60), false); got != 0 {
		t.Errorf("unsigned -60 = %d, want 0 (out of int64 range)", got)
	}
	if got := addrOf("0x1234"); got != "" {
		t.Errorf("short address = %q, want empty", got)
	}
}
