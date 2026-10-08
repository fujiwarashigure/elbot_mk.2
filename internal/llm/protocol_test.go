package llm

import (
	"context"
	"testing"
)

type capabilityFake struct {
	caps  ProtocolCapabilities
	model string
}

func (c *capabilityFake) ChatStream(context.Context, ChatRequest) (<-chan StreamChunk, error) {
	return nil, nil
}

func (c *capabilityFake) ListModels(context.Context) ([]string, error) { return nil, nil }

func (c *capabilityFake) ProtocolCapabilitiesFor(model string) ProtocolCapabilities {
	c.model = model
	caps := c.caps
	return caps
}

type plainFake struct{}

func (plainFake) ChatStream(context.Context, ChatRequest) (<-chan StreamChunk, error) {
	return nil, nil
}

func (plainFake) ListModels(context.Context) ([]string, error) { return nil, nil }

// 没实现查询接口的适配器必须得到最保守的答案，而不是零值（零值会让 Protocol 变成空字符串，
// 让"哪套协议"这个问题没有答案）。
func TestProtocolCapabilitiesOfFallsBackToChatCompletions(t *testing.T) {
	caps := ProtocolCapabilitiesOf(plainFake{}, "m")
	if caps.Protocol != ProtocolChatCompletions {
		t.Fatalf("protocol = %q", caps.Protocol)
	}
	if caps.ServerSideConversation || caps.NativeCompaction || caps.IncrementalTools || caps.ServerSideStore {
		t.Fatalf("server-side capabilities must stay false: %#v", caps)
	}
}

func TestProtocolCapabilitiesOfForwardsModelAndCaps(t *testing.T) {
	client := &capabilityFake{caps: ProtocolCapabilities{Protocol: ProtocolResponses, NativeCompaction: true}}
	caps := ProtocolCapabilitiesOf(client, "resp-model")
	if client.model != "resp-model" {
		t.Fatalf("model = %q", client.model)
	}
	if caps.Protocol != ProtocolResponses || !caps.NativeCompaction {
		t.Fatalf("caps = %#v", caps)
	}
}

// 报告者为空协议时补上最弱协议：空 Protocol 会让分派点的默认分支失效。
func TestProtocolCapabilitiesOfFillsEmptyProtocol(t *testing.T) {
	caps := ProtocolCapabilitiesOf(&capabilityFake{caps: ProtocolCapabilities{NativeCompaction: true}}, "m")
	if caps.Protocol != ProtocolChatCompletions {
		t.Fatalf("protocol = %q", caps.Protocol)
	}
}
