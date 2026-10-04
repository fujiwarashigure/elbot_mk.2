package agent

import (
	"context"
	"strings"
)

// markTurnCanceled records that a turn must not emit any further user-visible
// output. The request manager already cancels the context; this state is the
// last-mile gate for tools/providers that ignore cancellation and return a
// late result anyway.
func (a *Agent) markTurnCanceled(requestID string) {
	requestID = strings.TrimSpace(requestID)
	if a == nil || requestID == "" {
		return
	}
	a.turnMu.Lock()
	if a.turnStates == nil {
		a.turnStates = map[string]bool{}
	}
	a.turnStates[requestID] = true
	a.turnMu.Unlock()
}

// clearTurnState removes the terminal marker after the turn and its queued
// sends have finished. It is idempotent.
func (a *Agent) clearTurnState(requestID string) {
	requestID = strings.TrimSpace(requestID)
	if a == nil || requestID == "" {
		return
	}
	a.turnMu.Lock()
	delete(a.turnStates, requestID)
	a.turnMu.Unlock()
}

// turnOutputAllowed reports whether output may still be sent on ctx. A context
// without a turn request ID is not gated so commands, notices and background
// lifecycle messages keep working. The check is intentionally cheap because
// it is called before every stream flush and platform send.
func (a *Agent) turnOutputAllowed(ctx context.Context) bool {
	if a == nil {
		return true
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return false
		}
	}
	if a.groupRuntimeBlocked(ctx) {
		return false
	}
	requestID := ""
	if ctx != nil {
		requestID = turnRequestIDFromContext(ctx)
	}
	if requestID == "" {
		return true
	}
	a.turnMu.Lock()
	canceled := a.turnStates[requestID]
	a.turnMu.Unlock()
	return !canceled
}
