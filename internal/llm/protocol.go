package llm

// Protocol 标识一个适配器实际使用的线上协议。
type Protocol string

const (
	ProtocolChatCompletions Protocol = "chat_completions"
	ProtocolResponses       Protocol = "responses"
)

// ProtocolCapabilities 描述"这个适配器、这个模型"实际能提供的协议能力。
//
// 它刻意区分"协议理论上支持"与"fork 真的用上了"：fork 的 Responses 适配器是协议翻译层
// （固定 store=false、每轮重放完整历史、没有 previous_response_id / additional_tools），
// 因此它报告 ProtocolResponses 但所有服务端能力都是 false。上层据此分派（例如压缩走客户端
// 还是服务端），换基时只需要改这里的返回值，不必去 agent 各处找隐式的协议假设。
type ProtocolCapabilities struct {
	Protocol Protocol
	// ServerSideConversation 表示协议可以在服务端延续同一段对话（增量引用上一轮响应），
	// 而不是每轮重放完整历史。
	ServerSideConversation bool
	// NativeCompaction 表示压缩可以交给服务端完成，客户端不必自己生成摘要。
	NativeCompaction bool
	// IncrementalTools 表示工具定义可以增量下发，而不必每轮重发完整定义。
	IncrementalTools bool
	// ServerSideStore 表示服务端会保存这次响应并允许回读。
	ServerSideStore bool
}

// ProtocolCapabilityReporter 是适配器可选实现的协议能力查询入口，与 RetryNotifier /
// ModelMetadataProvider 同一套可选接口模式：不实现也能正常工作，由
// ProtocolCapabilitiesOf 给出保守默认值。
type ProtocolCapabilityReporter interface {
	ProtocolCapabilitiesFor(model string) ProtocolCapabilities
}

// ProtocolCapabilitiesOf 回答"这个客户端、这个模型实际支持哪些协议能力"。没有实现查询接口的
// 适配器按最弱的 Chat Completions 语义处理（所有服务端能力为 false），这样调用方永远拿到一个
// 保守答案，不需要在各处做类型断言。
func ProtocolCapabilitiesOf(client LLM, model string) ProtocolCapabilities {
	if reporter, ok := client.(ProtocolCapabilityReporter); ok {
		caps := reporter.ProtocolCapabilitiesFor(model)
		if caps.Protocol == "" {
			caps.Protocol = ProtocolChatCompletions
		}
		return caps
	}
	return ProtocolCapabilities{Protocol: ProtocolChatCompletions}
}
