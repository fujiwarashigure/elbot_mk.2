package qqofficial

import "strings"

func shouldAttachRiskKeyboard(text, prefix string) bool {
	return strings.Contains(text, "高风险工具调用等待确认") && strings.Contains(text, prefix+"confirm")
}

func riskKeyboard(appID, prefix string) *messageKeyboard {
	return &messageKeyboard{Content: &inlineKeyboardContent{
		BotAppID: appID,
		Rows: []keyboardRow{
			{Buttons: []keyboardButton{
				commandButton("detail", "详情", "已查看", prefix+"detail", 0),
				commandButton("confirm", "确认", "已确认", prefix+"confirm", 1),
			}},
			{Buttons: []keyboardButton{
				commandButton("confirmtool", "确认此工具", "已确认此工具", prefix+"confirmtool", 1),
				commandButton("confirmall", "全部确认", "已全部确认", prefix+"confirmall", 1),
			}},
			{Buttons: []keyboardButton{
				commandButton("reject", "拒绝", "已拒绝", prefix+"reject", 0),
				commandButton("stop", "停止", "已停止", prefix+"stop", 0),
			}},
		},
	}}
}

func commandButton(id, label, visited, command string, style int) keyboardButton {
	return keyboardButton{
		ID: id,
		RenderData: keyboardRenderData{
			Label:        label,
			VisitedLabel: visited,
			Style:        style,
		},
		Action: keyboardButtonAction{
			Type: 2,
			Permission: keyboardPermission{
				Type: 2,
			},
			ClickLimit:    10,
			UnsupportTips: command,
			Data:          command,
			Enter:         true,
		},
	}
}
