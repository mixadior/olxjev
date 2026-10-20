// Package request is the HTTP client olxurl reaches OLX's API with.
package request

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "time"
)

// Request fetches a URL's body. olxurl.Build calls OLX's breadcrumbs API
// through it; tests answer it without network.
type Request interface {
    Get(ctx context.Context, url string) ([]byte, error)
}

// maxBody caps a response body: a breadcrumbs answer is a few KB.
const maxBody = 1 << 20

// Local is a Request over net/http that retries transient failures.
type Local struct {
    Client  *http.Client
    Retries int           // extra attempts after a transport error, a 429 or a 5xx
    Backoff time.Duration // the wait before the first retry; doubles after each one
}

// NewLocalRequest is a Local with a 15 s timeout per attempt and 3 retries.
func NewLocalRequest() *Local {
    return &Local{Client: &http.Client{Timeout: 15 * time.Second}, Retries: 3, Backoff: 200 * time.Millisecond}
}

// StatusError is a non-2xx answer.
type StatusError struct {
    URL        string
    StatusCode int
}

func (e *StatusError) Error() string {
    return fmt.Sprintf("GET %s: %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
}

// Get returns url's body. A 2xx answer is a success, anything else an
// error (a *StatusError for an HTTP status); transport errors, 429 and 5xx
// are retried until ctx is done.
func (r *Local) Get(ctx context.Context, url string) ([]byte, error) {
    wait := r.Backoff
    for attempt := 0; ; attempt++ {
        body, retry, err := r.get(ctx, url)
        if err == nil || !retry || attempt >= r.Retries {
            return body, err
        }
        select {
        case <-ctx.Done():
            return nil, fmt.Errorf("%w (after %v)", ctx.Err(), err)
        case <-time.After(wait):
        }
        wait *= 2
    }
}

// get makes one attempt and says whether a failure is worth retrying.
func (r *Local) get(ctx context.Context, url string) ([]byte, bool, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return nil, false, err
    }
    req.Header.Set("Accept", "application/json")
    client := r.Client
    if client == nil {
        client = http.DefaultClient
    }
    resp, err := client.Do(req)
    if err != nil {
        return nil, ctx.Err() == nil, err
    }
    defer resp.Body.Close()
    body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
    if err != nil {
        return nil, ctx.Err() == nil, fmt.Errorf("GET %s: %w", url, err)
    }
    if resp.StatusCode < 200 || resp.StatusCode > 299 {
        retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
        return nil, retry, &StatusError{URL: url, StatusCode: resp.StatusCode}
    }
    return body, false, nil
}
