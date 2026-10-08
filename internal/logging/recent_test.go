package logging

import (
	"strings"
	"testing"
)

func TestRecentLogsBounds(t *testing.T) {
	tests := []struct {
		name      string
		entries   []Entry
		query     Query
		count     int
		truncated bool
	}{
		{name: "level and component filter", entries: []Entry{{Level: "INFO", Message: "older"}, {Level: "ERROR", Message: "failure", Fields: map[string]any{"component": "store"}}}, query: Query{Levels: []string{"error"}, Component: "store"}, count: 1},
		{name: "recent limit", entries: []Entry{{Message: "older"}, {Message: "newer"}}, query: Query{Limit: 1}, count: 1, truncated: true},
		{name: "oversized record does not hide other recent lines", entries: []Entry{{Message: "normal"}, {Message: strings.Repeat("x", 1<<20)}}, count: 1, truncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feed := NewFeed(1000)
			for _, entry := range tt.entries {
				feed.append(entry)
			}
			result, err := feed.Read(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Entries) != tt.count || result.Truncated != tt.truncated {
				t.Fatalf("count %d truncated %v", len(result.Entries), result.Truncated)
			}
			if tt.name == "recent limit" && result.Entries[0].Message != "newer" {
				t.Fatal("read retained older entry")
			}
		})
	}
}
