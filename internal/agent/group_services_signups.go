package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/logging"
)

func (a *Agent) SignupCreate(ctx context.Context, title string, capacity int) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	cfg := a.groupServicesCfg.Normalized()
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("报名标题不能为空")
	}
	if runeLen(title) > cfg.MaxTextRunes {
		return "", fmt.Errorf("报名标题超过 %d 个字符", cfg.MaxTextRunes)
	}
	if capacity < 0 || capacity > cfg.MaxSignupCapacity {
		return "", fmt.Errorf("报名人数必须在 0-%d 之间（0 表示不限制）", cfg.MaxSignupCapacity)
	}
	scope := a.scope(ctx)
	actor := a.actor(ctx)
	var entry config.GroupSignupConfig
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupSignupsForScope(scope)
		open := 0
		ids := make([]string, 0, len(entries))
		for _, existing := range entries {
			ids = append(ids, existing.ID)
			if existing.Status == "" || existing.Status == "open" {
				open++
			}
		}
		if open >= cfg.MaxSignupsPerScope {
			return fmt.Errorf("当前群进行中的报名已达到 %d 个上限", cfg.MaxSignupsPerScope)
		}
		entry = config.GroupSignupConfig{
			ID:            nextGroupServiceID(groupSignupPrefix, ids),
			Title:         title,
			Capacity:      capacity,
			Participants:  map[string]string{},
			CreatedBy:     actor.ID,
			CreatedByName: firstNonEmpty(actor.GroupCard, actor.Nickname, actor.DisplayName),
			CreatedAt:     formatGroupServiceTime(time.Now()),
			Status:        "open",
		}
		a.setGroupSignupsForScope(scope, append(entries, entry))
		return nil
	}); err != nil {
		return "", err
	}
	a.audit("group_signup_create", "platform", scope.Platform, "scope", scope.PlatformScopeID, "actor_id", actor.ID, "signup_id", entry.ID, "result", logging.ResultSucceeded)
	return formatGroupSignup(entry, actor.ID), nil
}

func (a *Agent) SignupList(ctx context.Context) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	entries := a.groupSignupsForScope(a.scope(ctx))
	open := make([]config.GroupSignupConfig, 0, len(entries))
	for _, entry := range entries {
		if entry.Status == "" || entry.Status == "open" {
			open = append(open, entry)
		}
	}
	if len(open) == 0 {
		return "当前群没有进行中的报名。", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("当前群报名（%d 个）：\n", len(open)))
	for _, entry := range open {
		capacity := "不限"
		if entry.Capacity > 0 {
			capacity = fmt.Sprintf("%d", entry.Capacity)
		}
		sb.WriteString(fmt.Sprintf("- %s  %s（%d/%s）\n", entry.ID, entry.Title, len(entry.Participants), capacity))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func (a *Agent) SignupShow(ctx context.Context, id string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	entry, ok := a.findSignup(ctx, id)
	if !ok {
		return fmt.Sprintf("没有找到报名 %q", strings.TrimSpace(id)), nil
	}
	return formatGroupSignup(entry, a.actor(ctx).ID), nil
}

func (a *Agent) SignupJoin(ctx context.Context, id string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	scope := a.scope(ctx)
	actor := a.actor(ctx)
	found := false
	message := ""
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupSignupsForScope(scope)
		for i := range entries {
			if !strings.EqualFold(strings.TrimSpace(entries[i].ID), strings.TrimSpace(id)) {
				continue
			}
			if entries[i].Status != "" && entries[i].Status != "open" {
				return fmt.Errorf("报名 %s 已关闭", entries[i].ID)
			}
			if entries[i].Participants == nil {
				entries[i].Participants = map[string]string{}
			}
			if _, exists := entries[i].Participants[actor.ID]; !exists && entries[i].Capacity > 0 && len(entries[i].Participants) >= entries[i].Capacity {
				return fmt.Errorf("报名 %s 已满（%d/%d）", entries[i].ID, len(entries[i].Participants), entries[i].Capacity)
			}
			entries[i].Participants[actor.ID] = firstNonEmpty(actor.GroupCard, actor.Nickname, actor.DisplayName)
			message = fmt.Sprintf("已加入报名 %s：%s（%d/%s）", entries[i].ID, entries[i].Title, len(entries[i].Participants), formatSignupCapacity(entries[i].Capacity))
			found = true
			a.setGroupSignupsForScope(scope, entries)
			break
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("没有找到报名 %q", strings.TrimSpace(id)), nil
	}
	return message, nil
}

func (a *Agent) SignupLeave(ctx context.Context, id string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	scope := a.scope(ctx)
	actor := a.actor(ctx)
	found := false
	message := ""
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupSignupsForScope(scope)
		for i := range entries {
			if !strings.EqualFold(strings.TrimSpace(entries[i].ID), strings.TrimSpace(id)) {
				continue
			}
			if entries[i].Participants != nil {
				delete(entries[i].Participants, actor.ID)
			}
			message = fmt.Sprintf("已退出报名 %s：%s（%d/%s）", entries[i].ID, entries[i].Title, len(entries[i].Participants), formatSignupCapacity(entries[i].Capacity))
			found = true
			a.setGroupSignupsForScope(scope, entries)
			break
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("没有找到报名 %q", strings.TrimSpace(id)), nil
	}
	return message, nil
}

func (a *Agent) SignupClose(ctx context.Context, id string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	scope := a.scope(ctx)
	found := false
	var closed config.GroupSignupConfig
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupSignupsForScope(scope)
		for i := range entries {
			if !strings.EqualFold(strings.TrimSpace(entries[i].ID), strings.TrimSpace(id)) {
				continue
			}
			if !a.canManageGroupServiceEntry(ctx, entries[i].CreatedBy) {
				return fmt.Errorf("只有报名创建者、当前群管理员或超级管理员可以关闭")
			}
			entries[i].Status = "closed"
			closed = entries[i]
			found = true
			a.setGroupSignupsForScope(scope, entries)
			break
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("没有找到报名 %q", strings.TrimSpace(id)), nil
	}
	a.audit("group_signup_close", "platform", scope.Platform, "scope", scope.PlatformScopeID, "actor_id", a.actor(ctx).ID, "signup_id", closed.ID, "result", logging.ResultSucceeded)
	return formatGroupSignup(closed, a.actor(ctx).ID), nil
}

func (a *Agent) findSignup(ctx context.Context, id string) (config.GroupSignupConfig, bool) {
	id = strings.TrimSpace(id)
	for _, entry := range a.groupSignupsForScope(a.scope(ctx)) {
		if strings.EqualFold(strings.TrimSpace(entry.ID), id) {
			return entry, true
		}
	}
	return config.GroupSignupConfig{}, false
}

func formatGroupSignup(entry config.GroupSignupConfig, actorID string) string {
	status := entry.Status
	if status == "" {
		status = "open"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] %s（%s，%d/%s）\n", entry.ID, entry.Title, status, len(entry.Participants), formatSignupCapacity(entry.Capacity)))
	if len(entry.Participants) == 0 {
		sb.WriteString("  暂无成员加入")
		return strings.TrimRight(sb.String(), "\n")
	}
	names := make([]string, 0, len(entry.Participants))
	for _, name := range entry.Participants {
		names = append(names, name)
	}
	sort.Strings(names)
	sb.WriteString("  成员：")
	sb.WriteString(strings.Join(names, "、"))
	if actorID != "" {
		if _, ok := entry.Participants[actorID]; ok {
			sb.WriteString("\n  你已加入")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatSignupCapacity(capacity int) string {
	if capacity <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d", capacity)
}
