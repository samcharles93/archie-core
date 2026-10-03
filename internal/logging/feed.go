package logging

import (
	"context"
	"log/slog"
	"reflect"
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
	fields := FlattenAttrs(record, h.attrs, h.groups)
	h.feed.append(Entry{Time: record.Time, Level: record.Level.String(), Message: record.Message, Fields: fields})
	return h.next.Handle(ctx, record)
}

// WithAttrs prefixes attrs with the current group chain now.
func (h *FeedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), PrefixAttrs(attrs, h.groups)...)
	clone.next = h.next.WithAttrs(attrs)
	return &clone
}

func (h *FeedHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	clone.next = h.next.WithGroup(name)
	return &clone
}

// PrefixAttrs prefixes each attr's key with the group chain. For use in
// WithAttrs.
func PrefixAttrs(attrs []slog.Attr, groups []string) []slog.Attr {
	if len(groups) == 0 {
		return attrs
	}
	out := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		key := attr.Key
		for _, group := range groups {
			if group != "" {
				key = group + "." + key
			}
		}
		out[i] = slog.Attr{Key: key, Value: attr.Value}
	}
	return out
}

// FlattenAttrs merges base with a record's attrs under the current group
// chain into a flat map.
func FlattenAttrs(record slog.Record, base []slog.Attr, groups []string) map[string]any {
	fields := map[string]any{}
	for _, attr := range base {
		addAttr(fields, nil, attr)
	}
	record.Attrs(func(attr slog.Attr) bool { addAttr(fields, groups, attr); return true })
	if len(fields) == 0 {
		return nil
	}
	return fields
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
	fields[key] = attrValue(attr.Value)
}

// attrValue resolves an attr for JSON encoding; errors become their message.
func attrValue(v slog.Value) any {
	if v.Kind() == slog.KindAny {
		if err, ok := v.Any().(error); ok && !isNilError(err) {
			return err.Error()
		}
	}
	return v.Any()
}

// isNilError reports whether err is nil or a nil pointer in an interface.
func isNilError(err error) bool {
	if err == nil {
		return true
	}
	rv := reflect.ValueOf(err)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}
