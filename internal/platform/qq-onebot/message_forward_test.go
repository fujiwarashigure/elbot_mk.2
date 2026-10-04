package qqonebot

import (
	"strings"
	"testing"
)

func TestNormalizeSegmentsPreservesFormatting(t *testing.T) {
	msg := normalizeSegments([]Segment{
		{Type: "text", Data: map[string]any{"text": "package main\n\n\tfmt.Println(\"hi\")"}},
	}, 1000)
	want := "package main\n\n\tfmt.Println(\"hi\")"
	if msg.Text != want {
		t.Fatalf("text = %q, want %q", msg.Text, want)
	}
}

func TestNormalizeForwardSegmentExpandsNodesWithMetadata(t *testing.T) {
	raw := `[{"type":"forward","data":{"content":[{"type":"node","data":{
		"user_id":"2001",
		"nickname":"Alice",
		"time":1710000000,
		"message_id":"1001",
		"content":[{"type":"text","data":{"text":"第一行\n第二行"}}]
	}},{"type":"node","data":{
		"user_id":"2002",
		"card":"Bob",
		"time":1710000000,
		"message_id":"1002",
		"content":[{"type":"at","data":{"qq":"3001"}},{"type":"text","data":{"text":" 收到"}}]
	}}]}}]`
	msg := normalizeMessage([]byte(raw), "", 1000)
	for _, want := range []string{forwardTrustMarker, "发送者：Alice", "消息ID：1001", "第一行", "第二行", "发送者：Bob", "消息ID：1002", "[at qq:3001]"} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("forward text missing %q:\n%s", want, msg.Text)
		}
	}
}

func TestNormalizeForwardSegmentBounds(t *testing.T) {
	raw := `[{"type":"forward","data":{"content":[{"type":"node","data":{"user_id":"1","content":[{"type":"text","data":{"text":"abcdef"}}]}},{"type":"node","data":{"user_id":"2","content":[{"type":"text","data":{"text":"ghijkl"}}]}}]}}]`
	msg := normalizeMessageWithLimits([]byte(raw), "", 1000, ForwardLimits{MaxNodes: 1, MaxRunes: 20, MaxDepth: 1})
	if msg.Text == "" || strings.Count(msg.Text, "[转发节点]") != 1 {
		t.Fatalf("bounded forward text = %q", msg.Text)
	}
}

func TestNormalizeForwardSegmentKeepsUnresolvedID(t *testing.T) {
	msg := normalizeMessage([]byte(`[{"type":"forward","data":{"id":"forward-1"}}]`), "", 1000)
	if len(msg.ForwardIDs) != 1 || msg.ForwardIDs[0] != "forward-1" {
		t.Fatalf("forward ids = %#v", msg.ForwardIDs)
	}
	if !strings.Contains(msg.Text, "forward-1") {
		t.Fatalf("forward text = %q", msg.Text)
	}
}

func TestForwardContentStaysOutOfMatchText(t *testing.T) {
	raw := `[{"type":"text","data":{"text":"唤醒 原消息"}},{"type":"forward","data":{"content":[{"type":"node","data":{"user_id":"2001","content":[{"type":"text","data":{"text":"唤醒 隐藏内容"}},{"type":"at","data":{"qq":"1000"}}]}}]}}]`
	msg := normalizeMessage([]byte(raw), "", 1000)
	if !strings.Contains(msg.Text, "隐藏内容") {
		t.Fatalf("display text should keep forward content: %q", msg.Text)
	}
	if strings.Contains(msg.MatchText, "隐藏内容") {
		t.Fatalf("match text must exclude forward content: %q", msg.MatchText)
	}
	if strings.Contains(msg.MatchText, "1000") {
		t.Fatalf("forwarded @ must not become a direct mention: %q", msg.MatchText)
	}
	if !strings.Contains(msg.MatchText, "原消息") {
		t.Fatalf("direct text should remain in match text: %q", msg.MatchText)
	}
	if len(msg.Mentions) != 0 {
		t.Fatalf("forwarded at leaked into mentions: %#v", msg.Mentions)
	}
}

func TestForwardDuplicateReferenceIsMarkedNotReexpanded(t *testing.T) {
	raw := `[{"type":"forward","data":{"content":[{"type":"node","data":{"user_id":"1","content":[{"type":"text","data":{"text":"重复节点"}}]}},{"type":"node","data":{"user_id":"1","content":[{"type":"text","data":{"text":"重复节点"}}]}}]}}]`
	msg := normalizeMessage([]byte(raw), "", 1000)
	if strings.Count(msg.Text, "重复节点") != 2 {
		t.Fatalf("legitimate duplicate sibling nodes should both render: %q", msg.Text)
	}
}
