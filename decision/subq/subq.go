// Package subq is the decision.Decider for SubQ's Decisions API
// (docs.subq.ai/endpoints/decisions-api): the System One protocol with at
// most 16 questions per request, so larger question sets are batched.
package subq

import (
    "encoding/json"
    "errors"
    "fmt"
    "net/http"

    "github.com/mattn/go-jev"

    "github.com/mixadior/olxjev/decision"
    "github.com/mixadior/olxjev/decision/internal/systemone"
)

const (
    Name         = "subq"
    DefaultURL   = "https://api.subq.ai/v1/systemone"
    DefaultModel = "subq-snap-preview"
    // MaxQuestions is the Decisions API's per-request limit (1–16).
    MaxQuestions = 16
    // PricePerMInputTokens is SubQ Snap's price in USD per million input
    // tokens; output tokens are free (docs.subq.ai/using-the-api/models,
    // $0.05/Mtok, as of 2026-10-17).
    PricePerMInputTokens = 0.05
)

// New returns a SubQ Decider; opts (URL, model, timeout, retries) override
// the SubQ defaults.
func New(apiKey string, opts ...jev.ClientOption) decision.Decider {
    opts = append([]jev.ClientOption{jev.WithAPIKey(apiKey), jev.WithURL(DefaultURL), jev.WithModel(DefaultModel)}, opts...)
    price := func(string) float64 { return PricePerMInputTokens }
    return decision.Batched(systemone.New(Name, price, jev.NewClient(opts...), apiError), MaxQuestions)
}

// Error is a SubQ error response: {"detail": {"error_type", "message"}}.
type Error struct {
    Status  int
    Type    string
    Message string
    err     error
}

func (e *Error) Error() string {
    msg := fmt.Sprintf("subq: HTTP %d %s: %s", e.Status, e.Type, e.Message)
    if e.Status == http.StatusPaymentRequired {
        msg += " (insufficient credits)"
    }
    return msg
}

func (e *Error) Unwrap() error { return e.err }

// apiError turns a SubQ error body into an *Error; other errors get the
// "subq: " prefix.
func apiError(err error) error {
    var api *jev.APIError
    if !errors.As(err, &api) {
        return fmt.Errorf("subq: %w", err)
    }
    var body struct {
        Detail struct {
            ErrorType string `json:"error_type"`
            Message   string `json:"message"`
        } `json:"detail"`
    }
    if json.Unmarshal([]byte(api.Body), &body) != nil || body.Detail.Message == "" {
        return fmt.Errorf("subq: %w", err)
    }
    return &Error{Status: api.Status, Type: body.Detail.ErrorType, Message: body.Detail.Message, err: err}
}
