package cost

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// litellmPrices is the pinned extract of LiteLLM's published catalog: the
// Claude rows, the commit they came from, and nothing else.
//
// Embedded, never fetched. scripts/refresh-prices.sh is the only thing in the
// repository that touches the network, and it is run deliberately, not by the
// binary. A price fetched while a report is rendered would make the report of
// an unchanged session depend on the day it was rendered.
//
//go:embed litellm-prices.json
var litellmPrices []byte

// catalogRates are one model's published rates, in USD per token.
//
// CacheWrite1h is a pointer because upstream omits it on a couple of legacy
// aliases, and an omitted rate is not a free one.
type catalogRates struct {
	Input        float64  `json:"input"`
	CacheRead    float64  `json:"cache_read"`
	CacheWrite5m float64  `json:"cache_write_5m"`
	CacheWrite1h *float64 `json:"cache_write_1h"`
	Output       float64  `json:"output"`
}

type catalogUpstream struct {
	Repo      string `json:"repo"`
	File      string `json:"file"`
	Commit    string `json:"commit"`
	Retrieved string `json:"retrieved"`
}

type catalogFile struct {
	Upstream  catalogUpstream         `json:"upstream"`
	PriceUnit string                  `json:"price_unit"`
	Models    map[string]catalogRates `json:"models"`
}

// catalog parses the embedded extract once.
//
// A parse failure panics rather than degrading to "no prices known". The file
// is compiled in and a test reads every row of it, so a failure here is a
// broken build, and a price table that quietly empties itself is the silent
// fall-through this package exists to refuse.
var catalog = sync.OnceValue(func() catalogFile {
	var c catalogFile
	if err := json.Unmarshal(litellmPrices, &c); err != nil {
		panic(fmt.Sprintf("cost: embedded price catalog is unparseable: %v", err))
	}
	return c
})

// InputPrice returns a model's published input price in USD per token.
//
// The bool is the whole point. A cost-weighted token is defined as a multiple
// of a model's own input price, so this one number converts EIT to money --
// and converting with the wrong number produces a dollar figure that looks
// exactly as authoritative as a right one. An unknown model therefore has no
// price rather than a default price. Weights.For does fall through to a
// default, which is defensible for a ratio and is not for a price: a ratio
// that is wrong by a generation over-states a class, while a price that is
// wrong by a model mis-states the bill.
//
// The match is exact, on the catalog's own keys, with only case normalised.
// It is deliberately not clever about provider prefixes: bedrock,
// databricks and the rest resell these models at their own rates, so
// "eu.anthropic.claude-opus-5" is not a spelling of "claude-opus-5" and
// pricing it as one would invent a number.
func InputPrice(modelID string) (float64, bool) {
	r, ok := catalog().Models[strings.ToLower(strings.TrimSpace(modelID))]
	if !ok || r.Input <= 0 {
		return 0, false
	}
	return r.Input, true
}

// CatalogPin names the exact published catalog these prices came from.
//
// Any report that prints dollars prints this beside them. A dollar figure
// whose source is not stated is the kind of number this tool exists not to
// produce, and "from LiteLLM" is not a source: the rates move, so the answer
// has to name the commit.
func CatalogPin() string {
	u := catalog().Upstream
	commit := u.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("%s@%s, retrieved %s", u.Repo, commit, u.Retrieved)
}
