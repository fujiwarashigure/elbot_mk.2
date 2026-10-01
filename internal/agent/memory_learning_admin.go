package agent

import "context"

// AngelMemoryCount returns the number of long-memory records for one scope.
func (a *Agent) AngelMemoryCount(ctx context.Context, platform, scopeID string) (int, error) {
	if a == nil || a.angelMemory == nil {
		return 0, nil
	}
	return a.angelMemory.Count(ctx, platform, scopeID)
}

// SelfLearningStats returns pending and approved candidate counts for one scope.
func (a *Agent) SelfLearningStats(ctx context.Context, platform, scopeID string) (int, int, error) {
	if a == nil || a.selfLearning == nil {
		return 0, 0, nil
	}
	return a.selfLearning.Stats(ctx, platform, scopeID)
}
