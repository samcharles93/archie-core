package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

var _ storecontract.EventTypeStore = (*EDA)(nil)

func eventTypeValue(r postgresdb.EventType) (eventtype.EventType, error) {
	t := eventtype.EventType{ID: r.ID, Source: r.Source, Name: r.Name, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if err := json.Unmarshal([]byte(r.Rule), &t.Rule); err != nil {
		return eventtype.EventType{}, fmt.Errorf("edastore: decode event type rule: %w", err)
	}
	if err := json.Unmarshal([]byte(r.Schema), &t.Schema); err != nil {
		return eventtype.EventType{}, fmt.Errorf("edastore: decode event type schema: %w", err)
	}
	return t, nil
}

func eventTypeValues(rows []postgresdb.EventType) ([]eventtype.EventType, error) {
	out := make([]eventtype.EventType, 0, len(rows))
	for _, r := range rows {
		t, err := eventTypeValue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// identifyCapture returns the ID of the one event type on the capture's
// source that matches it, or "" when it is unidentified.
func (s *EDA) identifyCapture(ctx context.Context, c storecontract.CapturedEvent) (string, error) {
	rows, err := s.q.EventTypesForSource(ctx, c.Source)
	if err != nil {
		return "", fmt.Errorf("edastore: load event types: %w", err)
	}
	types, err := eventTypeValues(rows)
	if err != nil {
		return "", err
	}
	t, _ := eventtype.Identify(types, c.Source, eventtype.Sample{
		Headers: eventtype.ParseHeaders(c.Headers),
		Body:    []byte(c.Body),
	})
	return t.ID, nil
}

// saveEventType validates t, then, under a per-source lock, refuses it if its
// rule overlaps another type on the source and runs write.
func (s *EDA) saveEventType(ctx context.Context, t eventtype.EventType, write func(*postgresdb.Queries, string) error) error {
	if err := t.Validate(); err != nil {
		return err
	}
	rule, err := json.Marshal(t.Rule)
	if err != nil {
		return fmt.Errorf("edastore: encode event type rule: %w", err)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockEventTypeSource(ctx, t.Source); err != nil {
			return fmt.Errorf("edastore: lock event types: %w", err)
		}
		rows, err := q.EventTypesForSource(ctx, t.Source)
		if err != nil {
			return fmt.Errorf("edastore: load event types: %w", err)
		}
		existing, err := eventTypeValues(rows)
		if err != nil {
			return err
		}
		if err := eventtype.CheckOverlap(existing, t); err != nil {
			return err
		}
		err = write(q, string(rule))
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: name %q is taken on source %q", eventtype.ErrInvalid, t.Name, t.Source)
		}
		return err
	})
}

// InsertEventType stores a new event type.
func (s *EDA) InsertEventType(ctx context.Context, t eventtype.EventType) (string, error) {
	t.ID = ""
	schema, err := json.Marshal(t.Schema)
	if err != nil {
		return "", fmt.Errorf("edastore: encode event type schema: %w", err)
	}
	id := newRecordID()
	err = s.saveEventType(ctx, t, func(q *postgresdb.Queries, rule string) error {
		return q.InsertEventType(ctx, postgresdb.InsertEventTypeParams{
			ID: id, Source: t.Source, Name: t.Name, Rule: rule, Schema: string(schema),
		})
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// UpdateEventType rewrites a type's name and rule. Its source and schema are
// the stored ones.
func (s *EDA) UpdateEventType(ctx context.Context, t eventtype.EventType) error {
	stored, err := s.q.GetEventType(ctx, t.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.ErrEventTypeNotFound
	}
	if err != nil {
		return err
	}
	t.Source = stored.Source
	return s.saveEventType(ctx, t, func(q *postgresdb.Queries, rule string) error {
		n, err := q.UpdateEventType(ctx, postgresdb.UpdateEventTypeParams{ID: t.ID, Name: t.Name, Rule: rule})
		if err == nil && n == 0 {
			return storecontract.ErrEventTypeNotFound
		}
		return err
	})
}

// DeleteEventType removes a type. Captures already identified as it keep the
// ID they were stored with.
func (s *EDA) DeleteEventType(ctx context.Context, id string) error {
	n, err := s.q.DeleteEventType(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrEventTypeNotFound
	}
	return nil
}

// ListEventTypes returns every event type, by source then name.
func (s *EDA) ListEventTypes(ctx context.Context) ([]eventtype.EventType, error) {
	rows, err := s.q.ListEventTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("edastore: list event types: %w", err)
	}
	return eventTypeValues(rows)
}
