// nav-links/icons.go
// 批量补抓图标端点（管理页「批量补图标」通道）。
//
// 设计要点：
//   - 宿主代理 API 对插件调用有 10s 超时——单条 favicon 抓取最坏 3 步 × 5s HTTP 超时
//     （根路径 → 页面声明 → DuckDuckGo 兜底），串行一批必超；故**并发抓取**（上限 4）
//     且每条设 8s 总预算（超时即弃，HTTP 客户端自身的 5s 超时保证内层协程自然收尾）；
//   - 每条独立落库（UpdateIcon 仅改图标字段），失败不落盘逐条回报——无会话状态，
//     前端分批循环天然支持进度与续跑。
package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// 批量抓取参数：并发上限与单条总预算（须显著小于宿主代理 10s 超时）。
const (
	iconBatchConcurrency = 4              // 单批内并发抓取数
	iconFetchBudget      = 8 * time.Second // 单条抓取总预算（超时按失败计）
	iconBatchMax         = 20             // 单批条数上限
)

// iconFetchOutcome 单条抓取结果（协程间传递；ok 时 icon 为 dataURL）。
type iconFetchOutcome struct {
	ok   bool
	icon string
	err  string
}

// fetchIconWithBudget 带总预算抓单条（预算内未完成按超时失败；纯并发封装）。
func fetchIconWithBudget(pageURL string) iconFetchOutcome {
	done := make(chan iconFetchOutcome, 1)
	go func() {
		dataURL, _, err := fetchFavicon(pageURL)
		if err != nil {
			done <- iconFetchOutcome{ok: false, err: err.Error()}
			return
		}
		done <- iconFetchOutcome{ok: true, icon: dataURL}
	}()
	select {
	case out := <-done:
		return out
	case <-time.After(iconFetchBudget):
		// 内层协程由 HTTP 客户端 5s 超时自然收尾，无需额外取消
		return iconFetchOutcome{ok: false, err: "抓取超时（8s）"}
	}
}

// registerIconsAPI 注册图标批量端点（POST /icons/batch，TrustedCaller）。
func registerIconsAPI(api *sdk.APIMux, p *NavLinksPlugin) {
	api.Handle("POST", "/icons/batch", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		st := p.storeSafe()
		if st == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			IDs []int64 `json:"ids"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.IDs) == 0 {
			return 400, jsonResp(map[string]any{"error": "请求体需为 {\"ids\":[…]} 且非空"}), nil
		}
		if len(req.IDs) > iconBatchMax {
			return 400, jsonResp(map[string]any{"error": "单批最多 20 条"}), nil
		}
		byID := make(map[int64]string, len(st.List()))
		for _, l := range st.List() {
			byID[l.ID] = l.URL
		}

		// 并发抓取（信号量限并发；结果按下标写回保持与请求顺序一致）
		outcomes := make([]iconFetchOutcome, len(req.IDs))
		var wg sync.WaitGroup
		sem := make(chan struct{}, iconBatchConcurrency)
		for i, id := range req.IDs {
			url, found := byID[id]
			if !found {
				outcomes[i] = iconFetchOutcome{ok: false, err: "站点不存在"}
				continue
			}
			wg.Add(1)
			go func(slot int, siteURL string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				outcomes[slot] = fetchIconWithBudget(siteURL)
			}(i, url)
		}
		wg.Wait()

		// 落库（成功才写；结果数组顺序与 ids 一致，前端可对应统计）
		results := make([]map[string]any, 0, len(req.IDs))
		for i, id := range req.IDs {
			out := outcomes[i]
			if !out.ok {
				results = append(results, map[string]any{"id": id, "ok": false, "error": out.err})
				continue
			}
			if !st.UpdateIcon(id, out.icon) {
				results = append(results, map[string]any{"id": id, "ok": false, "error": "图标保存失败"})
				continue
			}
			results = append(results, map[string]any{"id": id, "ok": true})
		}
		return 200, jsonResp(map[string]any{"results": results}), nil
	})
}
