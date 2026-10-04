package agent

import (
	"context"
	"encoding/json"
	"strings"

	"elbot/internal/llm"
	"elbot/internal/turn"
)

// turnInputForMessage snapshots the current actor identity onto the turn
// input. The inbox uses Speaker.ActorID to keep same-actor messages mergeable
// and to prevent one member's permissions from being applied to another
// member's text.
func (a *Agent) turnInputForMessage(ctx context.Context, text string) turn.Input {
	input := inboundTurnInput(ctx, text)
	actor := a.actor(ctx)
	actorID := strings.TrimSpace(actor.ID)
	if actorID == "" {
		actorID = strings.TrimSpace(actor.PlatformUserID)
	}
	input.Speaker = turn.Speaker{
		ActorID: actorID,
		UserID:  strings.TrimSpace(actor.PlatformUserID),
		Name:    firstNonEmpty(actor.GroupCard, actor.Nickname, actor.DisplayName),
		Role:    string(actor.GroupRole),
	}
	return input
}

func speakerDisplayName(speaker turn.Speaker) string {
	name := strings.TrimSpace(speaker.Name)
	name = strings.Join(strings.Fields(name), " ")
	name = strings.ReplaceAll(name, "]", "）")
	name = strings.ReplaceAll(name, "[", "（")
	if name == "" {
		name = "未知成员"
	}
	userID := strings.TrimSpace(speaker.UserID)
	if userID == "" {
		return name
	}
	return name + "(id:" + userID + ")"
}

func uniqueInputSpeakers(input turn.Input) []turn.Speaker {
	seen := map[string]bool{}
	out := make([]turn.Speaker, 0, 2)
	add := func(speaker turn.Speaker) {
		if speaker.ActorID == "" && speaker.UserID == "" && speaker.Name == "" {
			return
		}
		key := speaker.ActorID + "\x00" + speaker.UserID + "\x00" + speaker.Name
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, speaker)
	}
	for _, part := range input.Parts {
		add(part.Speaker)
	}
	add(input.Speaker)
	return out
}

// attachSpeakerMarker prefixes a generated speaker line when the current
// group thread is shared. The line is server-generated; user text is never
// treated as an instruction by this helper.
func (a *Agent) attachSpeakerMarker(ctx context.Context, input turn.Input, segments []llm.MessageSegment) []llm.MessageSegment {
	if a == nil || !a.scope(ctx).Shared {
		return segments
	}
	speakers := uniqueInputSpeakers(input)
	if len(speakers) == 0 {
		return segments
	}
	names := make([]string, 0, len(speakers))
	for _, speaker := range speakers {
		names = append(names, speakerDisplayName(speaker))
	}
	marker := "[发言成员：" + strings.Join(names, "、") + "]"
	return llm.PrependSegmentText(segments, marker)
}

func inputSpeakerMetadata(input turn.Input) string {
	speakers := uniqueInputSpeakers(input)
	if len(speakers) == 0 {
		return ""
	}
	type speakerRecord struct {
		ActorID string `json:"actor_id,omitempty"`
		UserID  string `json:"user_id,omitempty"`
		Name    string `json:"name,omitempty"`
		Role    string `json:"role,omitempty"`
	}
	records := make([]speakerRecord, 0, len(speakers))
	for _, speaker := range speakers {
		records = append(records, speakerRecord{ActorID: speaker.ActorID, UserID: speaker.UserID, Name: speaker.Name, Role: speaker.Role})
	}
	data, err := json.Marshal(map[string]any{"speakers": records})
	if err != nil {
		return ""
	}
	return string(data)
}
