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

// WithAttrs bakes the current group prefix into each new attr's key
// immediately, rather than storing it raw and re-deriving the key at
// Handle-time from whatever the group chain has grown to by then. Deferring
// it would nest an attr under a group added by a *later* WithGroup call it
// was never actually inside -- slog's contract is that WithGroup affects
// only attrs added afterwards.
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

// PrefixAttrs renames each attr's key with the given dotted group chain, so
// it carries its grouping with it regardless of what the chain grows to
// later. Call this from a slog.Handler's WithAttrs, never from Handle: a
// record's own attrs are always evaluated at the current group nesting, so
// they're flattened with the live chain instead (see FlattenAttrs).
// Exported for the same reason as FlattenAttrs: more than one handler needs
// to get slog's group-nesting contract right (FeedHandler here, and
// agentexec's system-log handler).
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

// FlattenAttrs merges base (handler-bound attrs from WithAttrs, already
// keyed with whatever group chain was active when each was added -- see
// prefixAttrs) with a record's own attrs, grouped under the chain currently
// active at Handle-time, into the single flat map Entry.Fields expects on
// decode. Exported because more than one slog.Handler needs to produce
// Entry-shaped output from a slog.Record: FeedHandler here, and agentexec's
// system-log handler shipping a task's own logs.
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

// attrValue resolves a slog attr to what Entry.Fields should hold on the
// wire. Both consumers of FlattenAttrs (SystemLogHandler, FeedHandler)
// json.Marshal Entry.Fields directly rather than through slog's own JSON
// handler, so an `error` value must be resolved to its message string here:
// encoding/json has no Error()-aware special case, and the concrete types
// behind almost every Go error (errors.errorString, fmt.wrapError, ...) hold
// only unexported fields, which marshal to `{}` and silently discard the
// message.
func attrValue(v slog.Value) any {
	if v.Kind() == slog.KindAny {
		if err, ok := v.Any().(error); ok && !isNilError(err) {
			return err.Error()
		}
	}
	return v.Any()
}

// isNilError reports whether err is nil in the way that matters before
// calling Error(): a plain nil interface, or the classic Go footgun where a
// non-nil interface wraps a nil concrete pointer (var e *T; var err error =
// e -- err != nil is true). A logging package's whole job is to never be the
// thing that crashes the process, so this checks for both rather than
// trusting every Error() method in this module and its dependencies to
// tolerate a nil receiver.
func isNilError(err error) bool {
	if err == nil {
		return true
	}
	rv := reflect.ValueOf(err)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}
