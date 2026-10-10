package imruntime

import (
	"context"
	"math"
	"regexp"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

var TelegramBotCommands = []BotCommand{{"start", "开始使用"}, {"help", "查看帮助"}, {"new", "新建会话或切换项目"}, {"projects", "查看最近项目"}, {"sessions", "查看项目历史会话"}, {"resume", "恢复历史会话"}, {"status", "查看当前状态"}, {"clear", "清空当前上下文"}, {"stop", "停止当前生成"}, {"provider", "切换 Provider"}, {"model", "切换模型"}, {"skills", "查看并调用 Skills"}, {"allow", "允许权限请求"}, {"always", "本会话永久允许"}, {"deny", "拒绝权限请求"}}

func SyncTelegramCommands(ctx context.Context, api *TelegramAPI) error {
	if _, err := api.Call(ctx, "deleteMyCommands", protocol.NewJSONObject()); err != nil {
		return err
	}
	_, err := api.Call(ctx, "setMyCommands", protocol.NewJSONObject(protocol.JSONField{Name: "commands", Value: raw(TelegramBotCommands)}))
	return err
}

type SelectionItem struct {
	Label       string            `json:"label"`
	Value       string            `json:"value"`
	Description string            `json:"description,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
}
type SelectionCallback struct {
	Kind   string `json:"kind"`
	Action string `json:"action"`
	Index  int    `json:"index"`
}

var selectionCallback = regexp.MustCompile(`^tgsel:([a-z_]+):(pick|page|noop):(\d+)$`)

func ParseSelectionCallback(data string) *SelectionCallback {
	match := selectionCallback.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	switch match[1] {
	case "provider", "model", "resume_project", "resume_session", "skill":
	default:
		return nil
	}
	n, err := strconv.ParseFloat(match[3], 64)
	if err != nil || n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
		return nil
	}
	return &SelectionCallback{match[1], match[2], int(n)}
}

type SelectionButton struct {
	Text     string `json:"text"`
	Callback string `json:"callbackData"`
}
type VisibleSelectionItem struct {
	SelectionItem
	Index int `json:"index"`
}
type SelectionPage struct {
	Page       int                    `json:"page"`
	TotalPages int                    `json:"totalPages"`
	Visible    []VisibleSelectionItem `json:"visibleItems"`
	Rows       [][]SelectionButton    `json:"rows"`
}

func BuildSelectionPage(kind string, items []SelectionItem, page float64, size int) SelectionPage {
	if size == 0 {
		size = 8
	}
	total := (len(items) + size - 1) / size
	if total < 1 {
		total = 1
	}
	if math.IsInf(page, 0) || math.IsNaN(page) || page < 0 {
		page = 0
	}
	if page >= float64(total) {
		page = float64(total - 1)
	}
	index := int(math.Floor(page))
	result := SelectionPage{index, total, []VisibleSelectionItem{}, [][]SelectionButton{}}
	start := index * size
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	for i := start; i < end; i++ {
		item := items[i]
		result.Visible = append(result.Visible, VisibleSelectionItem{item, i})
		label := []rune(protocol.TrimJavaScriptString(item.Label))
		if len(label) == 0 {
			label = []rune("选择")
		}
		if len(label) > 32 {
			label = append(label[:31], '…')
		}
		result.Rows = append(result.Rows, []SelectionButton{{string(label), "tgsel:" + kind + ":pick:" + stringNumber(i)}})
	}
	if total > 1 {
		nav := []SelectionButton{}
		if index > 0 {
			nav = append(nav, SelectionButton{"Prev", "tgsel:" + kind + ":page:" + stringNumber(index-1)})
		}
		nav = append(nav, SelectionButton{stringNumber(index+1) + "/" + stringNumber(total), "tgsel:" + kind + ":noop:" + stringNumber(index)})
		if index < total-1 {
			nav = append(nav, SelectionButton{"Next", "tgsel:" + kind + ":page:" + stringNumber(index+1)})
		}
		result.Rows = append(result.Rows, nav)
	}
	return result
}
