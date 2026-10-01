package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"elbot/internal/platform"
)

// Telegram implements the optional group-directory capability for chat info and
// administrators. Telegram Bot API cannot list every member nor fetch arbitrary
// history, so callers must keep local chat history as the fallback.

type telegramChatInfo struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Username    string `json:"username"`
	Description string `json:"description"`
	MemberCount int    `json:"member_count"`
}

func (a *Adapter) GetGroupInfo(ctx context.Context, groupID string) (platform.GroupInfo, error) {
	chatID, err := telegramChatID(groupID)
	if err != nil {
		return platform.GroupInfo{}, err
	}
	raw, err := a.CallPlatformAPI(ctx, "getChat", map[string]any{"chat_id": chatID})
	if err != nil {
		return platform.GroupInfo{}, err
	}
	var info telegramChatInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return platform.GroupInfo{}, fmt.Errorf("decode telegram getChat response: %w", err)
	}
	title := strings.TrimSpace(info.Title)
	if title == "" {
		title = strings.TrimSpace(info.Username)
	}
	return platform.GroupInfo{
		ID:          strconv.FormatInt(info.ID, 10),
		Name:        title,
		MemberCount: info.MemberCount,
	}, nil
}

type telegramChatMember struct {
	Status      string `json:"status"`
	CustomTitle string `json:"custom_title"`
	User        struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	} `json:"user"`
}

func (a *Adapter) GetGroupMemberList(ctx context.Context, groupID string) ([]platform.GroupMember, error) {
	chatID, err := telegramChatID(groupID)
	if err != nil {
		return nil, err
	}
	raw, err := a.CallPlatformAPI(ctx, "getChatAdministrators", map[string]any{"chat_id": chatID})
	if err != nil {
		return nil, err
	}
	var members []telegramChatMember
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, fmt.Errorf("decode telegram getChatAdministrators response: %w", err)
	}
	out := make([]platform.GroupMember, 0, len(members))
	for _, member := range members {
		if member.User.ID == 0 || member.User.IsBot {
			continue
		}
		display := strings.TrimSpace(strings.TrimSpace(member.User.FirstName + " " + member.User.LastName))
		if display == "" {
			display = strings.TrimSpace(member.User.Username)
		}
		role := strings.TrimSpace(member.Status)
		switch role {
		case "creator":
			role = "owner"
		case "administrator":
			role = "admin"
		}
		out = append(out, platform.GroupMember{
			UserID:      strconv.FormatInt(member.User.ID, 10),
			Nickname:    display,
			DisplayName: display,
			Role:        role,
		})
	}
	return out, nil
}

func telegramChatID(groupID string) (string, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return "", fmt.Errorf("telegram group id is required")
	}
	if _, err := strconv.ParseInt(groupID, 10, 64); err == nil {
		return groupID, nil
	}
	if _, value, ok := strings.Cut(groupID, ":"); ok {
		if _, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			return strings.TrimSpace(value), nil
		}
	}
	return "", fmt.Errorf("telegram group id %q is not numeric", groupID)
}
