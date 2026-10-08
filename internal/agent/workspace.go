package agent

import (
	"context"
	"slices"
	"strings"

	"elbot/internal/storage"
)

type sessionWorkspaceStore struct {
	agent   *Agent
	session *storage.Session
}

func (s sessionWorkspaceStore) GetWorkspaceDir(ctx context.Context) (string, error) {
	if s.agent == nil || s.agent.store == nil || s.session == nil || s.session.ID == "" {
		return "", nil
	}
	latest, err := s.agent.store.Sessions().Get(ctx, s.session.ID)
	if err != nil {
		return "", err
	}
	metadata := decodeSessionMetadata(latest.Metadata)
	return strings.TrimSpace(metadata.WorkspaceDir), nil
}

func (s sessionWorkspaceStore) SetWorkspaceDir(ctx context.Context, dir string) error {
	return s.SetWorkspaceDirWithAgentNotice(ctx, dir, false)
}

func (s sessionWorkspaceStore) ClearWorkspaceDir(ctx context.Context) error {
	return s.ClearWorkspaceDirWithAgentNotice(ctx, "", false)
}

func (s sessionWorkspaceStore) HasWorkspaceAgentNoticeDir(ctx context.Context, dir string) (bool, error) {
	dir = strings.TrimSpace(dir)
	if s.agent == nil || s.agent.store == nil || s.session == nil || s.session.ID == "" || dir == "" {
		return false, nil
	}
	latest, err := s.agent.store.Sessions().Get(ctx, s.session.ID)
	if err != nil {
		return false, err
	}
	metadata := decodeSessionMetadata(latest.Metadata)
	s.session.Metadata = latest.Metadata
	return slices.Contains(metadata.WorkspaceAgentNoticeDirs, dir), nil
}

func (s sessionWorkspaceStore) MarkWorkspaceAgentNoticeDir(ctx context.Context, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	return s.mutateMetadata(ctx, func(metadata *sessionMetadata) bool {
		if slices.Contains(metadata.WorkspaceAgentNoticeDirs, dir) {
			return false
		}
		metadata.WorkspaceAgentNoticeDirs = append(metadata.WorkspaceAgentNoticeDirs, dir)
		return true
	})
}

func (s sessionWorkspaceStore) SetWorkspaceDirWithAgentNotice(ctx context.Context, dir string, markNotice bool) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	return s.saveWorkspaceDirWithAgentNotice(ctx, dir, dir, markNotice)
}

func (s sessionWorkspaceStore) ClearWorkspaceDirWithAgentNotice(ctx context.Context, dir string, markNotice bool) error {
	return s.saveWorkspaceDirWithAgentNotice(ctx, "", strings.TrimSpace(dir), markNotice)
}

func (s sessionWorkspaceStore) saveWorkspaceDirWithAgentNotice(ctx context.Context, workspaceDir, noticeDir string, markNotice bool) error {
	return s.mutateMetadata(ctx, func(metadata *sessionMetadata) bool {
		changed := metadata.WorkspaceDir != workspaceDir
		metadata.WorkspaceDir = workspaceDir
		if markNotice && noticeDir != "" && !slices.Contains(metadata.WorkspaceAgentNoticeDirs, noticeDir) {
			metadata.WorkspaceAgentNoticeDirs = append(metadata.WorkspaceAgentNoticeDirs, noticeDir)
			changed = true
		}
		return changed
	})
}

// mutateMetadata applies one metadata change inside a single transaction. The
// callback receives the freshly loaded metadata, so fields a concurrent writer
// (activity, tool cache, naming) changed in the meantime are never dropped.
func (s sessionWorkspaceStore) mutateMetadata(ctx context.Context, mutate func(metadata *sessionMetadata) bool) error {
	if s.agent == nil || s.agent.store == nil || s.session == nil || s.session.ID == "" {
		return nil
	}
	updated, err := s.agent.store.Sessions().Mutate(ctx, s.session.ID, func(current *storage.Session) error {
		metadata := decodeSessionMetadata(current.Metadata)
		if !mutate(&metadata) {
			return nil
		}
		encoded := encodeSessionMetadataInto(current.Metadata, metadata)
		if encoded == current.Metadata {
			return nil
		}
		current.Metadata = encoded
		current.UpdatedAt = storage.Now()
		return nil
	})
	if err != nil {
		return err
	}
	s.session.Metadata = updated.Metadata
	return nil
}
