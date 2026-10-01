package qqonebot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"elbot/internal/platform"
)

// The methods in this file implement optional platform capabilities used by
// group analysis and learning. They are best-effort adapters over the OneBot
// action surface; deployments that do not expose these actions simply get an
// error and callers fall back to local chat history.

type oneBotHistoryMessage struct {
	Time       int64           `json:"time"`
	MessageID  json.RawMessage `json:"message_id"`
	UserID     json.RawMessage `json:"user_id"`
	Sender     Sender          `json:"sender"`
	RawMessage string          `json:"raw_message"`
	Message    json.RawMessage `json:"message"`
}

type oneBotHistoryResponse struct {
	Messages []oneBotHistoryMessage `json:"messages"`
}

func (a *Adapter) FetchGroupHistory(ctx context.Context, groupID string, since, until time.Time, limit int) ([]platform.HistoryMessage, error) {
	if a == nil || a.transport == nil {
		return nil, fmt.Errorf("qqonebot transport is not configured")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(groupID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse qqonebot group id: %w", err)
	}
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	resp, err := a.transport.Call(ctx, "get_group_msg_history", map[string]any{
		"group_id": id,
		"count":    limit,
	})
	if err != nil {
		return nil, err
	}
	var object oneBotHistoryResponse
	if err := json.Unmarshal(resp.Data, &object); err != nil {
		var direct []oneBotHistoryMessage
		if err := json.Unmarshal(resp.Data, &direct); err != nil {
			return nil, fmt.Errorf("decode get_group_msg_history response: %w", err)
		}
		object.Messages = direct
	}
	out := make([]platform.HistoryMessage, 0, len(object.Messages))
	for _, item := range object.Messages {
		createdAt := time.Now()
		if item.Time > 0 {
			createdAt = time.Unix(item.Time, 0)
		}
		if !since.IsZero() && createdAt.Before(since) {
			continue
		}
		if !until.IsZero() && createdAt.After(until) {
			continue
		}
		normalized := normalizeMessage(item.Message, item.RawMessage, 0)
		senderID := rawIDString(item.UserID)
		if senderID == "" {
			senderID = strconv.FormatInt(item.Sender.UserID, 10)
		}
		out = append(out, platform.HistoryMessage{
			PlatformMessageID: rawIDString(item.MessageID),
			SenderID:          senderID,
			SenderName:        displayName(item.Sender, item.Sender.UserID),
			Text:              normalized.Text,
			Segments:          append([]platform.MessageSegment(nil), normalized.Segments...),
			CreatedAt:         createdAt,
		})
	}
	return out, nil
}

type oneBotGroupInfo struct {
	GroupID     json.RawMessage `json:"group_id"`
	GroupName   string          `json:"group_name"`
	MemberCount int             `json:"member_count"`
}

func (a *Adapter) GetGroupInfo(ctx context.Context, groupID string) (platform.GroupInfo, error) {
	if a == nil || a.transport == nil {
		return platform.GroupInfo{}, fmt.Errorf("qqonebot transport is not configured")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(groupID), 10, 64)
	if err != nil {
		return platform.GroupInfo{}, fmt.Errorf("parse qqonebot group id: %w", err)
	}
	resp, err := a.transport.Call(ctx, "get_group_info", map[string]any{"group_id": id})
	if err != nil {
		return platform.GroupInfo{}, err
	}
	var data oneBotGroupInfo
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return platform.GroupInfo{}, fmt.Errorf("decode get_group_info response: %w", err)
	}
	resolvedID := rawIDString(data.GroupID)
	if resolvedID == "" {
		resolvedID = strconv.FormatInt(id, 10)
	}
	return platform.GroupInfo{ID: resolvedID, Name: strings.TrimSpace(data.GroupName), MemberCount: data.MemberCount}, nil
}

type oneBotGroupMember struct {
	UserID   json.RawMessage `json:"user_id"`
	Nickname string          `json:"nickname"`
	Card     string          `json:"card"`
	Role     string          `json:"role"`
}

func (a *Adapter) GetGroupMemberList(ctx context.Context, groupID string) ([]platform.GroupMember, error) {
	if a == nil || a.transport == nil {
		return nil, fmt.Errorf("qqonebot transport is not configured")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(groupID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse qqonebot group id: %w", err)
	}
	resp, err := a.transport.Call(ctx, "get_group_member_list", map[string]any{"group_id": id})
	if err != nil {
		return nil, err
	}
	var members []oneBotGroupMember
	if err := json.Unmarshal(resp.Data, &members); err != nil {
		return nil, fmt.Errorf("decode get_group_member_list response: %w", err)
	}
	out := make([]platform.GroupMember, 0, len(members))
	for _, member := range members {
		userID := rawIDString(member.UserID)
		if userID == "" {
			continue
		}
		card := strings.TrimSpace(member.Card)
		nickname := strings.TrimSpace(member.Nickname)
		display := card
		if display == "" {
			display = nickname
		}
		out = append(out, platform.GroupMember{
			UserID:      userID,
			Nickname:    nickname,
			GroupCard:   card,
			DisplayName: display,
			Role:        strings.TrimSpace(member.Role),
		})
	}
	return out, nil
}

func (a *Adapter) GetUserAvatarURL(_ context.Context, userID string, size int) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", fmt.Errorf("qqonebot user id is required")
	}
	if size <= 0 || size > 640 {
		size = 100
	}
	values := url.Values{}
	values.Set("b", "qq")
	values.Set("nk", userID)
	values.Set("s", strconv.Itoa(size))
	return "https://q1.qlogo.cn/g?" + values.Encode(), nil
}

func rawIDString(raw json.RawMessage) string {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strings.TrimSpace(number.String())
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}
