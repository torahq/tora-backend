package main

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
)

// rpcLog is one eth_getLogs entry.
type rpcLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
}

// elyVenue is one v1 Elysium venue: its factory or V4 PoolManager.
type elyVenue struct {
	ID      string
	Kind    string
	Emitter string
}

// elyPool is one pool of a v1 venue. Block is its creation block.
type elyPool struct {
	Venue       string `json:"venue"`
	Kind        string `json:"kind"`
	Pool        string `json:"pool"`
	Token0      string `json:"token0"`
	Token1      string `json:"token1"`
	Fee         int    `json:"fee"`
	TickSpacing int    `json:"tickSpacing"`
	Hooks       string `json:"hooks"`
	Block       int64  `json:"-"`
}

// elyToken is the metadata the app shows for a token.
type elyToken struct {
	Address  string `json:"address"`
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Decimals int    `json:"decimals"`
}

// elyTokenRow is a token plus how many indexed pools hold it.
type elyTokenRow struct {
	elyToken
	Pools int `json:"pools"`
}

const (
	topicPairCreated = "0x0d3648bd0f6ba80134a33ba9275ac585d9d315f0ad8355cddefde31afa28d0e9"
	topicPoolCreated = "0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118"
	topicInitialize  = "0xdd466e674ea557f56295e2d0218a125ea4b4f0f6f3307b95f85e6110838d6438"

	zeroAddr = "0x0000000000000000000000000000000000000000"
)

// elysiumVenues are the v1 venues. IDs and addresses are shared with the TypeScript side.
var elysiumVenues = []elyVenue{
	{ID: "dex-da-costa", Kind: "v2", Emitter: "0xfa456ad175f0bdb9285960720b4b6e8f25deb3f9"},
	{ID: "kalos", Kind: "v3", Emitter: "0x520bf060f58cee054f725877279328f5f73a7893"},
	{ID: "v3-f172", Kind: "v3", Emitter: "0xf172c253610b41147fb655067bfcb9415087b9ac"},
	{ID: "v3-cae8", Kind: "v3", Emitter: "0xcae8b61e7d6eeced86d45eb8d044027663e529fb"},
	{ID: "perpme-v3", Kind: "v3", Emitter: "0x1186defcaf121da6e773425fec0814608798e792"},
	{ID: "signal", Kind: "v4", Emitter: "0x889a96eedb1df35a012d7e48baf7f9c780f1243b"},
	{ID: "uniswap-v4", Kind: "v4", Emitter: "0xee1ec2963482379916fc97cca6bf553c982dede9"},
}

// topicFor is the creation event a venue kind emits.
func topicFor(kind string) string {
	switch kind {
	case "v2":
		return topicPairCreated
	case "v3":
		return topicPoolCreated
	case "v4":
		return topicInitialize
	}
	return ""
}

// decodePool turns one creation log into a pool. Unknown topics, a wrong topic count
// and short data are rejected.
func decodePool(v elyVenue, l rpcLog) (elyPool, bool) {
	if len(l.Topics) == 0 {
		return elyPool{}, false
	}
	topic := strings.ToLower(l.Topics[0])
	p := elyPool{Venue: v.ID, Kind: v.Kind}
	switch {
	case v.Kind == "v2" && topic == topicPairCreated:
		if len(l.Topics) != 3 {
			return elyPool{}, false
		}
		p.Token0, p.Token1 = addrOf(l.Topics[1]), addrOf(l.Topics[2])
		p.Pool = addrOf(word(l.Data, 0))
		p.Fee = 3000
		p.Hooks = zeroAddr
	case v.Kind == "v3" && topic == topicPoolCreated:
		if len(l.Topics) != 4 {
			return elyPool{}, false
		}
		p.Token0, p.Token1 = addrOf(l.Topics[1]), addrOf(l.Topics[2])
		p.Fee = int(toInt(l.Topics[3], false))
		p.TickSpacing = int(toInt(word(l.Data, 0), true))
		p.Pool = addrOf(word(l.Data, 1))
		p.Hooks = zeroAddr
	case v.Kind == "v4" && topic == topicInitialize:
		if len(l.Topics) != 4 || len(hexBody(l.Topics[1])) != 64 {
			return elyPool{}, false
		}
		p.Pool = strings.ToLower(l.Topics[1])
		p.Token0, p.Token1 = addrOf(l.Topics[2]), addrOf(l.Topics[3])
		p.Fee = int(toInt(word(l.Data, 0), false))
		p.TickSpacing = int(toInt(word(l.Data, 1), true))
		p.Hooks = addrOf(word(l.Data, 2))
	default:
		return elyPool{}, false
	}
	if p.Pool == "" || p.Token0 == "" || p.Token1 == "" || p.Hooks == "" {
		return elyPool{}, false
	}
	block, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(l.BlockNumber), "0x"), 16, 64)
	if err != nil {
		return elyPool{}, false
	}
	p.Block = block
	return p, true
}

// word returns ABI word i of hex data as "0x" plus 64 lowercase hex digits, or "" when data is short.
func word(data string, i int) string {
	s := hexBody(data)
	if i < 0 || len(s) < (i+1)*64 {
		return ""
	}
	return "0x" + s[i*64:(i+1)*64]
}

// toInt reads a hex word as an integer. Signed words are two's complement.
func toInt(w string, signed bool) int64 {
	s := hexBody(w)
	if s == "" {
		return 0
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 16); !ok {
		return 0
	}
	if signed && n.Bit(255) == 1 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	if !n.IsInt64() {
		return 0
	}
	return n.Int64()
}

// addrOf takes the low 20 bytes of a word or topic.
func addrOf(w string) string {
	s := hexBody(w)
	if len(s) < 40 {
		return ""
	}
	return "0x" + s[len(s)-40:]
}

// hexBody lowercases and drops the 0x prefix. Non-hex input yields "".
func hexBody(h string) string {
	s := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(h), "0x"))
	if len(s)%2 != 0 {
		return ""
	}
	if _, err := hex.DecodeString(s); err != nil {
		return ""
	}
	return s
}

// decodeString reads symbol() and name() results: an ABI-encoded string, or a bytes32 string.
// Anything unprintable is garbage and yields "".
func decodeString(h string) string {
	s := hexBody(h)
	if s == "" {
		return ""
	}
	b, _ := hex.DecodeString(s)
	// An ABI string starts with the offset word 32, then the length and the bytes.
	if len(b) >= 64 {
		if off := new(big.Int).SetBytes(b[:32]); off.IsInt64() && off.Int64() == 32 {
			size := new(big.Int).SetBytes(b[32:64])
			if !size.IsInt64() || size.Int64() <= 0 || int64(len(b)) < 64+size.Int64() {
				return ""
			}
			return text(b[64 : 64+size.Int64()])
		}
	}
	if len(b) < 32 {
		return ""
	}
	return text(b[:32])
}

// text trims trailing zero bytes and rejects non-printable bytes.
func text(b []byte) string {
	b = bytes.TrimRight(b, "\x00")
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return ""
		}
	}
	return string(b)
}
