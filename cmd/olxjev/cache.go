package main

import (
    "container/list"
    "strings"
    "sync"
    "unicode"
)

// lru is a fixed-capacity least-recently-used cache, safe for concurrent
// use. A capacity of 0 stores nothing.
type lru[K comparable, V any] struct {
    mu       sync.Mutex
    capacity int
    order    *list.List // front = most recently used; values are *lruEntry
    items    map[K]*list.Element
}

type lruEntry[K comparable, V any] struct {
    key   K
    value V
}

func newLRU[K comparable, V any](capacity int) *lru[K, V] {
    return &lru[K, V]{capacity: capacity, order: list.New(), items: map[K]*list.Element{}}
}

// Get returns the value for key and marks it most recently used.
func (c *lru[K, V]) Get(key K) (V, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if e, ok := c.items[key]; ok {
        c.order.MoveToFront(e)
        return e.Value.(*lruEntry[K, V]).value, true
    }
    var zero V
    return zero, false
}

// Add stores value under key, evicting the least recently used entry when
// the cache is full.
func (c *lru[K, V]) Add(key K, value V) {
    if c.capacity <= 0 {
        return
    }
    c.mu.Lock()
    defer c.mu.Unlock()
    if e, ok := c.items[key]; ok {
        e.Value.(*lruEntry[K, V]).value = value
        c.order.MoveToFront(e)
        return
    }
    c.items[key] = c.order.PushFront(&lruEntry[K, V]{key: key, value: value})
    if c.order.Len() > c.capacity {
        oldest := c.order.Back()
        c.order.Remove(oldest)
        delete(c.items, oldest.Value.(*lruEntry[K, V]).key)
    }
}

// Len is the number of cached entries.
func (c *lru[K, V]) Len() int {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.order.Len()
}

var apostrophes = strings.NewReplacer("’", "'", "ʼ", "'", "`", "'", "‘", "'")

// normalizeQuery is the cache key: queries that differ only in case,
// whitespace, apostrophe style or punctuation at the ends share it.
func normalizeQuery(q string) string {
    q = apostrophes.Replace(strings.ToLower(q))
    q = strings.Join(strings.Fields(q), " ")
    return strings.TrimFunc(q, func(r rune) bool { return unicode.IsPunct(r) && r != '\'' || unicode.IsSpace(r) })
}
