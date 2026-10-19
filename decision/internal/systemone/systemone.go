// Package systemone talks the System One protocol that TypeSafe (Jev) and
// SubQ (Snap) share — POST {model, state, questions} → {answers, usage} —
// over go-jev's HTTP transport (timeouts, retries on 429/529).
package systemone

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/mattn/go-jev"

    "github.com/mixadior/olxjev/decision"
)

// Client is a decision.Decider for one System One provider.
type Client struct {
    name    string
    price   func(model string) float64 // USD per million input tokens of the model that answered
    jev     *jev.Client
    wrapErr func(error) error // provider-specific error text; nil keeps errors as they are
}

// New returns a Decider named name that posts through client and prices
// input tokens at price(model) USD per million, model being the one the
// response names (else the client's).
func New(name string, price func(model string) float64, client *jev.Client, wrapErr func(error) error) *Client {
    return &Client{name: name, price: price, jev: client, wrapErr: wrapErr}
}

func (c *Client) Name() string  { return c.name }
func (c *Client) Model() string { return c.jev.Model() }

func (c *Client) Decide(ctx context.Context, state any, questions map[string]decision.Question) (*decision.Decision, error) {
    resp, err := c.jev.Evaluate(ctx, state, questions)
    if err != nil {
        if c.wrapErr != nil {
            err = c.wrapErr(err)
        }
        return nil, err
    }
    var body struct {
        Answers map[string]*decision.Answer `json:"answers"`
    }
    if err := json.Unmarshal(resp.Raw, &body); err != nil {
        return nil, fmt.Errorf("%s: unexpected response: %w", c.name, err)
    }
    if body.Answers == nil {
        // merged into a batch, a missing answers object would leave every
        // question silently unanswered
        return nil, fmt.Errorf("%s: response has no answers", c.name)
    }
    model := resp.Model
    if model == "" {
        model = c.Model()
    }
    return &decision.Decision{
        Answers: body.Answers,
        Usage: decision.Usage{
            Calls:        1,
            InputTokens:  resp.Usage.InputTokens,
            OutputTokens: resp.Usage.OutputTokens,
            CostUSD:      float64(resp.Usage.InputTokens) * c.price(model) / 1e6,
            Model:        model,
        },
        Raw: []json.RawMessage{resp.Raw},
    }, nil
}
