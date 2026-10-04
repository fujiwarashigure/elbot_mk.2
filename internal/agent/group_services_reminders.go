package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"elbot/internal/config"
	"elbot/internal/delivery"
	"elbot/internal/security"
	"elbot/internal/session"
)

const (
	minReminderDelay      = 10 * time.Second
	groupReminderPrefix   = "r"
	groupPollPrefix       = "p"
	groupSignupPrefix     = "s"
	groupServiceTickEvery = 30 * time.Second
)

func nextGroupServiceID(prefix string, ids []string) string {
	used := map[string]bool{}
	for _, id := range ids {
		used[strings.ToLower(strings.TrimSpace(id))] = true
	}
	for i := 1; ; i++ {
		id := prefix + strconv.Itoa(i)
		if !used[id] {
			return id
		}
	}
}

func parseReminderWhen(raw string, now time.Time) (time.Time, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return time.Time{}, fmt.Errorf("请指定提醒时间")
	}
	if strings.HasPrefix(raw, "in ") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "in "))
	}
	if due, ok := parseRelativeReminder(raw, now); ok {
		return due, nil
	}
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04", "01-02 15:04", "15:04:05", "15:04"}
	for _, layout := range layouts {
		var parsed time.Time
		var err error
		if layout == "15:04" || layout == "15:04:05" {
			parsed, err = time.ParseInLocation(layout, raw, time.Local)
			if err == nil {
				parsed = time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, time.Local)
				if !parsed.After(now) {
					parsed = parsed.Add(24 * time.Hour)
				}
			}
		} else if layout == "01-02 15:04" {
			parsed, err = time.ParseInLocation("2006-"+layout, fmt.Sprintf("%d-%s", now.Year(), raw), time.Local)
		} else {
			parsed, err = time.ParseInLocation(layout, raw, time.Local)
		}
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法识别提醒时间 %q，请使用 10m、1h30m、2d、15:04 或 YYYY-MM-DD HH:MM", raw)
}

func parseRelativeReminder(raw string, now time.Time) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	if strings.Contains(raw, "d") {
		parts := strings.SplitN(raw, "d", 2)
		days, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || days < 0 {
			return time.Time{}, false
		}
		duration := time.Duration(days) * 24 * time.Hour
		if rest := strings.TrimSpace(parts[1]); rest != "" {
			parsed, err := time.ParseDuration(rest)
			if err != nil {
				return time.Time{}, false
			}
			duration += parsed
		}
		return now.Add(duration), true
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return time.Time{}, false
	}
	return now.Add(parsed), true
}

func (a *Agent) ReminderCreate(ctx context.Context, whenText, text string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	cfg := a.groupServicesCfg.Normalized()
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("提醒内容不能为空")
	}
	if runeLen(text) > cfg.MaxTextRunes {
		return "", fmt.Errorf("提醒内容超过 %d 个字符", cfg.MaxTextRunes)
	}
	now := time.Now()
	dueAt, err := parseReminderWhen(whenText, now)
	if err != nil {
		return "", err
	}
	if dueAt.Before(now.Add(minReminderDelay)) {
		return "", fmt.Errorf("提醒时间至少要在 %s 之后", groupServiceDurationText(minReminderDelay))
	}
	if dueAt.After(now.Add(time.Duration(cfg.MaxReminderDays) * 24 * time.Hour)) {
		return "", fmt.Errorf("提醒时间不能超过 %d 天", cfg.MaxReminderDays)
	}
	scope := a.scope(ctx)
	actor := a.actor(ctx)
	var entry config.GroupReminderConfig
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupRemindersForScope(scope)
		pending := 0
		ids := make([]string, 0, len(entries))
		for _, existing := range entries {
			ids = append(ids, existing.ID)
			if existing.Status == "" || existing.Status == "pending" {
				pending++
			}
		}
		if pending >= cfg.MaxRemindersPerScope {
			return fmt.Errorf("当前群待发送提醒已达到 %d 条上限", cfg.MaxRemindersPerScope)
		}
		entry = config.GroupReminderConfig{
			ID:            nextGroupServiceID(groupReminderPrefix, ids),
			Text:          text,
			DueAt:         formatGroupServiceTime(dueAt),
			CreatedBy:     actor.ID,
			CreatedByName: firstNonEmpty(actor.GroupCard, actor.Nickname, actor.DisplayName),
			CreatedAt:     formatGroupServiceTime(now),
			Status:        "pending",
		}
		a.setGroupRemindersForScope(scope, append(entries, entry))
		return nil
	}); err != nil {
		return "", err
	}
	a.audit("group_reminder_create", "platform", scope.Platform, "scope", scope.PlatformScopeID, "actor_id", actor.ID, "reminder_id", entry.ID)
	return fmt.Sprintf("提醒已创建：%s（%s）\n%s", entry.ID, dueAt.Format("2006-01-02 15:04:05"), entry.Text), nil
}

func (a *Agent) ReminderList(ctx context.Context) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	entries := a.groupRemindersForScope(a.scope(ctx))
	pending := make([]config.GroupReminderConfig, 0, len(entries))
	for _, entry := range entries {
		if entry.Status == "" || entry.Status == "pending" {
			pending = append(pending, entry)
		}
	}
	if len(pending) == 0 {
		return "当前群没有待发送提醒。", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("当前群提醒（%d 条）：\n", len(pending)))
	for _, entry := range pending {
		due, _ := parseGroupServiceTime(entry.DueAt)
		sb.WriteString(fmt.Sprintf("- %s  %s  %s\n", entry.ID, due.Format("2006-01-02 15:04:05"), entry.Text))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func (a *Agent) ReminderRemove(ctx context.Context, id string) (string, error) {
	if err := a.requireGroupServices(ctx); err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("请指定要删除的提醒 id")
	}
	scope := a.scope(ctx)
	removed := false
	if err := a.withGroupServicesWrite(func() error {
		entries := a.groupRemindersForScope(scope)
		kept := make([]config.GroupReminderConfig, 0, len(entries))
		for _, entry := range entries {
			if !strings.EqualFold(strings.TrimSpace(entry.ID), id) {
				kept = append(kept, entry)
				continue
			}
			if !a.canManageGroupServiceEntry(ctx, entry.CreatedBy) {
				return fmt.Errorf("只有提醒创建者、当前群管理员或超级管理员可以删除")
			}
			removed = true
		}
		if removed {
			a.setGroupRemindersForScope(scope, kept)
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !removed {
		return fmt.Sprintf("没有找到提醒 %q", id), nil
	}
	return fmt.Sprintf("已删除提醒 %s", id), nil
}

func (a *Agent) canManageGroupServiceEntry(ctx context.Context, createdBy string) bool {
	actor := a.actor(ctx)
	if actor.Role == security.RoleSuperadmin {
		return true
	}
	if actor.GroupRole == security.GroupRoleOwner || actor.GroupRole == security.GroupRoleAdmin {
		return true
	}
	return strings.TrimSpace(createdBy) != "" && strings.TrimSpace(createdBy) == actor.ID
}

// StartGroupServices starts the reminder dispatcher. It is safe to call more
// than once and exits when ctx is done.
func (a *Agent) StartGroupServices(ctx context.Context) {
	if a == nil || !a.groupServicesCfg.IsEnabled() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.groupServicesMu.Lock()
	if a.groupServicesStarted {
		a.groupServicesMu.Unlock()
		return
	}
	a.groupServicesStarted = true
	a.groupServicesMu.Unlock()

	go func() {
		a.processDueReminders(ctx, time.Now())
		ticker := time.NewTicker(groupServiceTickEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				a.processDueReminders(ctx, now)
			}
		}
	}()
}

func (a *Agent) processDueReminders(ctx context.Context, now time.Time) {
	if a == nil || !a.groupServicesCfg.IsEnabled() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.groupServicesWriteMu.Lock()
	defer a.groupServicesWriteMu.Unlock()
	snapshot := a.groupServicesSnapshot()
	changed := false
	for key, entries := range snapshot.Reminders {
		platform, scopeID, ok := strings.Cut(key, ":")
		if !ok || strings.TrimSpace(platform) == "" || strings.TrimSpace(scopeID) == "" {
			continue
		}
		scope := session.Scope{Platform: platform, PlatformScopeID: scopeID}
		updated := append([]config.GroupReminderConfig(nil), entries...)
		for i, entry := range updated {
			if entry.Status != "" && entry.Status != "pending" {
				continue
			}
			dueAt, ok := parseGroupServiceTime(entry.DueAt)
			if !ok || dueAt.After(now) {
				continue
			}
			if a.groupRuntimeBlockedScope(scope) {
				state := normalizeGroupRuntimeState(a.groupRuntimeForScope(scope).State)
				if state == groupRuntimeRemoved || state == groupRuntimeUnavailable {
					updated[i].Status = "skipped"
					changed = true
					a.audit("group_reminder_skip", "platform", platform, "scope", scopeID, "reminder_id", entry.ID, "reason", state)
				}
				continue
			}
			if !a.groupPolicyForScope(scope).IsServicesEnabled() {
				continue
			}
			text := strings.TrimSpace(entry.Text)
			if text == "" {
				updated[i].Status = "skipped"
				changed = true
				continue
			}
			if entry.CreatedByName != "" {
				text = "提醒：" + text + "（由 " + entry.CreatedByName + " 创建）"
			} else {
				text = "提醒：" + text
			}
			_, err := a.SendNotice(ctx, delivery.Notice{Target: delivery.Target{Platform: platform, ScopeID: scopeID}, Outputs: []delivery.Output{delivery.Text(text)}})
			if err != nil {
				a.audit("group_reminder_send_error", "platform", platform, "scope", scopeID, "reminder_id", entry.ID, "error", err.Error())
				continue
			}
			updated[i].Status = "sent"
			changed = true
			a.audit("group_reminder_sent", "platform", platform, "scope", scopeID, "reminder_id", entry.ID)
		}
		snapshot.Reminders[key] = updated
	}
	if changed {
		a.setGroupServicesSnapshot(snapshot)
		if err := a.saveRuntimeState(); err != nil {
			a.audit("group_services_write_error", "kind", "reminder_dispatch", "error", err.Error())
		}
	}
}
