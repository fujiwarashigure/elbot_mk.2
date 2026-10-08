package toolrun

import (
	"context"
	"testing"

	"elbot/internal/llm"
	"elbot/internal/tool"
)

type backgroundTestTool struct{ name string }

func (t backgroundTestTool) Name() string { return t.name }
func (t backgroundTestTool) Info() tool.Info {
	return tool.NewBuilder(t.name).Risk(tool.RiskLow).BuildInfo()
}
func (t backgroundTestTool) Schema() llm.ToolSchema { return tool.NewBuilder(t.name).BuildSchema() }
func (t backgroundTestTool) Call(context.Context, tool.CallRequest) (*tool.Result, error) {
	return &tool.Result{}, nil
}

func TestBackgroundResolveOnlyAcceptsPreloadedTools(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(backgroundTestTool{name: "plain"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tool.NewDiscoverTool(registry, nil)); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry, nil)
	background := tool.WithSandboxContext(context.Background(), tool.SandboxContext{Background: true, BackgroundKind: tool.BackgroundKindCron})

	// A tool the background task never preloaded must stay unreachable, even
	// though it exists in the global registry.
	if resolved := manager.Resolve(background, "plain", nil); resolved.Available {
		t.Fatalf("background resolve of an undeclared tool = %#v", resolved)
	}
	// discover_tool is never allowed in a background task.
	if resolved := manager.Resolve(background, "discover_tool", nil); resolved.Available {
		t.Fatalf("background resolve of discover_tool = %#v", resolved)
	}
	// Preloaded tools still work.
	cached := []CachedTool{{Name: "plain", Source: SourceKindNative, Schema: llm.ToolSchema{Type: "function", Function: llm.ToolFunctionSchema{Name: "plain"}}}}
	if resolved := manager.Resolve(background, "plain", cached); !resolved.Available {
		t.Fatalf("preloaded background tool = %#v", resolved)
	}

	// Schemas skip discover_tool and foreground-only cached entries.
	schemas, err := manager.Schemas(background, Context{Mode: "work", DisableBaseTools: true}, []CachedTool{
		{Name: "discover_tool", Source: SourceKindNative, Schema: llm.ToolSchema{Type: "function", Function: llm.ToolFunctionSchema{Name: "discover_tool"}}},
		{Name: "plain", Source: SourceKindNative, Schema: llm.ToolSchema{Type: "function", Function: llm.ToolFunctionSchema{Name: "plain"}}},
	})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	for _, schema := range schemas {
		if schema.Function.Name == "discover_tool" {
			t.Fatalf("discover_tool leaked into background schemas: %#v", schemas)
		}
	}
	if len(schemas) != 1 || schemas[0].Function.Name != "plain" {
		t.Fatalf("background schemas = %#v", schemas)
	}
}
