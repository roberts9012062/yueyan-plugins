// marketplace-repo/backup-assistant/api.go
// 自定义 API 处理器：立即备份（POST /run）与历史查询（GET /history）。
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// weeklyDayLabels 周几显示文案（索引 0=周日 … 6=周六）。
var weeklyDayLabels = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// jsonReply JSON 响应封装（err 非空时返回 {error}）。
func jsonReply(status int, payload map[string]any) (int, []byte, error) {
	if status != 200 {
		payload = map[string]any{"error": payload["error"]}
	}
	raw, _ := json.Marshal(payload)
	return status, raw, nil
}

// handleRun POST /run：立即备份（仅管理员/系统——备份属站点级操作）。
func (p *BackupPlugin) handleRun(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.TrustedCaller(ctx) {
		return jsonReply(403, map[string]any{"error": "仅管理员可执行备份"})
	}
	store := p.storeSafe()
	if store == nil {
		return jsonReply(500, map[string]any{"error": "插件未激活"})
	}
	item, err := store.runBackup(sdk.Config(ctx))
	if err != nil {
		return jsonReply(500, map[string]any{"error": "备份失败：" + err.Error()})
	}
	return jsonReply(200, map[string]any{"file": item.File, "size": item.Size, "created_at": item.CreatedAt, "parts": item.Parts})
}

// handleHistory GET /history：备份历史 + 当前调度与内容开关状态（管理员）。
func (p *BackupPlugin) handleHistory(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.TrustedCaller(ctx) {
		return jsonReply(403, map[string]any{"error": "仅管理员可查看备份"})
	}
	store := p.storeSafe()
	if store == nil {
		return jsonReply(500, map[string]any{"error": "插件未激活"})
	}
	cfg := sdk.Config(ctx)
	lastRun := ""
	if state, err := store.loadState(); err == nil {
		lastRun = state.LastRun.Format("2006-01-02 15:04:05")
	}
	c := clockOrDefault(cfg["schedule_time"])
	day := weeklyDayOrDefault(cfg["weekly_day"])
	return jsonReply(200, map[string]any{
		"items":           store.history(),
		"schedule":        cfg["schedule"],
		"schedule_time":   fmt.Sprintf("%02d:%02d", c.Hour, c.Minute),
		"weekly_day":      day,
		"weekly_label":    weeklyDayLabels[day],
		"retention":       retentionCount(cfg),
		"last_run":        lastRun,
		"backup_db":       switchOn(cfg, "backup_db"),
		"backup_media":    switchOn(cfg, "backup_media"),
		"backup_frontend": switchOn(cfg, "backup_frontend"),
		"backup_backend":  switchOn(cfg, "backup_backend"),
	})
}
