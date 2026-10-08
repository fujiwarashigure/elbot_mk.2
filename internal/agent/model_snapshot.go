package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	agentcommands "elbot/internal/agent/commands"
	"elbot/internal/config"
	"elbot/internal/storage"
)

// modelSnapshotNameLimit 是快照名长度上限（按 rune 计）。名字会作为 state.toml 的 TOML 键
// 落盘，也会出现在命令回复里，所以既限制长度也限制字符集。
const modelSnapshotNameLimit = 32

// modelSnapshotShadowErr 说明为什么快照名不能与静态模型 profile 重名：
// `@model:<名字>` 与群策略的 default-model 走静态 profile 解析，`/model --apply` 走快照，
// 两者同名时同一个名字会有两种含义。
var errModelSnapshotNameEmpty = errors.New("快照名不能为空")

// modelSnapshotModeOrder 固定应用快照时的顺序。applyModeModelSelection 会把最后一次调用的
// 模型记为进程的主 provider/providerName，随机顺序会让结果不确定；work 是默认模式，因此放在
// 最后应用。其余未列出的模式按名字排序跟在后面。
var modelSnapshotModeOrder = []string{storage.SessionModeChat, "elwisp1", "elwisp2", "elwisp3", storage.SessionModeWork}

// ModelSnapshots 列出已保存的命名模型快照，按名字排序。
func (a *Agent) ModelSnapshots() []agentcommands.ModelSnapshot {
	if a == nil {
		return nil
	}
	a.modelSnapshotsMu.RLock()
	out := make([]agentcommands.ModelSnapshot, 0, len(a.modelSnapshots))
	for name, snapshot := range a.modelSnapshots {
		out = append(out, modelSnapshotView(name, snapshot))
	}
	a.modelSnapshotsMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SaveModelSnapshot 把当前的模型选择（各模式 + 压缩 + 命名）保存为命名快照。
func (a *Agent) SaveModelSnapshot(name string) error {
	if a == nil {
		return errors.New("agent unavailable")
	}
	key, err := normalizeModelSnapshotName(name)
	if err != nil {
		return err
	}
	if a.modelProfileNameExists(key) {
		return fmt.Errorf("名字 %q 已经是 services.toml 里的模型 profile/别名；快照名要与 @model:<名字> 用的名字区分开", key)
	}
	snapshot := config.StateModelSnapshot{
		ModeModels:   a.modeModelsSnapshot(),
		CompactModel: a.contextRuntime.configuredCompactModel(),
		NamingModel:  a.configuredNamingModel(),
	}
	if len(snapshot.ModeModels) == 0 {
		return errors.New("当前没有可保存的模型选择")
	}
	// 先吸收外部编辑再改内存：反过来会把刚保存的快照丢掉（见 mergeExternalRuntimeState）。
	a.mergeExternalRuntimeState()
	a.modelSnapshotsMu.Lock()
	previous, existed := a.modelSnapshots[key]
	if a.modelSnapshots == nil {
		a.modelSnapshots = map[string]config.StateModelSnapshot{}
	}
	a.modelSnapshots[key] = snapshot
	a.modelSnapshotsMu.Unlock()
	if a.statePath == "" {
		return nil
	}
	if err := a.saveRuntimeState(); err != nil {
		// 内存里已经写进去了，但文件里没有：回滚内存，避免"命令报失败、重启后又消失"
		// 或者"命令报失败、本次进程里却已生效"这种两头不一致。
		a.modelSnapshotsMu.Lock()
		if existed {
			a.modelSnapshots[key] = previous
		} else {
			delete(a.modelSnapshots, key)
		}
		a.modelSnapshotsMu.Unlock()
		return err
	}
	return nil
}

// ApplyModelSnapshot 把命名快照里的模型选择整体切回去。任何 provider 在当前进程里不存在
// 时整条拒绝、不做部分应用——半个模型集合比不改更糟。
func (a *Agent) ApplyModelSnapshot(name string) (agentcommands.ModelSnapshot, error) {
	if a == nil {
		return agentcommands.ModelSnapshot{}, errors.New("agent unavailable")
	}
	key, err := normalizeModelSnapshotName(name)
	if err != nil {
		return agentcommands.ModelSnapshot{}, err
	}
	// 先合并外部编辑：否则用内存里已经过期的快照去改模型，紧接着 saveRuntimeState 又会把
	// 内存覆盖回文件内容，落盘的是"没应用"的状态，命令却报成功。
	a.mergeExternalRuntimeState()
	a.modelSnapshotsMu.RLock()
	snapshot, ok := a.modelSnapshots[key]
	a.modelSnapshotsMu.RUnlock()
	if !ok {
		if a.modelProfileNameExists(key) {
			return agentcommands.ModelSnapshot{}, fmt.Errorf("%q 是静态模型 profile，不是快照：它只固定一个模型，用 `/model --profiles` 查看，或先用 `/model --save %s` 把当前选择存成快照", key, key)
		}
		return agentcommands.ModelSnapshot{}, fmt.Errorf("没有名为 %q 的模型快照；用 `/model --snapshots` 查看已保存的快照", key)
	}
	// 先整体校验 provider，再动手：半个模型集合比不改更糟。
	modes := orderedModelSnapshotModes(snapshot.ModeModels)
	for _, mode := range modes {
		selection := snapshot.ModeModels[mode]
		if _, ok := a.modelRuntime.providers[selection.Provider]; !ok {
			return agentcommands.ModelSnapshot{}, fmt.Errorf("快照 %q 的 %s 指向未知 provider %q", key, mode, selection.Provider)
		}
	}
	if snapshot.CompactModel.Provider != "" {
		if _, ok := a.modelRuntime.providers[snapshot.CompactModel.Provider]; !ok {
			return agentcommands.ModelSnapshot{}, fmt.Errorf("快照 %q 的 compact 指向未知 provider %q", key, snapshot.CompactModel.Provider)
		}
	}
	if snapshot.NamingModel.Provider != "" {
		if _, ok := a.modelRuntime.providers[snapshot.NamingModel.Provider]; !ok {
			return agentcommands.ModelSnapshot{}, fmt.Errorf("快照 %q 的 naming 指向未知 provider %q", key, snapshot.NamingModel.Provider)
		}
	}
	for _, mode := range modes {
		selection := snapshot.ModeModels[mode]
		if err := a.applyModeModelSelection(mode, selection.Provider, selection.Model); err != nil {
			return agentcommands.ModelSnapshot{}, err
		}
	}
	if snapshot.CompactModel.Provider != "" && snapshot.CompactModel.Model != "" {
		a.contextRuntime.setCompactModel(snapshot.CompactModel)
	}
	if snapshot.NamingModel.Provider != "" && snapshot.NamingModel.Model != "" {
		a.setNamingModel(snapshot.NamingModel)
		if a.titleGen != nil {
			a.titleGen.setNaming(a.clientForProvider(snapshot.NamingModel.Provider), snapshot.NamingModel.Provider, snapshot.NamingModel.Model)
		}
	}
	if a.statePath != "" {
		if err := a.saveRuntimeState(); err != nil {
			return agentcommands.ModelSnapshot{}, err
		}
	}
	return modelSnapshotView(key, snapshot), nil
}

// DeleteModelSnapshot 删除命名快照。删除不存在的名字不算错误，调用方可以据此报"本来就没有"。
func (a *Agent) DeleteModelSnapshot(name string) error {
	if a == nil {
		return errors.New("agent unavailable")
	}
	key, err := normalizeModelSnapshotName(name)
	if err != nil {
		return err
	}
	a.mergeExternalRuntimeState()
	a.modelSnapshotsMu.Lock()
	_, existed := a.modelSnapshots[key]
	delete(a.modelSnapshots, key)
	a.modelSnapshotsMu.Unlock()
	if !existed {
		return fmt.Errorf("没有名为 %q 的模型快照", key)
	}
	if a.statePath == "" {
		return nil
	}
	return a.saveRuntimeState()
}

// modelSnapshotsSnapshot 返回内存副本，供 saveRuntimeState 落盘。
func (a *Agent) modelSnapshotsSnapshot() map[string]config.StateModelSnapshot {
	a.modelSnapshotsMu.RLock()
	defer a.modelSnapshotsMu.RUnlock()
	if len(a.modelSnapshots) == 0 {
		return nil
	}
	out := make(map[string]config.StateModelSnapshot, len(a.modelSnapshots))
	for name, snapshot := range a.modelSnapshots {
		out[name] = cloneModelSnapshot(snapshot)
	}
	return out
}

// setModelSnapshotsSnapshot 用文件里的内容替换内存副本，供 applyRuntimeState 使用。
func (a *Agent) setModelSnapshotsSnapshot(snapshots map[string]config.StateModelSnapshot) {
	a.modelSnapshotsMu.Lock()
	defer a.modelSnapshotsMu.Unlock()
	if len(snapshots) == 0 {
		a.modelSnapshots = nil
		return
	}
	merged := make(map[string]config.StateModelSnapshot, len(snapshots))
	for name, snapshot := range snapshots {
		merged[name] = cloneModelSnapshot(snapshot)
	}
	a.modelSnapshots = merged
}

func cloneModelSnapshot(snapshot config.StateModelSnapshot) config.StateModelSnapshot {
	cloned := config.StateModelSnapshot{
		CompactModel: snapshot.CompactModel,
		NamingModel:  snapshot.NamingModel,
	}
	if len(snapshot.ModeModels) > 0 {
		cloned.ModeModels = make(map[string]config.ModelSelection, len(snapshot.ModeModels))
		for mode, selection := range snapshot.ModeModels {
			cloned.ModeModels[mode] = selection
		}
	}
	return cloned
}

// modelProfileNameExists 报告这个名字是否是静态模型选择（model_profiles 的名字或它的别名）。
func (a *Agent) modelProfileNameExists(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if _, ok := a.modelProfiles[name]; ok {
		return true
	}
	_, ok := a.modelAliases[strings.ToLower(name)]
	return ok
}

// normalizeModelSnapshotName 校验并归一化快照名。字符集刻意收窄到"字母/数字/下划线/连字符"
// （含中文等 Unicode 字母）：名字要作为 TOML 键落盘、在命令回复里显示，收窄比事后处理转义便宜。
func normalizeModelSnapshotName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errModelSnapshotNameEmpty
	}
	if runes := []rune(name); len(runes) > modelSnapshotNameLimit {
		return "", fmt.Errorf("快照名最长 %d 个字符，%q 有 %d 个", modelSnapshotNameLimit, name, len(runes))
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return "", fmt.Errorf("快照名 %q 含有不允许的字符 %q；只能用字母、数字、下划线、连字符", name, string(r))
	}
	return name, nil
}

func orderedModelSnapshotModes(modeModels map[string]config.ModelSelection) []string {
	modes := make([]string, 0, len(modeModels))
	seen := make(map[string]bool, len(modeModels))
	for _, mode := range modelSnapshotModeOrder {
		if _, ok := modeModels[mode]; !ok {
			continue
		}
		modes = append(modes, mode)
		seen[mode] = true
	}
	rest := make([]string, 0, len(modeModels))
	for mode := range modeModels {
		if !seen[mode] {
			rest = append(rest, mode)
		}
	}
	sort.Strings(rest)
	return append(modes, rest...)
}

// modelSnapshotView 把落盘结构转成命令层要显示的形状：模式槽位按固定顺序在前，
// compact / naming 固定在后，空槽位不显示。
func modelSnapshotView(name string, snapshot config.StateModelSnapshot) agentcommands.ModelSnapshot {
	view := agentcommands.ModelSnapshot{Name: name}
	for _, mode := range orderedModelSnapshotModes(snapshot.ModeModels) {
		selection := snapshot.ModeModels[mode]
		if selection.Provider == "" || selection.Model == "" {
			continue
		}
		view.Slots = append(view.Slots, agentcommands.ModelSnapshotSlot{Label: mode, Provider: selection.Provider, Model: selection.Model})
	}
	if snapshot.CompactModel.Provider != "" && snapshot.CompactModel.Model != "" {
		view.Slots = append(view.Slots, agentcommands.ModelSnapshotSlot{Label: "compact", Provider: snapshot.CompactModel.Provider, Model: snapshot.CompactModel.Model})
	}
	if snapshot.NamingModel.Provider != "" && snapshot.NamingModel.Model != "" {
		view.Slots = append(view.Slots, agentcommands.ModelSnapshotSlot{Label: "naming", Provider: snapshot.NamingModel.Provider, Model: snapshot.NamingModel.Model})
	}
	return view
}
