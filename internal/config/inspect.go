package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// UnknownAppConfigKeys lists the app.toml keys that no known field consumes.
// A typo such as [provders.openai] or an obsolete key is otherwise ignored
// silently at startup.
func UnknownAppConfigKeys(configPath string) ([]string, error) {
	path, err := ResolvePath(configPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	var target any = newLoadConfig()
	if err := decoder.Decode(target); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			keys := make([]string, 0, len(strict.Errors))
			for _, item := range strict.Errors {
				keys = append(keys, strings.Join(item.Key(), "."))
			}
			sort.Strings(keys)
			return keys, nil
		}
		return nil, fmt.Errorf("inspect config %q: %w", path, err)
	}
	return nil, nil
}

// BuiltinAssetDrift compares the built-in skill files on disk with the embedded
// templates. Only skills are compared: app.toml and services.toml are meant to
// be edited locally, while a built-in AgentSkill that drifted from the shipped
// version usually means a stale or hand-edited copy.
func BuiltinAssetDrift(configDir string) []string {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return nil
	}
	drift := []string{}
	for _, asset := range defaultConfigAssets {
		if !strings.HasPrefix(filepath.ToSlash(asset.Path), "skills/") {
			continue
		}
		path := filepath.Join(configDir, filepath.FromSlash(asset.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				drift = append(drift, "缺失内置 Skill 文件 "+asset.Path)
				continue
			}
			drift = append(drift, "无法读取内置 Skill 文件 "+asset.Path+"："+err.Error())
			continue
		}
		if string(data) != asset.Content {
			drift = append(drift, "内置 Skill 文件与默认版本不一致 "+asset.Path)
		}
	}
	sort.Strings(drift)
	return drift
}
