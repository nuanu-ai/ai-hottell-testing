package analytics

import (
	"regexp"
)

// claudePrice is a Claude model's API price in USD per million tokens of uncached input, cache
// read and output; a cache write is claudeCacheWrite times the input price.
type claudePrice struct {
	Input, CacheRead, Output float64
}

// claudeCacheWrite and claudeCacheWrite1h are the prices of a cache write as a multiple of the
// input price: the 5-minute cache and the 1-hour one. A cache write the transcript does not split
// by TTL is priced as 5-minute, the cache Claude Code writes by default (HT-517).
const (
	claudeCacheWrite   = 1.25
	claudeCacheWrite1h = 2.0
)

// claudePrices are the first-party API prices of the Claude models by id, as published on
// 2026-09-25. A model missing here has no estimate. The estimate is not an invoice: Claude Code
// is in fact paid for by a subscription.
var claudePrices = map[string]claudePrice{ //nolint:gochecknoglobals // a constant table
	"claude-fable-5-1":  {Input: 10, CacheRead: 0.25, Output: 50},
	"claude-mythos-5-1": {Input: 10, CacheRead: 0.25, Output: 50},
	"claude-fable-5":    {Input: 10, CacheRead: 1, Output: 50},
	"claude-mythos-5":   {Input: 10, CacheRead: 1, Output: 50},
	"claude-opus-5-5":   {Input: 4, CacheRead: 0.20, Output: 20},
	"claude-opus-5":     {Input: 5, CacheRead: 0.50, Output: 25},
	"claude-opus-4-8":   {Input: 5, CacheRead: 0.50, Output: 25},
	"claude-opus-4-7":   {Input: 5, CacheRead: 0.50, Output: 25},
	"claude-opus-4-6":   {Input: 5, CacheRead: 0.50, Output: 25},
	"claude-sonnet-5-5": {Input: 2, CacheRead: 0.20, Output: 10},
	"claude-sonnet-5":   {Input: 2, CacheRead: 0.20, Output: 10},
	"claude-sonnet-4-6": {Input: 3, CacheRead: 0.30, Output: 15},
	"claude-haiku-4-5":  {Input: 1, CacheRead: 0.10, Output: 5},
}

// datedModel is the date suffix of a dated model id, as claude-haiku-4-5-20251001.
var datedModel = regexp.MustCompile(`-\d{8}$`)

// claudeTokens are the token counts of a Claude request. Input is all the input, CacheRead and
// CacheCreation included; CacheCreation1h is the part of CacheCreation written to the 1-hour
// cache, the rest being 5-minute.
type claudeTokens struct {
	Input, CacheRead, CacheCreation, CacheCreation1h, Output int64
}

// claudeTokensCost is the estimated cost in USD of a Claude request's tokens at its model's
// price. A request without tokens costs 0 whatever its model, as Claude Code's synthetic lines;
// nil for a model without a price.
func claudeTokensCost(model string, t claudeTokens) *float64 {
	if t.Input == 0 && t.Output == 0 {
		zero := 0.0
		return &zero
	}
	p, ok := claudePrices[datedModel.ReplaceAllString(model, "")]
	if !ok {
		return nil
	}
	uncached := max(0, t.Input-t.CacheRead-t.CacheCreation)
	oneHour := min(max(0, t.CacheCreation1h), t.CacheCreation)
	fiveMin := t.CacheCreation - oneHour
	c := (float64(uncached)*p.Input + float64(t.CacheRead)*p.CacheRead +
		float64(fiveMin)*p.Input*claudeCacheWrite + float64(oneHour)*p.Input*claudeCacheWrite1h +
		float64(t.Output)*p.Output) / 1e6
	return &c
}
