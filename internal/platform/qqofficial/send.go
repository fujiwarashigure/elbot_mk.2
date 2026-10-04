package qqofficial

import (
	"context"
	"fmt"
	"strings"

	"elbot/internal/delivery"
	"elbot/internal/platform"
)

const (
	msgTypeText     = 0
	msgTypeMarkdown = 2
	msgTypeArk      = 3
	msgTypeMedia    = 7

	fileTypeImage = 1
	fileTypeFile  = 4

	// qqOfficialTextPageRunes keeps each page comfortably below the QQ
	// official API text limit even when every rune is a 3-byte CJK
	// character. Pages after the first get a numeric continuation marker.
	qqOfficialTextPageRunes = 1500
)

func receiptWithMessageID(id string) delivery.Receipt {
	id = strings.TrimSpace(id)
	if id == "" {
		return delivery.Receipt{}
	}
	return delivery.Receipt{PlatformMessageIDs: []string{id}}
}

func qqOfficialTextPages(text string) []string {
	runes := []rune(text)
	if len(runes) <= qqOfficialTextPageRunes {
		return []string{text}
	}
	pageSize := qqOfficialTextPageRunes - 16
	if pageSize <= 0 {
		pageSize = qqOfficialTextPageRunes
	}
	total := (len(runes) + pageSize - 1) / pageSize
	pages := make([]string, 0, total)
	for start := 0; start < len(runes); start += pageSize {
		end := start + pageSize
		if end > len(runes) {
			end = len(runes)
		}
		pages = append(pages, fmt.Sprintf("%s……（%d/%d）", string(runes[start:end]), len(pages)+1, total))
	}
	return pages
}

func (a *Adapter) sendContextOutput(ctx context.Context, outputs []delivery.Output) (delivery.Receipt, error) {
	t, err := a.contextTarget(ctx)
	if err != nil {
		return delivery.Receipt{}, err
	}
	var receipt delivery.Receipt
	for i, out := range outputs {
		sent, err := a.sendOutput(ctx, t, out)
		sent = qqOfficialMediaReceipt(sent, t, out, i)
		receipt = receipt.Merge(sent)
		if err != nil {
			return receipt.MarkPartialFailure(err), err
		}
	}
	return receipt, nil
}

func qqOfficialMediaReceipt(receipt delivery.Receipt, target sendTarget, out delivery.Output, outputIndex int) delivery.Receipt {
	if len(receipt.PlatformMessageIDs) != 1 || (out.Kind != delivery.KindImage && out.Kind != delivery.KindFile) {
		return receipt
	}
	receipt.SentMessages = append(receipt.SentMessages, delivery.SentMessage{PlatformMessageID: receipt.PlatformMessageIDs[0], Platform: platformName, ScopeID: qqOfficialTargetScope(target), OutputIndexes: []int{outputIndex}})
	return receipt
}

func qqOfficialTargetScope(target sendTarget) string {
	if target.Kind == targetGroup {
		return "group:" + target.OpenID
	}
	if target.Kind == targetC2C {
		return "c2c:" + target.OpenID
	}
	return ""
}
func (a *Adapter) sendOutput(ctx context.Context, t sendTarget, out delivery.Output) (delivery.Receipt, error) {
	switch out.Kind {
	case delivery.KindText:
		return a.sendText(ctx, t, out.Text)
	case delivery.KindReply:
		text := delivery.FallbackText(out)
		return a.sendText(ctx, t, text)
	case delivery.KindEmoticon:
		return a.sendText(ctx, t, delivery.FallbackText(out))
	case delivery.KindImage:
		return a.sendMedia(ctx, t, out, fileTypeImage)
	case delivery.KindFile:
		return a.sendMedia(ctx, t, out, fileTypeFile)
	default:
		return a.sendText(ctx, t, delivery.FallbackText(out))
	}
}

func (a *Adapter) contextTarget(ctx context.Context) (sendTarget, error) {
	if t, ok := ctx.Value(targetKey{}).(sendTarget); ok && t.Kind != "" && strings.TrimSpace(t.OpenID) != "" {
		return t, nil
	}
	if msg, ok := platform.MessageContextFrom(ctx); ok {
		scope := strings.TrimSpace(msg.ScopeID)
		if strings.HasPrefix(scope, "group:") {
			return sendTarget{Kind: targetGroup, OpenID: strings.TrimPrefix(scope, "group:"), MsgID: metaString(msg.Meta, metaMsgID), EventID: metaString(msg.Meta, metaEventID)}, nil
		}
		if strings.HasPrefix(scope, "c2c:") {
			return sendTarget{Kind: targetC2C, OpenID: strings.TrimPrefix(scope, "c2c:"), MsgID: metaString(msg.Meta, metaMsgID), EventID: metaString(msg.Meta, metaEventID)}, nil
		}
		openID := strings.TrimSpace(strings.TrimPrefix(msg.PlatformUserID, platformName+":"))
		if openID != "" {
			return sendTarget{Kind: targetC2C, OpenID: openID, MsgID: metaString(msg.Meta, metaMsgID), EventID: metaString(msg.Meta, metaEventID)}, nil
		}
	}
	return sendTarget{}, fmt.Errorf("qqofficial send target missing")
}

func (a *Adapter) sendText(ctx context.Context, target sendTarget, text string) (delivery.Receipt, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return delivery.Receipt{}, nil
	}
	if target.Proactive && !a.cfg.allowProactive() {
		return delivery.Receipt{}, fmt.Errorf("qqofficial proactive messages are disabled")
	}
	prefix := platform.PrimaryCommandPrefix(a.cfg.CommandPrefixes)
	if a.cfg.markdownByDefault() {
		receipt, err := a.sendTextPages(ctx, target, text, msgTypeMarkdown, prefix)
		if err == nil {
			return receipt, nil
		}
		if len(receipt.PlatformMessageIDs) > 0 {
			// Some markdown pages are already visible; a text fallback would
			// duplicate them, so report the partial delivery instead.
			return receipt.MarkPartialFailure(err), err
		}
		a.logWarn(ctx, "qqofficial markdown send failed, fallback to text", "error", err)
	}
	return a.sendTextPages(ctx, target, text, msgTypeText, prefix)
}

func (a *Adapter) sendTextPages(ctx context.Context, target sendTarget, text string, msgType int, prefix string) (delivery.Receipt, error) {
	pages := qqOfficialTextPages(text)
	keyboard := a.cfg.enableKeyboard() && shouldAttachRiskKeyboard(text, prefix)
	var receipt delivery.Receipt
	for i, page := range pages {
		msg := a.baseMessage(target)
		switch msgType {
		case msgTypeMarkdown:
			msg.MsgType = msgTypeMarkdown
			msg.Markdown = &messageMarkdown{Content: page}
		default:
			msg.MsgType = msgTypeText
			msg.Content = page
		}
		if i == 0 && keyboard {
			msg.Keyboard = riskKeyboard(a.cfg.AppID, prefix)
		}
		resp, err := a.client.sendMessage(ctx, target, msg)
		if err != nil {
			return receipt.MarkPartialFailure(fmt.Errorf("page %d/%d: %w", i+1, len(pages), err)), err
		}
		if id := strings.TrimSpace(resp.ID); id != "" {
			receipt.PlatformMessageIDs = append(receipt.PlatformMessageIDs, id)
		}
	}
	return receipt, nil
}

func (a *Adapter) sendMedia(ctx context.Context, target sendTarget, out delivery.Output, fileType int) (delivery.Receipt, error) {
	if target.Proactive && !a.cfg.allowProactive() {
		return delivery.Receipt{}, fmt.Errorf("qqofficial proactive messages are disabled")
	}
	source, err := prepareSource(out.Source.URL, out.Source.Path, out.Source.Data)
	if err != nil {
		return delivery.Receipt{}, err
	}
	uploaded, err := a.client.uploadFile(ctx, target, fileType, source)
	if err != nil {
		return delivery.Receipt{}, err
	}
	msg := a.baseMessage(target)
	msg.MsgType = msgTypeMedia
	msg.Media = &messageMedia{FileInfo: uploaded.FileInfo}
	resp, err := a.client.sendMessage(ctx, target, msg)
	if err != nil {
		return delivery.Receipt{}, err
	}
	return receiptWithMessageID(resp.ID), nil
}

func (a *Adapter) baseMessage(target sendTarget) messageToCreate {
	msg := messageToCreate{}
	if strings.TrimSpace(target.MsgID) != "" {
		msg.MsgID = strings.TrimSpace(target.MsgID)
		msg.MsgSeq = a.nextMsgSeq(msg.MsgID)
	} else if strings.TrimSpace(target.EventID) != "" {
		msg.EventID = strings.TrimSpace(target.EventID)
	}
	return msg
}

func (a *Adapter) sendArk(ctx context.Context, target sendTarget, ark messageArk) (delivery.Receipt, error) {
	if !a.cfg.EnableArk {
		return delivery.Receipt{}, fmt.Errorf("qqofficial ark is disabled")
	}
	msg := a.baseMessage(target)
	msg.MsgType = msgTypeArk
	msg.Ark = &ark
	resp, err := a.client.sendMessage(ctx, target, msg)
	if err != nil {
		return delivery.Receipt{}, err
	}
	return receiptWithMessageID(resp.ID), nil
}

func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	switch value := meta[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}
