package analytics

import (
	"slices"
)

// Price is a price in USD per million tokens of uncached input, cached input and output.
type Price struct {
	Input  float64 `json:"input"`
	Cached float64 `json:"cached"`
	Output float64 `json:"output"`
}

// AssumedPrice is the one price every Codex model is priced at. It is an estimate, not an
// invoice: in fact Codex and Claude Code are paid for by subscriptions.
var AssumedPrice = Price{Input: 1.25, Cached: 0.125, Output: 10} //nolint:gochecknoglobals // a constant struct

// The bases of a session's cost.
const (
	// CostBasisOTel: the cost is Claude Code's own estimate from its api_request events.
	CostBasisOTel = "otel_reported"
	// CostBasisEstimate: the cost is the tokens at AssumedPrice (Codex) or at the model's API
	// price (Claude Code without native OpenTelemetry, from its transcript).
	CostBasisEstimate = "api_price_estimate"
)

// pricingNote explains the cost of the dataset.
const pricingNote = "Claude Code: стоимость — собственная оценка Claude Code (cost_usd в событиях api_request), не счёт; " +
	"без нативного OTel — оценка по цене API модели из транскрипта (неизвестная модель — null). " +
	"Codex: токены по сессиям в OTel не связаны. Где их дал журнал сессии (токены за ход) или codex.sse_event " +
	"с conversation.id — оценка по условной цене API, одинаковой для всех моделей Codex; иначе стоимость null. " +
	"Фактически — подписки."

// UsageCost is the cost of tokens at AssumedPrice; cached is a part of input.
func UsageCost(input, cached, output int64) float64 {
	uncached := max(0, input-cached)
	return (float64(uncached)*AssumedPrice.Input + float64(cached)*AssumedPrice.Cached +
		float64(output)*AssumedPrice.Output) / 1e6
}

// usageCostOf is UsageCost of a request's tokens, for its Cost.
func usageCostOf(input, cached, output int64) *float64 {
	c := UsageCost(input, cached, output)
	return &c
}

// SessionCost is the cost of a session's requests rounded to four decimals; nil when there is
// no request or one of them has no cost.
func SessionCost(api []APIRecord) *float64 {
	if len(api) == 0 {
		return nil
	}
	var sum float64
	for _, a := range api {
		if a.Cost == nil {
			return nil
		}
		sum += *a.Cost
	}
	sum = roundTo(sum, 4)
	return &sum
}

// CostBasis is what a session's cost rests on: CostBasisOTel with a Claude request,
// CostBasisEstimate otherwise; "" when the cost is unknown.
func CostBasis(api []APIRecord, cost *float64) string {
	if cost == nil {
		return ""
	}
	if slices.ContainsFunc(api, func(a APIRecord) bool { return a.Src == SrcClaude }) {
		return CostBasisOTel
	}
	return CostBasisEstimate
}

// Pricing is the pricing block of the dataset.
type Pricing struct {
	Basis   string           `json:"basis"`
	Note    string           `json:"note"`
	Assumed Price            `json:"assumed"`
	Models  map[string]Price `json:"models"`
}

// BuildPricing lists the Codex models of the sessions' requests, each at AssumedPrice.
func BuildPricing(sessions ...[]APIRecord) Pricing {
	p := Pricing{Basis: CostBasisOTel, Note: pricingNote, Assumed: AssumedPrice, Models: map[string]Price{}}
	for _, api := range sessions {
		for _, a := range api {
			if (a.Src == SrcCodexSSE || a.Src == SrcCodexRollout) && a.Model != "" {
				p.Models[a.Model] = AssumedPrice
			}
		}
	}
	return p
}
