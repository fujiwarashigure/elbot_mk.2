package agent

import "elbot/internal/request"

// ActiveRequests returns a snapshot of currently running turns, tools, hooks and compaction tasks.
func (a *Agent) ActiveRequests() request.Snapshot {
	if a == nil || a.requests == nil {
		return request.Snapshot{}
	}
	return a.requests.Snapshot()
}
