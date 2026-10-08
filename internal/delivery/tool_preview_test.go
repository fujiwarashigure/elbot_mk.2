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
