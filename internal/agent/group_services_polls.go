package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"elbot/internal/config"
)

func (a *Agent) PollCreate(ctx context.Context, question string, options []string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	cfg := a.groupServicesCfg.Normalized()
	question = strings.TrimSpace(question)
	if question == "" {
		return "", fmt.Errorf("投票问题不能为空")
	}
	if runeLen(question) > cfg.MaxTextRunes {
		return "", fmt.Errorf("投票问题超过 %d 个字符", cfg.MaxTextRunes)
	}
	clean := make([]string, 0, len(options))
	seen := map[string]bool{}
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		if runeLen(option) > cfg.MaxTextRunes {
			return "", fmt.Errorf("投票选项超过 %d 个字符", cfg.MaxTextRunes)
		}
		key := strings.ToLower(option)
		if seen[key] {
			continue
		}
		seen[key] = true
		clean = append(clean, option)
	}
	if len(clean) < 2 {
		return "", fmt.Errorf("投票至少需要两个选项")
	}
	if len(clean) > cfg.MaxPollOptions {
		return "", fmt.Errorf("投票最多 %d 个选项", cfg.MaxPollOptions)
	}
	scope := a.scope(ctx)
	actor := a.actor(ctx)
	var entry config.GroupPollConfig
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupPollsForScope(scope)
		open := 0
		ids := make([]string, 0, len(entries))
		for _, existing := range entries {
			ids = append(ids, existing.ID)
			if existing.Status == "" || existing.Status == "open" {
				open++
			}
		}
		if open >= cfg.MaxPollsPerScope {
			return fmt.Errorf("当前群进行中的投票已达到 %d 个上限", cfg.MaxPollsPerScope)
		}
		entry = config.GroupPollConfig{
			ID:            nextGroupServiceID(groupPollPrefix, ids),
			Question:      question,
			Options:       clean,
			Votes:         map[string]int{},
			Voters:        map[string]string{},
			CreatedBy:     actor.ID,
			CreatedByName: firstNonEmpty(actor.GroupCard, actor.Nickname, actor.DisplayName),
			CreatedAt:     formatGroupServiceTime(time.Now()),
			Status:        "open",
		}
		a.setGroupPollsForScope(scope, append(entries, entry))
		return nil
	}); err != nil {
		return "", err
	}
	a.audit("group_poll_create", "platform", scope.Platform, "scope", scope.PlatformScopeID, "actor_id", actor.ID, "poll_id", entry.ID)
	return formatGroupPoll(entry, ""), nil
}

func (a *Agent) PollList(ctx context.Context) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	entries := a.groupPollsForScope(a.scope(ctx))
	open := make([]config.GroupPollConfig, 0, len(entries))
	for _, entry := range entries {
		if entry.Status == "" || entry.Status == "open" {
			open = append(open, entry)
		}
	}
	if len(open) == 0 {
		return "当前群没有进行中的投票。", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("当前群投票（%d 个）：\n", len(open)))
	for _, entry := range open {
		sb.WriteString(fmt.Sprintf("- %s  %s（%d 票）\n", entry.ID, entry.Question, len(entry.Votes)))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func (a *Agent) PollShow(ctx context.Context, id string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	entry, ok := a.findPoll(ctx, id)
	if !ok {
		return fmt.Sprintf("没有找到投票 %q", strings.TrimSpace(id)), nil
	}
	return formatGroupPoll(entry, a.actor(ctx).ID), nil
}

func (a *Agent) PollVote(ctx context.Context, id, optionText string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	optionText = strings.TrimSpace(optionText)
	if id == "" || optionText == "" {
		return "", fmt.Errorf("用法：/vote <投票id> <选项序号>")
	}
	index, err := strconv.Atoi(optionText)
	if err != nil || index < 1 {
		return "", fmt.Errorf("选项序号必须是正整数")
	}
	scope := a.scope(ctx)
	actor := a.actor(ctx)
	found := false
	optionName := ""
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupPollsForScope(scope)
		for i := range entries {
			if !strings.EqualFold(strings.TrimSpace(entries[i].ID), id) {
				continue
			}
			if entries[i].Status != "" && entries[i].Status != "open" {
				return fmt.Errorf("投票 %s 已关闭", id)
			}
			if index > len(entries[i].Options) {
				return fmt.Errorf("选项序号超出范围（1-%d）", len(entries[i].Options))
			}
			if entries[i].Votes == nil {
				entries[i].Votes = map[string]int{}
			}
			if entries[i].Voters == nil {
				entries[i].Voters = map[string]string{}
			}
			entries[i].Votes[actor.ID] = index
			entries[i].Voters[actor.ID] = firstNonEmpty(actor.GroupCard, actor.Nickname, actor.DisplayName)
			optionName = entries[i].Options[index-1]
			found = true
			break
		}
		if found {
			a.setGroupPollsForScope(scope, entries)
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("没有找到投票 %q", id), nil
	}
	return fmt.Sprintf("已投票：%s 选项 %d %s", id, index, optionName), nil
}

func (a *Agent) PollClose(ctx context.Context, id string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	scope := a.scope(ctx)
	found := false
	var closed config.GroupPollConfig
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupPollsForScope(scope)
		for i := range entries {
			if !strings.EqualFold(strings.TrimSpace(entries[i].ID), strings.TrimSpace(id)) {
				continue
			}
			if !a.canManageGroupServiceEntry(ctx, entries[i].CreatedBy) {
				return fmt.Errorf("只有投票创建者、当前群管理员或超级管理员可以关闭")
			}
			entries[i].Status = "closed"
			closed = entries[i]
			found = true
			a.setGroupPollsForScope(scope, entries)
			break
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("没有找到投票 %q", strings.TrimSpace(id)), nil
	}
	a.audit("group_poll_close", "platform", scope.Platform, "scope", scope.PlatformScopeID, "actor_id", a.actor(ctx).ID, "poll_id", closed.ID)
	return formatGroupPoll(closed, a.actor(ctx).ID), nil
}

func (a *Agent) findPoll(ctx context.Context, id string) (config.GroupPollConfig, bool) {
	id = strings.TrimSpace(id)
	for _, entry := range a.groupPollsForScope(a.scope(ctx)) {
		if strings.EqualFold(strings.TrimSpace(entry.ID), id) {
			return entry, true
		}
	}
	return config.GroupPollConfig{}, false
}

func formatGroupPoll(entry config.GroupPollConfig, actorID string) string {
	var sb strings.Builder
	status := entry.Status
	if status == "" {
		status = "open"
	}
	sb.WriteString(fmt.Sprintf("[%s] %s（%s）\n", entry.ID, entry.Question, status))
	counts := make([]int, len(entry.Options))
	for actor, option := range entry.Votes {
		if option >= 1 && option <= len(counts) {
			counts[option-1]++
		}
		if actorID != "" && actor == actorID {
			sb.WriteString(fmt.Sprintf("你的选择：%d %s\n", option, entry.Options[option-1]))
		}
	}
	for i, option := range entry.Options {
		sb.WriteString(fmt.Sprintf("  %d. %s - %d 票\n", i+1, option, counts[i]))
	}
	sb.WriteString(fmt.Sprintf("共 %d 票", len(entry.Votes)))
	return strings.TrimRight(sb.String(), "\n")
}
