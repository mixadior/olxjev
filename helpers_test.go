package olxjev

import (
    "sync"
    "testing"

    botform "github.com/mixadior/bfc"

    "github.com/mixadior/olxjev/olxform"
)

var (
    composerOnce sync.Once
    composer     *botform.BotFormComposer
    composerErr  error
)

// testComposer loads the real OLX form once per test binary (~1 s).
func testComposer(t *testing.T) *botform.BotFormComposer {
    t.Helper()
    composerOnce.Do(func() { composer, composerErr = olxform.NewComposer() })
    if composerErr != nil {
        t.Fatalf("olxform.NewComposer: %v", composerErr)
    }
    return composer
}
