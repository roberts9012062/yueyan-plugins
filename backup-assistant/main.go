// marketplace-repo/backup-assistant/main.go
// 备份助手插件（进程外，免费）：媒体库定时/手动备份 + 保留策略 + 完成通知。
//
// 能力划分：
//   - api：POST /run 立即备份、GET /history 备份历史（管理员）
//   - settings：调度周期（off/daily/weekly）、保留份数、完成通知 webhook
//   - admin.page：后台「备份助手」页（备份历史 + 立即备份按钮，见 frontend/）
//
// 说明：备份对象为站点媒体库（data/media 目录打包 ZIP）；「云端通道」以完成通知
// webhook 实现（备份完成后 POST 元信息到站长配置的地址，可对接网盘/群机器人）。
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与 plugin.json / yueyan-plugin.json 一致）。
const pluginID = "backup-assistant"

// scheduleTick 调度巡检间隔（分钟级精度足够；每次巡检重读配置——设置变更无需重启）。
const scheduleTick = time.Minute

// BackupPlugin 备份助手插件实现（进程外）。
type BackupPlugin struct {
	mu        sync.Mutex
	store     *backupStore // 备份存储（OnActivate 初始化）
	schedMu   sync.Mutex
	schedStop chan struct{} // 定时器停止信号（OnDeactivate 关闭）
	schedDone chan struct{} // 定时器退出回执
}

// Info 插件信息（与商城清单一致；能力声明 + 设置项）。
func (p *BackupPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:           pluginID,
		Name:         "备份助手",
		Version:      "1.2.0",
		Author:       "月言官方",
		Description:  "媒体库定时/手动备份：ZIP 打包落盘 + 保留策略自动清理 + 完成通知 webhook（云端通道）。免费。",
		Capabilities: []string{"api", "settings", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "schedule", Label: "定时备份（off=关闭，daily=每天，weekly=每周）", Type: "select", Default: "off", Options: []string{"off", "daily", "weekly"}},
			{Key: "retention_count", Label: "保留份数（超出自动清理最旧）", Type: "text", Default: "5"},
			{Key: "webhook_url", Label: "完成通知 webhook（POST 备份元信息，留空不通知）", Type: "text", Default: ""},
		},
	}
}

// OnActivate 启用回调：初始化备份存储 + 启动定时巡检。
func (p *BackupPlugin) OnActivate(ctx context.Context) error {
	store, err := newBackupStore(pluginID)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.store = store
	p.mu.Unlock()
	p.startScheduler()
	return nil
}

// OnDeactivate 停用回调：停定时器 + 释放存储句柄（历史备份文件保留）。
func (p *BackupPlugin) OnDeactivate(ctx context.Context) error {
	p.stopScheduler()
	p.mu.Lock()
	p.store = nil
	p.mu.Unlock()
	return nil
}

// Hooks 订阅钩子（备份属独立任务，不插入业务管道）。
func (p *BackupPlugin) Hooks() []sdk.Hook { return nil }

// storeSafe 取备份存储（未激活返回 nil，调用方判空）。
func (p *BackupPlugin) storeSafe() *backupStore {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.store
}

// startScheduler 启动定时巡检（每分钟读配置判断是否到点；幂等——已运行先停）。
func (p *BackupPlugin) startScheduler() {
	p.stopScheduler()
	stop := make(chan struct{})
	done := make(chan struct{})
	p.schedMu.Lock()
	p.schedStop = stop
	p.schedDone = done
	p.schedMu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(scheduleTick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				p.runDueBackup()
			}
		}
	}()
}

// stopScheduler 停止定时巡检（等待 goroutine 退出；未运行为空操作）。
func (p *BackupPlugin) stopScheduler() {
	p.schedMu.Lock()
	stop, done := p.schedStop, p.schedDone
	p.schedStop, p.schedDone = nil, nil
	p.schedMu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

// runDueBackup 到点备份判定：调度开启且距上次成功备份超过周期则执行。
// 失败不记录 last_run（下个巡检周期重试），错误写 stderr 便于排查。
func (p *BackupPlugin) runDueBackup() {
	store := p.storeSafe()
	if store == nil {
		return
	}
	cfg := sdk.Config(context.Background())
	interval, ok := scheduleInterval(cfg["schedule"])
	if !ok {
		return // off / 非法值：不调度
	}
	state, _ := store.loadState()
	if time.Since(state.LastRun) < interval {
		return
	}
	if _, err := store.runBackup(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "[backup-assistant] 定时备份失败：", err)
		return
	}
}

// scheduleInterval 调度周期换算（off 或非法值返回 false；纯函数）。
func scheduleInterval(schedule string) (time.Duration, bool) {
	switch schedule {
	case "daily":
		return 24 * time.Hour, true
	case "weekly":
		return 7 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

// RegisterAPI 自定义 API（宿主挂 /api/v1/plugins/backup-assistant/**；管理端点统一 TrustedCaller 拦截）：
//
//	POST /run      立即备份 → {file, size, created_at} 或 {error}
//	GET  /history  备份历史 + 调度状态 → {items, schedule, last_run}
func (p *BackupPlugin) RegisterAPI(api *sdk.APIMux) {
	api.Handle("POST", "/run", p.handleRun)
	api.Handle("GET", "/history", p.handleHistory)
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[backup-assistant] 进程启动")
	server.Serve(&BackupPlugin{})
}
