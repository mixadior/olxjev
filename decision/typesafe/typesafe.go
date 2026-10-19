// Package typesafe is the decision.Decider for TypeSafe's Jev
// (docs.typesafe.ai), the default olxjev backend.
package typesafe

import (
    "strings"

    "github.com/mattn/go-jev"

    "github.com/mixadior/olxjev/decision"
    "github.com/mixadior/olxjev/decision/internal/systemone"
)

const (
    Name         = "typesafe"
    DefaultURL   = jev.DefaultURL
    DefaultModel = jev.DefaultModel
    // PricePerMInputTokens is Jev's price in USD per million input tokens;
    // output tokens are free (docs.typesafe.ai/models: Jev 1.13 and 1.14,
    // $0.042/Mtok, as of 2026-10-15).
    PricePerMInputTokens = 0.042

    // OpenCodeURL is OpenCode Zen's System One endpoint: the same protocol,
    // jev-1.13 (paid, as TypeSafe) and jev-1.13-free ("free for a limited
    // time"; no key needed, no published rate limits, no data policy stated
    // for it — opencode.ai/docs/zen, 2026-10-18).
    OpenCodeURL   = "https://opencode.ai/inference/systemone/v1/systemone"
    OpenCodeModel = "jev-1.13-free"
)

// New returns a Jev Decider; opts (URL, model, timeout, retries) override
// the TypeSafe defaults. An empty apiKey sends no Authorization header,
// which suits open servers such as `tensai serve`.
func New(apiKey string, opts ...jev.ClientOption) decision.Decider {
    opts = append([]jev.ClientOption{jev.WithAPIKey(apiKey)}, opts...)
    return systemone.New(Name, Price, jev.NewClient(opts...), nil)
}

// Price is model's price in USD per million input tokens: OpenCode's
// "-free" models cost nothing, every other Jev PricePerMInputTokens.
func Price(model string) float64 {
    if strings.HasSuffix(model, "-free") {
        return 0
    }
    return PricePerMInputTokens
}
