package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/session"
)

func cloneGroupReminders(input map[string][]config.GroupReminderConfig) map[string][]config.GroupReminderConfig {
	if len(input) == 0 {
		return map[string][]config.GroupReminderConfig{}
	}
	out := make(map[string][]config.GroupReminderConfig, len(input))
	for key, values := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = append([]config.GroupReminderConfig(nil), values...)
	}
	return out
}

func cloneGroupPolls(input map[string][]config.GroupPollConfig) map[string][]config.GroupPollConfig {
	if len(input) == 0 {
		return map[string][]config.GroupPollConfig{}
	}
	out := make(map[string][]config.GroupPollConfig, len(input))
	for key, values := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		copied := make([]config.GroupPollConfig, 0, len(values))
		for _, value := range values {
			value.Options = append([]string(nil), value.Options...)
			if value.Votes != nil {
				votes := make(map[string]int, len(value.Votes))
				for actor, option := range value.Votes {
					votes[actor] = option
				}
				value.Votes = votes
			}
			if value.Voters != nil {
				voters := make(map[string]string, len(value.Voters))
				for actor, name := range value.Voters {
					voters[actor] = name
				}
				value.Voters = voters
			}
			copied = append(copied, value)
		}
		out[key] = copied
	}
	return out
}

func cloneGroupSignups(input map[string][]config.GroupSignupConfig) map[string][]config.GroupSignupConfig {
	if len(input) == 0 {
		return map[string][]config.GroupSignupConfig{}
	}
	out := make(map[string][]config.GroupSignupConfig, len(input))
	for key, values := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		copied := make([]config.GroupSignupConfig, 0, len(values))
		for _, value := range values {
			if value.Participants != nil {
				participants := make(map[string]string, len(value.Participants))
				for actor, name := range value.Participants {
					participants[actor] = name
				}
				value.Participants = participants
			}
			copied = append(copied, value)
		}
		out[key] = copied
	}
	return out
}

func (a *Agent) setGroupServicesSnapshot(snapshot config.StateGroupServicesConfig) {
	if a == nil {
		return
	}
	a.groupServicesMu.Lock()
	a.groupReminders = cloneGroupReminders(snapshot.Reminders)
	a.groupPolls = cloneGroupPolls(snapshot.Polls)
	a.groupSignups = cloneGroupSignups(snapshot.Signups)
	a.groupServicesMu.Unlock()
}

func (a *Agent) groupServicesSnapshot() config.StateGroupServicesConfig {
	if a == nil {
		return config.StateGroupServicesConfig{}
	}
	a.groupServicesMu.RLock()
	defer a.groupServicesMu.RUnlock()
	return config.StateGroupServicesConfig{
		Reminders: cloneGroupReminders(a.groupReminders),
		Polls:     cloneGroupPolls(a.groupPolls),
		Signups:   cloneGroupSignups(a.groupSignups),
	}
}

func (a *Agent) groupRemindersForScope(scope session.Scope) []config.GroupReminderConfig {
	if a == nil {
		return nil
	}
	key := contextOverflowKey(scope)
	a.groupServicesMu.RLock()
	defer a.groupServicesMu.RUnlock()
	return append([]config.GroupReminderConfig(nil), a.groupReminders[key]...)
}

func (a *Agent) setGroupRemindersForScope(scope session.Scope, entries []config.GroupReminderConfig) {
	if a == nil {
		return
	}
	key := contextOverflowKey(scope)
	a.groupServicesMu.Lock()
	defer a.groupServicesMu.Unlock()
	if a.groupReminders == nil {
		a.groupReminders = map[string][]config.GroupReminderConfig{}
	}
	if len(entries) == 0 {
		delete(a.groupReminders, key)
		return
	}
	a.groupReminders[key] = append([]config.GroupReminderConfig(nil), entries...)
}

func (a *Agent) groupPollsForScope(scope session.Scope) []config.GroupPollConfig {
	if a == nil {
		return nil
	}
	key := contextOverflowKey(scope)
	a.groupServicesMu.RLock()
	defer a.groupServicesMu.RUnlock()
	return cloneGroupPolls(map[string][]config.GroupPollConfig{key: a.groupPolls[key]})[key]
}

func (a *Agent) setGroupPollsForScope(scope session.Scope, entries []config.GroupPollConfig) {
	if a == nil {
		return
	}
	key := contextOverflowKey(scope)
	a.groupServicesMu.Lock()
	defer a.groupServicesMu.Unlock()
	if a.groupPolls == nil {
		a.groupPolls = map[string][]config.GroupPollConfig{}
	}
	if len(entries) == 0 {
		delete(a.groupPolls, key)
		return
	}
	a.groupPolls[key] = cloneGroupPolls(map[string][]config.GroupPollConfig{key: entries})[key]
}

func (a *Agent) groupSignupsForScope(scope session.Scope) []config.GroupSignupConfig {
	if a == nil {
		return nil
	}
	key := contextOverflowKey(scope)
	a.groupServicesMu.RLock()
	defer a.groupServicesMu.RUnlock()
	return cloneGroupSignups(map[string][]config.GroupSignupConfig{key: a.groupSignups[key]})[key]
}

func (a *Agent) setGroupSignupsForScope(scope session.Scope, entries []config.GroupSignupConfig) {
	if a == nil {
		return
	}
	key := contextOverflowKey(scope)
	a.groupServicesMu.Lock()
	defer a.groupServicesMu.Unlock()
	if a.groupSignups == nil {
		a.groupSignups = map[string][]config.GroupSignupConfig{}
	}
	if len(entries) == 0 {
		delete(a.groupSignups, key)
		return
	}
	a.groupSignups[key] = cloneGroupSignups(map[string][]config.GroupSignupConfig{key: entries})[key]
}

func (a *Agent) groupServicesEnabledForScope(ctx context.Context) bool {
	if a == nil || !a.groupServicesCfg.IsEnabled() {
		return false
	}
	if !a.isGroupScope(ctx) {
		return false
	}
	return a.groupPolicyForScope(a.scope(ctx)).IsServicesEnabled()
}

func (a *Agent) requireGroupServices(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("agent is nil")
	}
	if !a.groupServicesCfg.IsEnabled() {
		return fmt.Errorf("群内提醒/投票/报名已全局关闭")
	}
	if !a.isGroupScope(ctx) {
		return fmt.Errorf("该功能只适用于群聊")
	}
	if !a.groupPolicyForScope(a.scope(ctx)).IsServicesEnabled() {
		return fmt.Errorf("当前群已关闭提醒/投票/报名")
	}
	return nil
}

func (a *Agent) withGroupServicesWrite(fn func() error) error {
	if a == nil {
		return fmt.Errorf("agent is nil")
	}
	a.groupServicesWriteMu.Lock()
	defer a.groupServicesWriteMu.Unlock()
	previous := a.groupServicesSnapshot()
	if err := fn(); err != nil {
		a.setGroupServicesSnapshot(previous)
		return err
	}
	if err := a.saveRuntimeState(); err != nil {
		a.setGroupServicesSnapshot(previous)
		return err
	}
	return nil
}

func normalizeGroupServicesSnapshot(input config.StateGroupServicesConfig, cfg config.GroupServicesConfig) config.StateGroupServicesConfig {
	cfg = cfg.Normalized()
	out := config.StateGroupServicesConfig{Reminders: map[string][]config.GroupReminderConfig{}, Polls: map[string][]config.GroupPollConfig{}, Signups: map[string][]config.GroupSignupConfig{}}
	now := time.Now()
	for key, values := range input.Reminders {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		clean := []config.GroupReminderConfig{}
		for _, value := range values {
			value.ID = strings.TrimSpace(value.ID)
			value.Text = truncateRunes(strings.TrimSpace(value.Text), cfg.MaxTextRunes)
			value.DueAt = strings.TrimSpace(value.DueAt)
			value.Status = strings.TrimSpace(value.Status)
			if value.ID == "" || value.Text == "" || value.DueAt == "" {
				continue
			}
			if value.Status == "sent" || value.Status == "skipped" {
				if at, ok := parseGroupServiceTime(value.DueAt); ok && now.Sub(at) > 24*time.Hour {
					continue
				}
			}
			clean = append(clean, value)
			if len(clean) >= cfg.MaxRemindersPerScope {
				break
			}
		}
		if len(clean) > 0 {
			out.Reminders[key] = clean
		}
	}
	for key, values := range input.Polls {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		clean := []config.GroupPollConfig{}
		for _, value := range values {
			value.ID = strings.TrimSpace(value.ID)
			value.Question = truncateRunes(strings.TrimSpace(value.Question), cfg.MaxTextRunes)
			value.Status = strings.TrimSpace(value.Status)
			if len(value.Options) > cfg.MaxPollOptions {
				value.Options = value.Options[:cfg.MaxPollOptions]
			}
			for i := range value.Options {
				value.Options[i] = truncateRunes(strings.TrimSpace(value.Options[i]), cfg.MaxTextRunes)
			}
			if value.ID == "" || value.Question == "" || len(value.Options) < 2 {
				continue
			}
			if value.Status == "closed" {
				if at, ok := parseGroupServiceTime(value.CreatedAt); ok && now.Sub(at) > 7*24*time.Hour {
					continue
				}
			}
			clean = append(clean, value)
			if len(clean) >= cfg.MaxPollsPerScope {
				break
			}
		}
		if len(clean) > 0 {
			out.Polls[key] = clean
		}
	}
	for key, values := range input.Signups {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		clean := []config.GroupSignupConfig{}
		for _, value := range values {
			value.ID = strings.TrimSpace(value.ID)
			value.Title = truncateRunes(strings.TrimSpace(value.Title), cfg.MaxTextRunes)
			value.Status = strings.TrimSpace(value.Status)
			if value.Capacity < 0 {
				value.Capacity = 0
			}
			if value.Capacity > cfg.MaxSignupCapacity {
				value.Capacity = cfg.MaxSignupCapacity
			}
			if value.ID == "" || value.Title == "" {
				continue
			}
			if value.Status == "closed" {
				if at, ok := parseGroupServiceTime(value.CreatedAt); ok && now.Sub(at) > 7*24*time.Hour {
					continue
				}
			}
			clean = append(clean, value)
			if len(clean) >= cfg.MaxSignupsPerScope {
				break
			}
		}
		if len(clean) > 0 {
			out.Signups[key] = clean
		}
	}
	return out
}

func parseGroupServiceTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func formatGroupServiceTime(value time.Time) string {
	return value.Format(time.RFC3339)
}

func groupServiceDurationText(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	return value.Round(time.Second).String()
}
