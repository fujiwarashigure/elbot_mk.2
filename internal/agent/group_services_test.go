package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elbot/internal/config"
)

func TestReminderCreateListRemoveAndDispatch(t *testing.T) {
	adapter := &fakePlatform{}
	a := New(adapter, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := memberPanelTestContext()

	content, err := a.ReminderCreate(ctx, "10m", "开会")
	if err != nil {
		t.Fatalf("ReminderCreate: %v", err)
	}
	if !strings.Contains(content, "r1") {
		t.Fatalf("create output = %q", content)
	}
	list, err := a.ReminderList(ctx)
	if err != nil {
		t.Fatalf("ReminderList: %v", err)
	}
	if !strings.Contains(list, "开会") || !strings.Contains(list, "r1") {
		t.Fatalf("list output = %q", list)
	}

	scope := a.scope(ctx)
	entries := a.groupRemindersForScope(scope)
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	entries[0].DueAt = formatGroupServiceTime(time.Now().Add(-time.Minute))
	a.setGroupRemindersForScope(scope, entries)
	a.processDueReminders(ctx, time.Now())
	if !strings.Contains(adapter.out.String(), "提醒：开会") {
		t.Fatalf("dispatch output = %q", adapter.out.String())
	}
	if got := a.groupRemindersForScope(scope)[0].Status; got != "sent" {
		t.Fatalf("reminder status = %q, want sent", got)
	}

	if _, err := a.ReminderRemove(ctx, "missing"); err != nil {
		t.Fatalf("remove missing: %v", err)
	}
	if _, err := a.ReminderRemove(ctx, "r1"); err != nil {
		t.Fatalf("remove r1: %v", err)
	}
	if entries := a.groupRemindersForScope(scope); len(entries) != 0 {
		t.Fatalf("reminders after remove = %#v", entries)
	}
}

func TestReminderTimeParsing(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"10m", 10 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"2d", 48 * time.Hour},
		{"2d3h", 51 * time.Hour},
	} {
		got, err := parseReminderWhen(tc.raw, now)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if got.Sub(now) != tc.want {
			t.Fatalf("parse %q = %s, want %s", tc.raw, got.Sub(now), tc.want)
		}
	}
	got, err := parseReminderWhen("23:59", now)
	if err != nil {
		t.Fatalf("parse clock: %v", err)
	}
	if !got.After(now) {
		t.Fatalf("clock parse = %s, want after now", got)
	}
}

func TestPollCreateVoteShowClose(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := memberPanelTestContext()

	created, err := a.PollCreate(ctx, "周末去哪", []string{"爬山", "桌游", "聚餐"})
	if err != nil {
		t.Fatalf("PollCreate: %v", err)
	}
	if !strings.Contains(created, "p1") {
		t.Fatalf("create output = %q", created)
	}
	if _, err := a.PollVote(ctx, "p1", "2"); err != nil {
		t.Fatalf("PollVote: %v", err)
	}
	shown, err := a.PollShow(ctx, "p1")
	if err != nil {
		t.Fatalf("PollShow: %v", err)
	}
	if !strings.Contains(shown, "桌游 - 1 票") || !strings.Contains(shown, "你的选择") {
		t.Fatalf("show output = %q", shown)
	}
	closed, err := a.PollClose(ctx, "p1")
	if err != nil {
		t.Fatalf("PollClose: %v", err)
	}
	if !strings.Contains(closed, "closed") {
		t.Fatalf("close output = %q", closed)
	}
	if _, err := a.PollVote(ctx, "p1", "1"); err == nil {
		t.Fatal("vote after close must fail")
	}
}

func TestSignupCreateJoinLeaveClose(t *testing.T) {
	a := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := memberPanelTestContext()

	if _, err := a.SignupCreate(ctx, "团建", 2); err != nil {
		t.Fatalf("SignupCreate: %v", err)
	}
	joined, err := a.SignupJoin(ctx, "s1")
	if err != nil {
		t.Fatalf("SignupJoin: %v", err)
	}
	if !strings.Contains(joined, "1/2") {
		t.Fatalf("join output = %q", joined)
	}
	shown, err := a.SignupShow(ctx, "s1")
	if err != nil {
		t.Fatalf("SignupShow: %v", err)
	}
	if !strings.Contains(shown, "你已加入") {
		t.Fatalf("show output = %q", shown)
	}
	left, err := a.SignupLeave(ctx, "s1")
	if err != nil {
		t.Fatalf("SignupLeave: %v", err)
	}
	if !strings.Contains(left, "0/2") {
		t.Fatalf("leave output = %q", left)
	}
	closed, err := a.SignupClose(ctx, "s1")
	if err != nil {
		t.Fatalf("SignupClose: %v", err)
	}
	if !strings.Contains(closed, "closed") {
		t.Fatalf("close output = %q", closed)
	}
}

func TestGroupServicesCommandIntegration(t *testing.T) {
	adapter := &fakePlatform{}
	a := New(adapter, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	a.statePath = filepath.Join(t.TempDir(), "state.toml")
	ctx := memberPanelTestContext()

	if err := a.HandleMessage(ctx, "/remind 10m 开会"); err != nil {
		t.Fatalf("HandleMessage /remind: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "提醒已创建") {
		t.Fatalf("remind output = %q", adapter.out.String())
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "/poll 午饭吃什么 | 面 | 饭"); err != nil {
		t.Fatalf("HandleMessage /poll: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "p1") {
		t.Fatalf("poll output = %q", adapter.out.String())
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "/vote p1 2"); err != nil {
		t.Fatalf("HandleMessage /vote: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "已投票") {
		t.Fatalf("vote output = %q", adapter.out.String())
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "/signup 团建 2"); err != nil {
		t.Fatalf("HandleMessage /signup: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "s1") {
		t.Fatalf("signup output = %q", adapter.out.String())
	}
	adapter.out.Reset()
	if err := a.HandleMessage(ctx, "/join s1"); err != nil {
		t.Fatalf("HandleMessage /join: %v", err)
	}
	if !strings.Contains(adapter.out.String(), "1/2") {
		t.Fatalf("join output = %q", adapter.out.String())
	}
}

func TestGroupServicesPersistAndRespectSwitches(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.toml")
	ctx := memberPanelTestContext()
	first := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	first.statePath = statePath
	if _, err := first.PollCreate(ctx, "午饭", []string{"面", "饭"}); err != nil {
		t.Fatalf("PollCreate: %v", err)
	}

	second := New(&fakePlatform{}, &fakeLLM{}, "test-model", config.ProviderConfig{}, newTestStore(t))
	second.statePath = statePath
	second.loadRuntimeStateAtStartup()
	list, err := second.PollList(ctx)
	if err != nil {
		t.Fatalf("PollList after reload: %v", err)
	}
	if !strings.Contains(list, "午饭") {
		t.Fatalf("reloaded poll list = %q", list)
	}

	disabled := false
	first.groupServicesCfg.Enabled = &disabled
	if _, err := first.SignupCreate(ctx, "x", 0); err == nil {
		t.Fatal("global disabled must reject signup")
	}
	first.groupServicesCfg.Enabled = nil
	first.setGroupPolicyForScope(first.scope(ctx), config.GroupPolicyConfig{Services: testBoolPtr(false)})
	if _, err := first.ReminderCreate(ctx, "10m", "x"); err == nil {
		t.Fatal("group policy services off must reject reminder")
	}
}
