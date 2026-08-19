// marketplace-repo/stats-pro/api.go
// 自定义 API 处理器：访客上报（POST /hit）与统计汇总（GET /summary）。
package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// postHit 热门内容条目。
type postHit struct {
	PostID string `json:"post_id"` // 帖子 ID
	Hits   int64  `json:"hits"`    // 浏览次数
}

// hitRequest 上报请求体（宿主桥接匿名转发或登录用户直调）。
type hitRequest struct {
	PostID    string `json:"post_id"`    // 帖子 ID（帖子页上报；列表页等可空）
	VisitorID string `json:"visitor_id"` // 访客标识（前端 localStorage 随机 ID，UV 去重用）
}

// jsonOut JSON 响应封装。
func jsonOut(status int, payload map[string]any) (int, []byte, error) {
	raw, _ := json.Marshal(payload)
	return status, raw, nil
}

// handleHit POST /hit：访客上报。
// 鉴权：匿名放行（公开桥接场景）；登录用户按 exclude_admin 设置排除管理员
// （站长自己刷帖不污染统计）。
func (p *StatsPlugin) handleHit(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	store := p.storeSafe()
	if store == nil {
		return jsonOut(500, map[string]any{"error": "插件未激活"})
	}
	var req hitRequest
	_ = json.Unmarshal(body, &req) // 空体也计一次 PV（纯浏览上报）
	cfg := sdk.Config(ctx)
	if strings.TrimSpace(cfg["exclude_admin"]) != "off" && sdk.CallerIsAdmin(ctx) {
		return jsonOut(200, map[string]any{"counted": false, "reason": "管理员访问不计数"})
	}
	postID := strings.TrimSpace(req.PostID)
	if postID != "" {
		if _, err := strconv.ParseInt(postID, 10, 64); err != nil {
			return jsonOut(400, map[string]any{"error": "非法 post_id"})
		}
	}
	visitorID := strings.TrimSpace(req.VisitorID)
	if len(visitorID) > 64 {
		visitorID = visitorID[:64] // 防超长标识撑爆去重表
	}
	store.record(time.Now().Format("2006-01-02"), visitorID, postID)
	return jsonOut(200, map[string]any{"counted": true})
}

// handleSummary GET /summary：统计汇总（仅管理员/系统——后台页数据源）。
func (p *StatsPlugin) handleSummary(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.TrustedCaller(ctx) {
		return jsonOut(403, map[string]any{"error": "仅管理员可查看统计"})
	}
	store := p.storeSafe()
	if store == nil {
		return jsonOut(500, map[string]any{"error": "插件未激活"})
	}
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	totalPV, totalUV := store.totals()
	return jsonOut(200, map[string]any{
		"today":     store.dayStats(today),
		"yesterday": store.dayStats(yesterday),
		"total_pv":  totalPV,
		"total_uv":  totalUV,
		"top_posts": store.topPosts(),
	})
}
