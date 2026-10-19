package decision

import (
    "context"
    "maps"
    "slices"

    "golang.org/x/sync/errgroup"
)

// Batched returns a Decider that sends d at most max questions per call
// (SubQ accepts 16). Over max, the question keys are sorted and cut into
// consecutive chunks — the same questions always make the same chunks —
// which run concurrently; the first error cancels the rest and is returned
// with no Decision. The merged Decision has every chunk's answers, the
// summed usage and the chunks' Raw bodies in chunk order. max <= 0 returns d.
// The result is Groupable: WithGroups keeps related questions in one chunk.
func Batched(d Decider, max int) Decider {
    if max <= 0 {
        return d
    }
    return &batched{Decider: d, max: max}
}

// Groupable is a Decider that splits questions into batches and can keep
// related ones together: SubQ's answers shift with the questions that share
// a request (live, 2026-10-18: up to 0.17 between chunks of 16 and of 5).
type Groupable interface {
    Decider
    // WithGroups returns a Decider that packs whole groups (named by
    // group(key)) into each batch, in sorted group order; a group larger
    // than a batch is split on its own.
    WithGroups(group func(key string) string) Decider
}

type batched struct {
    Decider // Name and Model
    max     int
    group   func(key string) string // nil: every key is its own group
}

func (b *batched) WithGroups(group func(key string) string) Decider {
    return &batched{Decider: b.Decider, max: b.max, group: group}
}

// chunks packs the question keys into batches of at most b.max.
func (b *batched) chunks(questions map[string]Question) [][]string {
    byGroup := map[string][]string{}
    for k := range questions {
        g := k
        if b.group != nil {
            g = b.group(k)
        }
        byGroup[g] = append(byGroup[g], k)
    }
    var chunks [][]string
    var cur []string
    for _, g := range slices.Sorted(maps.Keys(byGroup)) {
        keys := slices.Sorted(slices.Values(byGroup[g]))
        if len(keys) > b.max {
            if len(cur) > 0 {
                chunks, cur = append(chunks, cur), nil
            }
            for len(keys) > 0 {
                n := min(b.max, len(keys))
                chunks = append(chunks, keys[:n])
                keys = keys[n:]
            }
            continue
        }
        if len(cur)+len(keys) > b.max {
            chunks, cur = append(chunks, cur), nil
        }
        cur = append(cur, keys...)
    }
    if len(cur) > 0 {
        chunks = append(chunks, cur)
    }
    return chunks
}

func (b *batched) Decide(ctx context.Context, state any, questions map[string]Question) (*Decision, error) {
    if len(questions) <= b.max {
        return b.Decider.Decide(ctx, state, questions)
    }
    chunks := b.chunks(questions)
    results := make([]*Decision, len(chunks))
    g, gctx := errgroup.WithContext(ctx)
    for i, chunk := range chunks {
        g.Go(func() error {
            qs := make(map[string]Question, len(chunk))
            for _, k := range chunk {
                qs[k] = questions[k]
            }
            d, err := b.Decider.Decide(gctx, state, qs)
            if err != nil {
                return err
            }
            results[i] = d
            return nil
        })
    }
    if err := g.Wait(); err != nil {
        return nil, err
    }
    merged := &Decision{Answers: map[string]*Answer{}}
    for _, d := range results {
        if d == nil {
            continue
        }
        maps.Copy(merged.Answers, d.Answers)
        merged.Usage.Add(d.Usage)
        merged.Raw = append(merged.Raw, d.Raw...)
    }
    return merged, nil
}
