package request

import (
    "context"
    "errors"
    "net/http"
    "net/http/httptest"
    "sync/atomic"
    "testing"
    "time"
)

// fastLocal is a Local that doesn't wait between retries.
func fastLocal() *Local {
    l := NewLocalRequest()
    l.Backoff = time.Millisecond
    return l
}

// serve answers with statuses in turn (the last one repeats) and counts calls.
func serve(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
    t.Helper()
    var calls atomic.Int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        n := int(calls.Add(1)) - 1
        w.WriteHeader(statuses[min(n, len(statuses)-1)])
        w.Write([]byte(`{"ok":true}`))
    }))
    t.Cleanup(srv.Close)
    return srv, &calls
}

func TestGetRetriesTransientFailures(t *testing.T) {
    srv, calls := serve(t, http.StatusBadGateway, http.StatusTooManyRequests, http.StatusOK)
    body, err := fastLocal().Get(context.Background(), srv.URL)
    if err != nil || string(body) != `{"ok":true}` || calls.Load() != 3 {
        t.Fatalf("Get = %q, %v after %d calls; want the body after 3", body, err, calls.Load())
    }
}

func TestGetGivesUpAfterRetries(t *testing.T) {
    srv, calls := serve(t, http.StatusServiceUnavailable)
    _, err := fastLocal().Get(context.Background(), srv.URL)
    var se *StatusError
    if !errors.As(err, &se) || se.StatusCode != http.StatusServiceUnavailable || calls.Load() != 4 {
        t.Fatalf("Get err = %v after %d calls; want a 503 StatusError after 1 + 3 retries", err, calls.Load())
    }
}

// A client error is final: retrying a 404 can't help.
func TestGetDoesNotRetryClientErrors(t *testing.T) {
    srv, calls := serve(t, http.StatusNotFound)
    if _, err := fastLocal().Get(context.Background(), srv.URL); err == nil || calls.Load() != 1 {
        t.Fatalf("Get err = %v after %d calls; want an error after 1", err, calls.Load())
    }
}

func TestGetStopsWhenContextIsDone(t *testing.T) {
    srv, calls := serve(t, http.StatusInternalServerError)
    l := NewLocalRequest()
    l.Backoff = time.Hour
    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()
    if _, err := l.Get(ctx, srv.URL); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
        t.Fatalf("Get err = %v after %d calls; want the deadline after 1", err, calls.Load())
    }
}

// TLS certificates are verified: a self-signed server is refused.
func TestGetVerifiesCertificates(t *testing.T) {
    srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
    defer srv.Close()
    l := fastLocal()
    l.Retries = 0
    if _, err := l.Get(context.Background(), srv.URL); err == nil {
        t.Fatal("Get accepted a self-signed certificate")
    }
}
