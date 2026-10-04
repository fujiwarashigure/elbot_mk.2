package qqonebot

import (
	"context"
	"testing"

	"elbot/internal/platform"
)

type captureEventHandler struct {
	event platform.PlatformEvent
	count int
}

func (h *captureEventHandler) HandleMessage(context.Context, string) error { return nil }

func (h *captureEventHandler) HandlePlatformEvent(_ context.Context, event platform.PlatformEvent) error {
	h.event = event
	h.count++
	return nil
}

func TestHandlePlatformEventDispatchesNotice(t *testing.T) {
	adapter := New(Config{}, nil, nil, nil)
	handler := &captureEventHandler{}
	adapter.handlePlatformEvent(context.Background(), handler, Event{
		PostType:   "notice",
		NoticeType: "group_recall",
		GroupID:    9,
		UserID:     1001,
		OperatorID: 1002,
		MessageID:  77,
	})
	if handler.count != 1 {
		t.Fatalf("event count = %d", handler.count)
	}
	if handler.event.Kind != platform.EventNotice || handler.event.Type != "group_recall" || handler.event.ScopeID != "group:9" {
		t.Fatalf("event = %#v", handler.event)
	}
	if handler.event.UserID != "1002" || handler.event.MessageID != "77" {
		t.Fatalf("event routing = %#v", handler.event)
	}
}
