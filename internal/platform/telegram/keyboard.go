package telegram

import "strings"

func shouldAttachRiskKeyboard(text, prefix string) bool {
	return strings.Contains(text, "高风险工具调用等待确认") && strings.Contains(text, prefix+"confirm")
}

func riskKeyboard(prefix string) *replyMarkup {
	return &replyMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{
			commandButton("详情", prefix+"detail"),
			commandButton("确认", prefix+"confirm"),
		},
		{
			commandButton("确认此工具", prefix+"confirmtool"),
			commandButton("全部确认", prefix+"confirmall"),
		},
		{
			commandButton("拒绝", prefix+"reject"),
			commandButton("停止", prefix+"stop"),
		},
	}}
}

func commandButton(label, command string) inlineKeyboardButton {
	return inlineKeyboardButton{Text: label, CallbackData: command}
}
