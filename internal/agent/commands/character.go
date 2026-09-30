package commands

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/character"
	"elbot/internal/command"
	"elbot/internal/security"
)

func NewCharacters(deps Deps) command.Handler {
	return charactersCommand{deps: deps}
}

type charactersCommand struct {
	deps Deps
}

func (c charactersCommand) Info() command.Info {
	return command.Info{
		Name:        "chars",
		Usage:       "/chars [query|reload]",
		Description: "List the character library. Use @char:<id> in a message to apply a character for one turn.",
		MinRole:     security.RoleUser,
	}
}

func (c charactersCommand) Handle(ctx context.Context, req command.Request) (*command.Result, error) {
	store := c.deps.Characters
	if store == nil || !store.Enabled() {
		return &command.Result{Content: "角色素材库未配置。"}, nil
	}
	arg := strings.TrimSpace(req.Args)
	if arg == "reload" {
		actor, _ := security.ActorFromContext(ctx)
		if actor.Role != security.RoleSuperadmin {
			return &command.Result{Content: "只有超级管理员可以重建角色索引。"}, nil
		}
		if err := store.Refresh(ctx); err != nil {
			return nil, err
		}
		return &command.Result{Content: "角色索引已重建。"}, nil
	}
	items, err := store.List(ctx, characterViewerFromActor(ctx))
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(arg)
	filtered := make([]*character.Character, 0, len(items))
	for _, item := range items {
		if query != "" && !characterMatchesQuery(item, query) {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return &command.Result{Content: "没有可用角色。把角色目录放到 data/config/elbot/characters/<id>/ 后发送 " + req.Prefix + "chars reload 刷新。"}, nil
	}
	lines := []string{fmt.Sprintf("角色：共 %d 个可见。", len(filtered))}
	for i, item := range filtered {
		if i >= 50 {
			lines = append(lines, fmt.Sprintf("...[已截断，还有 %d 个]", len(filtered)-50))
			break
		}
		line := "- " + item.ID + " | " + item.Name
		if len(item.Aliases) > 0 {
			line += " | aliases=" + strings.Join(item.Aliases, ",")
		}
		if len(item.Tags) > 0 {
			line += " | tags=" + strings.Join(item.Tags, ",")
		}
		line += " | " + string(item.Visibility)
		if item.Description != "" {
			line += " | " + item.Description
		}
		lines = append(lines, line)
	}
	lines = append(lines, "发送 @char:<id> 可在本轮启用某个角色。")
	return &command.Result{Content: strings.Join(lines, "\n")}, nil
}

func characterViewerFromActor(ctx context.Context) character.Viewer {
	actor, _ := security.ActorFromContext(ctx)
	return character.Viewer{Platform: actor.Platform, ActorID: actor.ID, Superadmin: actor.Role == security.RoleSuperadmin}
}

func characterMatchesQuery(item *character.Character, query string) bool {
	fields := []string{item.ID, item.Name, item.Description, strings.Join(item.Aliases, " "), strings.Join(item.Tags, " ")}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

type CharacterModule struct{}

func (CharacterModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps, NewCharacters)
}
