// Package historygate centralizes the decision of whether a new chat-history
// row may be persisted. Adapters only receive a repository; the repository is
// wrapped once at application startup, so future adapters cannot forget to ask
// the current group policy before writing.
package historygate

import (
	"context"
	"strings"
	"sync"
	"time"

	"elbot/internal/storage"
)

// Policy reports whether new history writes for platform+scopeID are allowed.
// A nil policy means "allow", preserving behavior for tests and deployments
// that do not configure group policy.
type Policy struct {
	mu sync.RWMutex
	fn func(platform, scopeID string) bool
}

// NewPolicy returns an empty policy that allows all scopes until Set is called.
func NewPolicy() *Policy { return &Policy{} }

// Set installs the policy callback. Passing nil restores allow-all behavior.
func (p *Policy) Set(fn func(platform, scopeID string) bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.fn = fn
	p.mu.Unlock()
}

// Allowed reports whether a new row for platform+scopeID may be written.
func (p *Policy) Allowed(platform, scopeID string) bool {
	if p == nil {
		return true
	}
	p.mu.RLock()
	fn := p.fn
	p.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn(strings.TrimSpace(platform), strings.TrimSpace(scopeID))
}

// ChatRepository wraps a storage.ChatHistoryRepository and drops writes whose
// trusted platform/scope has history disabled. Reads always delegate, so
// turning history off never deletes or hides existing records by itself.
type ChatRepository struct {
	repo   storage.ChatHistoryRepository
	policy *Policy
}

// WrapChat returns a policy-aware view of repo. A nil repo is returned as-is.
func WrapChat(repo storage.ChatHistoryRepository, policy *Policy) storage.ChatHistoryRepository {
	if repo == nil {
		return nil
	}
	if policy == nil {
		policy = NewPolicy()
	}
	return &ChatRepository{repo: repo, policy: policy}
}

func (r *ChatRepository) Append(ctx context.Context, message *storage.ChatMessage) error {
	if r == nil || r.repo == nil || message == nil {
		return nil
	}
	if !r.policy.Allowed(message.Platform, message.PlatformScopeID) {
		return nil
	}
	return r.repo.Append(ctx, message)
}

func (r *ChatRepository) GetByPlatformMessage(ctx context.Context, platform, scopeID, platformMessageID string) (*storage.ChatMessage, error) {
	return r.repo.GetByPlatformMessage(ctx, platform, scopeID, platformMessageID)
}

func (r *ChatRepository) Search(ctx context.Context, req storage.ChatHistorySearchRequest) ([]storage.ChatMessage, error) {
	return r.repo.Search(ctx, req)
}

func (r *ChatRepository) Around(ctx context.Context, req storage.ChatHistoryAroundRequest) ([]storage.ChatMessage, error) {
	return r.repo.Around(ctx, req)
}

func (r *ChatRepository) DeleteBefore(ctx context.Context, cutoff time.Time) (int, error) {
	return r.repo.DeleteBefore(ctx, cutoff)
}

// ListRange is always exposed. It delegates when the wrapped repository
// implements storage.ChatHistoryRangeRepository; otherwise it returns a
// not-supported error so callers can still fall back if they probe first.
func (r *ChatRepository) ListRange(ctx context.Context, req storage.ChatHistoryRangeRequest) ([]storage.ChatMessage, error) {
	rangeRepo, ok := r.repo.(storage.ChatHistoryRangeRepository)
	if !ok {
		return nil, errRangeUnsupported
	}
	return rangeRepo.ListRange(ctx, req)
}

// OutboundRepository wraps storage.OutboundMessageRepository with the same
// trusted-scope policy. Outbound rows are assistant messages that were really
// sent, so they are part of the persistent chat history surface.
type OutboundRepository struct {
	repo   storage.OutboundMessageRepository
	policy *Policy
}

// WrapOutbound returns a policy-aware view of repo. A nil repo is returned as-is.
func WrapOutbound(repo storage.OutboundMessageRepository, policy *Policy) storage.OutboundMessageRepository {
	if repo == nil {
		return nil
	}
	if policy == nil {
		policy = NewPolicy()
	}
	return &OutboundRepository{repo: repo, policy: policy}
}

func (r *OutboundRepository) Append(ctx context.Context, message *storage.OutboundMessage) error {
	if r == nil || r.repo == nil || message == nil {
		return nil
	}
	if !r.policy.Allowed(message.Platform, message.PlatformScopeID) {
		return nil
	}
	return r.repo.Append(ctx, message)
}

func (r *OutboundRepository) ListRange(ctx context.Context, req storage.OutboundMessageRangeRequest) ([]storage.OutboundMessage, error) {
	return r.repo.ListRange(ctx, req)
}

func (r *OutboundRepository) DeleteBefore(ctx context.Context, cutoff time.Time) (int, error) {
	return r.repo.DeleteBefore(ctx, cutoff)
}

// CountRange is exposed for storage.OutboundMessageCounter callers. It
// delegates when supported and otherwise returns a not-supported error.
func (r *OutboundRepository) CountRange(ctx context.Context, req storage.OutboundMessageRangeRequest) (int, error) {
	counter, ok := r.repo.(storage.OutboundMessageCounter)
	if !ok {
		return 0, errRangeUnsupported
	}
	return counter.CountRange(ctx, req)
}

var errRangeUnsupported = &unsupportedError{}

type unsupportedError struct{}

func (*unsupportedError) Error() string {
	return "history range access is not supported by the wrapped repository"
}
