package completion

import (
	"context"
	"testing"
)

func TestCharacterDirectiveSource(t *testing.T) {
	source := CharacterDirectiveSource{Characters: func(context.Context) []CharacterOption {
		return []CharacterOption{
			{ID: "catgirl", Name: "猫娘", Description: "黏人"},
			{ID: "assistant", Name: "助手"},
		}
	}}
	items := source.Complete(context.Background(), Request{Text: "@char:cat", Cursor: len("@char:cat")})
	if len(items) != 1 || items[0].Text != "@char:catgirl" || items[0].Kind != KindCharacterDirective {
		t.Fatalf("items = %#v", items)
	}
	items = source.Complete(context.Background(), Request{Text: "@c:", Cursor: 3})
	if len(items) != 2 {
		t.Fatalf("empty query should list all characters: %#v", items)
	}
	items = source.Complete(context.Background(), Request{Text: "@c", Cursor: 2})
	if len(items) != 1 || items[0].Text != "@c:" {
		t.Fatalf("prefix-only items = %#v", items)
	}
	if items := source.Complete(context.Background(), Request{Text: "@t:web", Cursor: 6}); len(items) != 0 {
		t.Fatalf("tool directive should not match: %#v", items)
	}
}
