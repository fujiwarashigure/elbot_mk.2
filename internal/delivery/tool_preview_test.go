package delivery

import "testing"

func TestIsToolPreviewRecognizesAgentPreviewPrefix(t *testing.T) {
	preview := Text("[tool] 正在调用 shell")
	if !preview.IsToolPreview() {
		t.Fatalf("agent tool preview must be recognized: %#v", preview)
	}
	if !(Output{Kind: KindText, Text: "  [tool] 正在调用"}).IsToolPreview() {
		t.Fatal("leading whitespace must not defeat the check")
	}
}

func TestIsToolPreviewRejectsOtherOutput(t *testing.T) {
	cases := map[string]Output{
		"plain reply":       Text("正在调用 shell"),
		"bracketed word":    Text("[tools] 正在调用"),
		"empty":             Text(""),
		"non-text kind":     {Kind: KindImage, Text: "[tool] 图片"},
		"prefix only":       Text("[tool]"),
		"mention not tool":  Text("[toolbox] 内容"),
		"answer about tool": Text("我用了 [tool] 前缀"),
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if out.IsToolPreview() {
				t.Fatalf("%s must not be treated as a tool preview: %#v", name, out)
			}
		})
	}
}

func TestIsToolPreviewNoticeRequiresExactlyOneTextOutput(t *testing.T) {
	preview := Text("[tool] 正在调用 shell")
	if !IsToolPreviewNotice([]Output{preview}) {
		t.Fatal("single tool preview output must be recognized")
	}
	if IsToolPreviewNotice(nil) || IsToolPreviewNotice([]Output{}) {
		t.Fatal("empty notice must not be a tool preview")
	}
	if IsToolPreviewNotice([]Output{preview, Text("附加说明")}) {
		t.Fatal("multi-output notice must not match the platform group filter")
	}
	if IsToolPreviewNotice([]Output{Text("普通回答")}) {
		t.Fatal("plain notice must not be a tool preview")
	}
}

// 群聊跳过规则是"当前会话 + 群聊 + 工具预览"三条同时成立，缺任何一条都必须投递：
// 多丢一条用户可见内容比多发一条进度预览严重得多。
func TestShouldDropGroupToolPreviewRequiresAllThreeConditions(t *testing.T) {
	preview := Text("[tool] 正在调用 shell")
	reply := Text("普通回答")
	target := Target{PrivateUserID: "10001"}
	cases := []struct {
		name         string
		target       Target
		outputs      []Output
		groupContext bool
		want         bool
	}{
		{name: "group current-conversation preview", target: Target{}, outputs: []Output{preview}, groupContext: true, want: true},
		{name: "private current-conversation preview", target: Target{}, outputs: []Output{preview}, groupContext: false, want: false},
		{name: "group current-conversation reply", target: Target{}, outputs: []Output{reply}, groupContext: true, want: false},
		{name: "explicit target keeps preview", target: target, outputs: []Output{preview}, groupContext: true, want: false},
		{name: "no outputs", target: Target{}, outputs: nil, groupContext: true, want: false},
		{name: "multi output notice", target: Target{}, outputs: []Output{preview, reply}, groupContext: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldDropGroupToolPreview(tc.target, tc.outputs, tc.groupContext); got != tc.want {
				t.Fatalf("ShouldDropGroupToolPreview = %v, want %v", got, tc.want)
			}
		})
	}
}

// 群聊里"普通通知照发"是这条规则不能放宽的边界：它只针对工具进度预览。
func TestShouldDropGroupToolPreviewKeepsPlainNotices(t *testing.T) {
	plain := []Output{Text("普通回答")}
	if ShouldDropGroupToolPreview(Target{}, plain, true) {
		t.Fatal("plain group notice must not be dropped")
	}
}
