package logging

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Feed is a logging-owned bounded replay buffer. It is intentionally separate
// from task activity: daemon diagnostics are not lifecycle events.
type Feed struct {
	mu      sync.RWMutex
	entries []Entry
	limit   int
	nextID  atomic.Int64
	subs    map[chan Entry]struct{}
}

func NewFeed(limit int) *Feed {
	if limit <= 0 {
		limit = 500
	}
	return &Feed{limit: limit, subs: make(map[chan Entry]struct{})}
}

func (f *Feed) Snapshot() []Entry {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]Entry(nil), f.entries...)
}

func (f *Feed) Subscribe(ctx context.Context) <-chan Entry {
	ch := make(chan Entry, 64)
	if f == nil {
		close(ch)
		return ch
	}
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		if _, ok := f.subs[ch]; ok {
			delete(f.subs, ch)
			close(ch)
		}
		f.mu.Unlock()
	}()
	return ch
}

func (f *Feed) append(entry Entry) {
	if f == nil {
		return
	}
	entry.ID = f.nextID.Add(1)
	f.mu.Lock()
	if len(f.entries) == f.limit {
		copy(f.entries, f.entries[1:])
		f.entries[len(f.entries)-1] = entry
	} else {
		f.entries = append(f.entries, entry)
	}
	for ch := range f.subs {
		select {
		case ch <- entry:
		default:
		}
	}
	f.mu.Unlock()
}

// FeedHandler writes to the wrapped slog handler and copies the same record to
// a bounded live feed. The dashboard never parses slog JSON itself.
type FeedHandler struct {
	next   slog.Handler
	feed   *Feed
	attrs  []slog.Attr
	groups []string
}

func NewFeedHandler(next slog.Handler, feed *Feed) *FeedHandler {
	return &FeedHandler{next: next, feed: feed}
}

func (h *FeedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *FeedHandler) Handle(ctx context.Context, record slog.Record) error {
	fields := map[string]any{}
	for _, attr := range h.attrs {
		addAttr(fields, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool { addAttr(fields, h.groups, attr); return true })
	if len(fields) == 0 {
		fields = nil
	}
	h.feed.append(Entry{Time: record.Time, Level: record.Level.String(), Message: record.Message, Fields: fields})
	return h.next.Handle(ctx, record)
}

func (h *FeedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *FeedHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

func addAttr(fields map[string]any, groups []string, attr slog.Attr) {
	if attr.Equal(slog.Attr{}) {
		return
	}
	key := attr.Key
	for _, group := range groups {
		if group != "" {
			key = group + "." + key
		}
	}
	fields[key] = attr.Value.Any()
}
