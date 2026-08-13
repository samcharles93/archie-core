package main

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/taskrun"
)

type recordingTaskRunSubscriber struct {
	subject      string
	queue        string
	handler      nats.MsgHandler
	subscribeErr error
	queueErr     error
	subscription *nats.Subscription
	subscribe    int
	queueSub     int
}

func (s *recordingTaskRunSubscriber) Subscribe(subject string, handler nats.MsgHandler) (*nats.Subscription, error) {
	s.subscribe++
	s.subject = subject
	s.handler = handler
	return s.subscription, s.subscribeErr
}

func (s *recordingTaskRunSubscriber) QueueSubscribe(subject, queue string, handler nats.MsgHandler) (*nats.Subscription, error) {
	s.queueSub++
	s.subject = subject
	s.queue = queue
	s.handler = handler
	return s.subscription, s.queueErr
}

func TestSubscribeTaskRunsWithSelectsSubscriptionMode(t *testing.T) {
	taskID := int64(42)
	subscription := &nats.Subscription{}
	log := slog.New(slog.DiscardHandler)

	tests := []struct {
		name          string
		hasTaskID     bool
		wantSubject   string
		wantQueue     string
		wantSubscribe int
		wantQueueSub  int
	}{
		{
			name:          "dedicated task uses direct per-task subject",
			hasTaskID:     true,
			wantSubject:   taskrun.SubjectForTask(taskID),
			wantSubscribe: 1,
		},
		{
			name:         "shared worker uses wildcard queue group",
			hasTaskID:    false,
			wantSubject:  taskRunSubjectWildcard,
			wantQueue:    taskRunQueueGroup,
			wantQueueSub: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subscriber := &recordingTaskRunSubscriber{subscription: subscription}
			callbackCalls := 0
			callback := func(*nats.Msg) { callbackCalls++ }

			got, err := subscribeTaskRunsWith(subscriber, log, taskID, test.hasTaskID, callback)
			if err != nil {
				t.Fatalf("subscribeTaskRunsWith() error = %v", err)
			}
			if got != subscription {
				t.Fatalf("subscribeTaskRunsWith() subscription = %p, want %p", got, subscription)
			}
			if subscriber.subject != test.wantSubject || subscriber.queue != test.wantQueue {
				t.Fatalf("subscription = (%q, %q), want (%q, %q)", subscriber.subject, subscriber.queue, test.wantSubject, test.wantQueue)
			}
			if subscriber.subscribe != test.wantSubscribe || subscriber.queueSub != test.wantQueueSub {
				t.Fatalf("call counts = (subscribe %d, queue %d), want (%d, %d)", subscriber.subscribe, subscriber.queueSub, test.wantSubscribe, test.wantQueueSub)
			}
			if subscriber.handler == nil {
				t.Fatal("subscription callback is nil")
			}
			subscriber.handler(&nats.Msg{})
			if callbackCalls != 1 {
				t.Fatalf("callback calls = %d, want 1", callbackCalls)
			}
		})
	}
}

func TestSubscribeTaskRunsWithReturnsSubscriptionError(t *testing.T) {
	wantErr := errors.New("subscribe failed")
	tests := []struct {
		name       string
		hasTaskID  bool
		subscriber *recordingTaskRunSubscriber
	}{
		{
			name:       "dedicated subscription",
			hasTaskID:  true,
			subscriber: &recordingTaskRunSubscriber{subscribeErr: wantErr},
		},
		{
			name:       "shared queue subscription",
			subscriber: &recordingTaskRunSubscriber{queueErr: wantErr},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := subscribeTaskRunsWith(test.subscriber, slog.New(slog.DiscardHandler), 42, test.hasTaskID, func(*nats.Msg) {})
			if !errors.Is(err, wantErr) {
				t.Fatalf("subscribeTaskRunsWith() error = %v, want %v", err, wantErr)
			}
		})
	}
}
