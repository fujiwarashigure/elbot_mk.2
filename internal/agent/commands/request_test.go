package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"elbot/internal/command"
	"elbot/internal/request"
	"elbot/internal/security"
	"elbot/internal/session"
	"elbot/internal/storage"
	"elbot/internal/turn"
)

// superadminTestCtx mirrors a CLI/operator actor; only superadmins may address
// requests outside their own current session.
func superadminTestCtx() context.Context {
	return security.WithActor(context.Background(), security.Actor{ID: "cli:local", Platform: "cli", PlatformUserID: "local", Role: security.RoleSuperadmin})
}

func regularTestCtx() context.Context {
	return security.WithActor(context.Background(), security.Actor{ID: "cli:u1", Platform: "cli", PlatformUserID: "u1", Role: security.RoleUser})
}

func TestRequestsCommandFormatsTree(t *testing.T) {
	ctx := context.Background()
	store := newCommandTestStore(t)
	sessionRow := &storage.Session{ID: "s1", Title: "测试会话", Mode: storage.SessionModeWork, OwnerID: "cli:local", Platform: "cli", PlatformScopeID: "local", CreatedAt: storage.Now(), UpdatedAt: storage.Now()}
	if err := store.Sessions().Create(ctx, sessionRow); err != nil {
		t.Fatalf("create session: %v", err)
	}
	manager := request.NewManager(time.Minute)
	turnReq, _, turnDone, err := manager.Start(ctx, request.StartRequest{SessionID: "s1", Kind: request.KindTurn, Label: "chat"})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	defer turnDone()
	_, _, toolDone, err := manager.Start(ctx, request.StartRequest{ParentID: turnReq.ID, SessionID: "s1", Kind: request.KindTool, Label: "shell"})
	if err != nil {
		t.Fatalf("start tool: %v", err)
	}
	defer toolDone()
	_, _, hookDone, err := manager.Start(ctx, request.StartRequest{ParentID: turnReq.ID, SessionID: "s1", Kind: request.KindHook, Label: "gpt_image_draw_fullwidth"})
	if err != nil {
		t.Fatalf("start hook: %v", err)
	}
	defer hookDone()

	cmd := NewRequests(Deps{Requests: manager, Store: store, Turns: turn.NewManager()})
	result, err := cmd.Handle(ctx, command.Request{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	for _, want := range []string{"[1] chat turn request", "[1.1]", "[1.2]", "shell tool request", "gpt_image_draw_fullwidth hook request", "session: 测试会话", "tool: shell", "hook: gpt_image_draw_fullwidth"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("requests output missing %q:\n%s", want, result.Content)
		}
	}
}

func assertCommandTestCanceled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
}

func TestFormatActiveRequestsUsesTree(t *testing.T) {
	ctx := context.Background()
	store := newCommandTestStore(t)
	sessionRow := &storage.Session{ID: "s1", Title: "状态会话", Mode: storage.SessionModeWork, OwnerID: "cli:local", Platform: "cli", PlatformScopeID: "local", CreatedAt: storage.Now(), UpdatedAt: storage.Now()}
	if err := store.Sessions().Create(ctx, sessionRow); err != nil {
		t.Fatalf("create session: %v", err)
	}
	manager := request.NewManager(time.Minute)
	turnReq, _, turnDone, err := manager.Start(ctx, request.StartRequest{SessionID: "s1", Kind: request.KindTurn, Label: "chat"})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	defer turnDone()
	_, _, toolDone, err := manager.Start(ctx, request.StartRequest{ParentID: turnReq.ID, SessionID: "s1", Kind: request.KindTool, Label: "shell"})
	if err != nil {
		t.Fatalf("start tool: %v", err)
	}
	defer toolDone()
	_, _, hookDone, err := manager.Start(ctx, request.StartRequest{ParentID: turnReq.ID, SessionID: "s1", Kind: request.KindHook, Label: "gpt_image_draw_fullwidth"})
	if err != nil {
		t.Fatalf("start hook: %v", err)
	}
	defer hookDone()

	got := formatActiveRequests(ctx, Deps{Requests: manager, Store: store, Turns: turn.NewManager()}, manager.ListBySession("s1"))
	for _, want := range []string{"[1] chat turn request", "[1.1]", "[1.2]", "shell tool request", "gpt_image_draw_fullwidth hook request"} {
		if !strings.Contains(got, want) {
			t.Fatalf("active requests output missing %q:\n%s", want, got)
		}
	}
}

func TestStopCommandCancelsNumberedHookRequest(t *testing.T) {
	ctx := superadminTestCtx()
	manager := request.NewManager(time.Minute)
	turnReq, turnCtx, turnDone, err := manager.Start(ctx, request.StartRequest{SessionID: "s1", Kind: request.KindTurn, Label: "chat"})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	defer turnDone()
	_, hookCtx, _, err := manager.Start(turnCtx, request.StartRequest{ParentID: turnReq.ID, SessionID: "s1", Kind: request.KindHook, Label: "gpt_image_draw_fullwidth"})
	if err != nil {
		t.Fatalf("start hook: %v", err)
	}

	cmd := NewStop(Deps{Requests: manager, Turns: turn.NewManager()})
	if _, err := cmd.Handle(ctx, command.Request{Args: "1.1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCommandTestCanceled(t, hookCtx)
	select {
	case <-turnCtx.Done():
		t.Fatal("turn request was canceled by hook stop")
	default:
	}
}

func TestStopCommandCancelsNumberedChildRequest(t *testing.T) {
	ctx := superadminTestCtx()
	manager := request.NewManager(time.Minute)
	turnReq, turnCtx, turnDone, err := manager.Start(ctx, request.StartRequest{SessionID: "s1", Kind: request.KindTurn, Label: "chat"})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	defer turnDone()
	_, toolCtx, _, err := manager.Start(turnCtx, request.StartRequest{ParentID: turnReq.ID, SessionID: "s1", Kind: request.KindTool, Label: "shell"})
	if err != nil {
		t.Fatalf("start tool: %v", err)
	}

	cmd := NewStop(Deps{Requests: manager, Turns: turn.NewManager()})
	if _, err := cmd.Handle(ctx, command.Request{Args: "1.1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCommandTestCanceled(t, toolCtx)
	select {
	case <-turnCtx.Done():
		t.Fatal("turn request was canceled by child stop")
	default:
	}
}

func TestStopCommandCancelsNumberedTurnAndChildren(t *testing.T) {
	ctx := superadminTestCtx()
	manager := request.NewManager(time.Minute)
	turnReq, turnCtx, _, err := manager.Start(ctx, request.StartRequest{SessionID: "s1", Kind: request.KindTurn, Label: "chat"})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	_, toolCtx, _, err := manager.Start(turnCtx, request.StartRequest{ParentID: turnReq.ID, SessionID: "s1", Kind: request.KindTool, Label: "shell"})
	if err != nil {
		t.Fatalf("start tool: %v", err)
	}

	cmd := NewStop(Deps{Requests: manager, Turns: turn.NewManager()})
	if _, err := cmd.Handle(ctx, command.Request{Args: "1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCommandTestCanceled(t, turnCtx)
	assertCommandTestCanceled(t, toolCtx)
	if got := len(manager.List()); got != 0 {
		t.Fatalf("active requests = %d, want 0", got)
	}
}

func TestStopCommandCompletesRequestIDs(t *testing.T) {
	ctx := superadminTestCtx()
	manager := request.NewManager(0)
	started, _, done, err := manager.Start(ctx, request.StartRequest{SessionID: "s1", Kind: request.KindLLM, Label: "chat"})
	if err != nil {
		t.Fatalf("start request: %v", err)
	}
	defer done()

	cmd := NewStop(Deps{Requests: manager}).(command.Completer)
	prefix := started.ID[:8]
	got := cmd.Complete(ctx, command.CompletionRequest{Raw: "/stop " + prefix, Prefix: "/", Name: "stop", Args: prefix, Cursor: len("/stop ") + len(prefix)})
	if len(got) != 1 || got[0].Text != started.ID || got[0].Kind != "request_id" {
		t.Fatalf("Complete = %#v", got)
	}
}

func TestStopCommandRejectsOtherUsersRequest(t *testing.T) {
	ctx := regularTestCtx()
	store := newCommandTestStore(t)
	svc := session.NewService(store)
	scope := session.Scope{ActorID: "cli:u1", Platform: "cli", PlatformScopeID: "local"}
	otherScope := session.Scope{ActorID: "cli:u2", Platform: "cli", PlatformScopeID: "local"}
	if _, err := svc.Create(ctx, otherScope, session.CreateRequest{Title: "他人的会话"}); err != nil {
		t.Fatalf("create other session: %v", err)
	}
	other, err := svc.Create(ctx, otherScope, session.CreateRequest{Title: "他人的会话 2"})
	if err != nil {
		t.Fatalf("create other session: %v", err)
	}
	mine, err := svc.Create(ctx, scope, session.CreateRequest{Title: "我的会话"})
	if err != nil {
		t.Fatalf("create own session: %v", err)
	}

	manager := request.NewManager(time.Minute)
	otherReq, otherCtx, otherDone, err := manager.Start(ctx, request.StartRequest{SessionID: other.ID, Kind: request.KindTurn, Label: "chat"})
	if err != nil {
		t.Fatalf("start other turn: %v", err)
	}
	defer otherDone()
	_, mineCtx, mineDone, err := manager.Start(ctx, request.StartRequest{SessionID: mine.ID, Kind: request.KindTurn, Label: "chat"})
	if err != nil {
		t.Fatalf("start own turn: %v", err)
	}
	defer mineDone()

	deps := Deps{Sessions: svc, Requests: manager, Turns: turn.NewManager(), Scope: func(context.Context) session.Scope { return scope }}
	cmd := NewStop(deps)

	if result, err := cmd.Handle(ctx, command.Request{Args: otherReq.ID}); err != nil {
		t.Fatalf("Handle: %v", err)
	} else if !strings.HasPrefix(result.Content, "request not found") {
		t.Fatalf("stopping another user's request = %q, want request not found", result.Content)
	}
	select {
	case <-otherCtx.Done():
		t.Fatal("another user's request was canceled")
	default:
	}
	if len(manager.ListBySession(other.ID)) == 0 {
		t.Fatal("another user's request disappeared from the manager")
	}

	// The numbers of /requests are resolved inside the caller's own scope only,
	// so "1" addresses the caller's turn, not the other session's turn.
	if result, err := cmd.Handle(ctx, command.Request{Args: "1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	} else if !strings.Contains(result.Content, "stopped") {
		t.Fatalf("stopping own request = %q, want stopped", result.Content)
	}
	assertCommandTestCanceled(t, mineCtx)

	completions := cmd.(command.Completer).Complete(ctx, command.CompletionRequest{Raw: "/stop ", Prefix: "/", Name: "stop", Args: "", Cursor: len("/stop ")})
	for _, completion := range completions {
		if completion.Text == otherReq.ID {
			t.Fatalf("completion leaked another user's request %q", otherReq.ID)
		}
	}
}
