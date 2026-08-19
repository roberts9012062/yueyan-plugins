// marketplace-repo/stats-pro/main.go
// 站点统计增强插件（进程外，免费）：访客 PV/UV 计数 + 热门内容排行。
//
// 能力划分：
//   - api：POST /hit 访客上报（经宿主公开桥接 /api/v1/stats/hit 匿名可达）、
//     GET /summary 统计汇总（管理员，后台页数据源）
//   - frontend：post.footer 槽位上报脚本（页面浏览即上报，帖内展示计数）
//   - admin.page：后台「站点统计」页（今日/昨日/累计 + 热门内容 Top10）
//   - settings：管理员访问不计数开关
//
// 计数模型：按日 PV/UV（UV 以访客自报 visitor_id 按日去重）+ 帖子浏览计数 +
// 累计总量；内存计数、变更节流落盘（插件数据目录 stats.json），重启不丢。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与 plugin.json / yueyan-plugin.json 一致）。
const pluginID = "stats-pro"

// StatsPlugin 站点统计插件实现（进程外）。
type StatsPlugin struct {
	store *statsStore // 计数存储（OnActivate 初始化）
}

// Info 插件信息（与商城清单一致；能力声明 + 设置项）。
func (p *StatsPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:           pluginID,
		Name:         "站点统计增强",
		Version:      "1.5.0",
		Author:       "月言官方",
		Description:  "访客 PV/UV 计数、热门内容排行与后台统计面板，比基础报表更细。免费。",
		Capabilities: []string{"api", "frontend", "settings", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "exclude_admin", Label: "管理员访问不计数", Type: "switch", Default: "on"},
		},
	}
}

// OnActivate 启用回调：初始化计数存储（加载历史 stats.json）。
func (p *StatsPlugin) OnActivate(ctx context.Context) error {
	store, err := newStatsStore(pluginID)
	if err != nil {
		return err
	}
	p.store = store
	return nil
}

// OnDeactivate 停用回调：落盘剩余计数后释放句柄。
func (p *StatsPlugin) OnDeactivate(ctx context.Context) error {
	if p.store != nil {
		_ = p.store.flush()
		p.store = nil
	}
	return nil
}

// Hooks 订阅钩子（统计属旁路观察，不插入业务管道）。
func (p *StatsPlugin) Hooks() []sdk.Hook { return nil }

// storeSafe 取计数存储（未激活返回 nil，调用方判空）。
func (p *StatsPlugin) storeSafe() *statsStore {
	return p.store
}

// RegisterAPI 自定义 API（宿主挂 /api/v1/plugins/stats-pro/**）：
//
//	POST /hit      访客上报 {post_id?, visitor_id?}（经宿主公开桥接匿名可达；
//	               也可登录用户直调——管理员按设置排除）
//	GET  /summary  统计汇总 {today, yesterday, totals, top_posts}（仅管理员）
func (p *StatsPlugin) RegisterAPI(api *sdk.APIMux) {
	api.Handle("POST", "/hit", p.handleHit)
	api.Handle("GET", "/summary", p.handleSummary)
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[stats-pro] 进程启动")
	server.Serve(&StatsPlugin{})
}
