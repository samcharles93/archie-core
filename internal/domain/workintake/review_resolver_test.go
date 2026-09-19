package workintake

import (
	"context"
	"errors"
	"testing"

	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

type reviewTaskLookupFunc func(context.Context, string, string, int) (*workflowtask.Task, error)

func (f reviewTaskLookupFunc) OpenTaskByPR(ctx context.Context, owner, repo string, number int) (*workflowtask.Task, error) {
	return f(ctx, owner, repo, number)
}

func TestReviewTaskResolverDropsOnlyUnownedReactions(t *testing.T) {
	want := &workflowtask.Task{ID: 7, Owner: "acme", Repo: "widgets", PRNumber: 42, Status: workflowtask.StatusPROpen}
	lookupErr := errors.New("state store unavailable")
	resolver := &ReviewTaskResolver{Tasks: reviewTaskLookupFunc(func(_ context.Context, owner, repo string, number int) (*workflowtask.Task, error) {
		switch number {
		case 42:
			if owner != want.Owner || repo != want.Repo {
				t.Fatalf("lookup coordinates = %s/%s, want %s/%s", owner, repo, want.Owner, want.Repo)
			}
			return want, nil
		case 43:
			return nil, nil
		default:
			return nil, lookupErr
		}
	})}

	got, err := resolver.Resolve(t.Context(), ReviewCommentEnvelope{Owner: "acme", Repo: "widgets", PRNumber: 42})
	if err != nil || got != want || resolver.Dropped() != 0 {
		t.Fatalf("Resolve(owned) = (%+v, %v), dropped=%d", got, err, resolver.Dropped())
	}
	got, err = resolver.Resolve(t.Context(), ReviewCommentEnvelope{Owner: "acme", Repo: "widgets", PRNumber: 43})
	if err != nil || got != nil || resolver.Dropped() != 1 {
		t.Fatalf("Resolve(unowned) = (%+v, %v), dropped=%d, want nil, nil, 1", got, err, resolver.Dropped())
	}
	got, err = resolver.Resolve(t.Context(), ReviewCommentEnvelope{Owner: "acme", Repo: "widgets", PRNumber: 44})
	if !errors.Is(err, lookupErr) || got != nil || resolver.Dropped() != 1 {
		t.Fatalf("Resolve(error) = (%+v, %v), dropped=%d, want nil, lookup error, 1", got, err, resolver.Dropped())
	}
}
