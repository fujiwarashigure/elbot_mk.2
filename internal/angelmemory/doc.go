// Package angelmemory is reserved for ElBot's clean-room memory layer.
//
// The public contract for this package is intentionally not implemented yet.
// Future work will add:
//
//   - memory records and retrieval backed by local SQLite / FTS;
//   - optional embedding and rerank clients;
//   - recall / remember / note tools registered in Tool Runtime;
//   - llm.turn.prepared injection through hook.LLMPayload.SystemAppend;
//   - post-response consolidation triggered by platform.message.sent.
//
// No GPL/AGPL code, prompts, schemas, templates, or assets may be copied into
// this package. See the repository's clean-room and licensing notes before
// adding an implementation.
package angelmemory
