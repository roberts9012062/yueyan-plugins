// marketplace-repo/backup-assistant/main.go
// 备份助手插件（进程外，免费）：数据库 + 前端 + 后端全量定时/手动备份。
//
// 能力划分：
//   - api：POST /run 立即备份、GET /history 备份历史（管理员）
//   - settings：定点调度（每天/每周 HH:MM）、四类备份内容开关、pg_dump 路径、
//     保留份数、完成通知 webhook
//   - admin.page：后台「备份助手」页（备份历史 + 立即备份 + 下载，见 frontend/）
//
// 备份对象（v1.3.0 起，均可独立开关）：
//   - 数据库：pg_dump -Fc（连接信息取宿主环境变量/.env；pg_restore 可恢复）
//   - 媒体库：data/media 目录
//   - 前端：frontend 源代码（排除 node_modules/.next）+ .next 构建产物（排除 cache）
//   - 后端：Go 源代码（cmd/internal/pkg/db/scripts 等）+ server.exe 二进制
//
// 「云端通道」以完成通知 webhook 实现（备份完成后 POST 元信息到站长配置的地址）。
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

// 插件唯一 ID 与版本（与 plugin.json / yueyan-plugin.json 一致）。
const (
	pluginID      = "backup-assistant"
	pluginVersion = "1.3.1"
)

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
		Version:      pluginVersion,
		Author:       "月言官方",
		Description:  "全站备份：数据库（pg_dump）+ 媒体库 + 前端/后端源代码与构建产物；定点定时（每天/每周 HH:MM，错过自动补跑）+ 保留策略 + 完成通知。免费。",
		Capabilities: []string{"api", "settings", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "schedule", Label: "定时备份（off=关闭，daily=每天，weekly=每周）", Type: "select", Default: "off", Options: []string{"off", "daily", "weekly"}},
			{Key: "schedule_time", Label: "备份时刻（HH:MM，24 小时制；错过的时刻启动后自动补跑）", Type: "text", Default: "03:00"},
			{Key: "weekly_day", Label: "每周备份日（仅每周调度生效；0=周日 … 6=周六）", Type: "select", Default: "0", Options: []string{"0", "1", "2", "3", "4", "5", "6"}},
			{Key: "backup_db", Label: "备份数据库（pg_dump -Fc；需本机有 pg_dump）", Type: "switch", Default: "on"},
			{Key: "backup_media", Label: "备份媒体库（data/media 上传文件）", Type: "switch", Default: "on"},
			{Key: "backup_frontend", Label: "备份前端（源代码 + .next 构建产物）", Type: "switch", Default: "on"},
			{Key: "backup_backend", Label: "备份后端（Go 源代码 + server.exe 二进制）", Type: "switch", Default: "on"},
			{Key: "pg_dump_path", Label: "pg_dump 路径（留空=自动探测 PATH 与常见安装目录）", Type: "text", Default: ""},
			{Key: "db_user", Label: "备份专用数据库账号（留空=用宿主连接账号；建议只读账号）", Type: "text", Default: ""},
			{Key: "db_password", Label: "备份专用数据库密码（配合上方账号使用）", Type: "text", Default: ""},
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

// runDueBackup 到点备份判定：定点调度开启且上次成功备份早于最近应跑时刻则执行。
// 失败不记录 last_run（下个巡检周期重试），错误写 stderr 便于排查。
func (p *BackupPlugin) runDueBackup() {
	store := p.storeSafe()
	if store == nil {
		return
	}
	cfg := sdk.Config(context.Background())
	c := clockOrDefault(cfg["schedule_time"])
	day := weeklyDayOrDefault(cfg["weekly_day"])
	state, _ := store.loadState()
	if !dueForBackup(time.Now(), state.LastRun, cfg["schedule"], day, c) {
		return // off / 未到点 / 今日已备份
	}
	if _, err := store.runBackup(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "[backup-assistant] 定时备份失败：", err)
		return
	}
}

// RegisterAPI 自定义 API（宿主挂 /api/v1/plugins/backup-assistant/**；管理端点统一 TrustedCaller 拦截）：
//
//	POST /run      立即备份 → {file, size, created_at, parts} 或 {error}
//	GET  /history  备份历史 + 调度状态 → {items, schedule, schedule_time, ...}
func (p *BackupPlugin) RegisterAPI(api *sdk.APIMux) {
	api.Handle("POST", "/run", p.handleRun)
	api.Handle("GET", "/history", p.handleHistory)
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[backup-assistant] 进程启动")
	server.Serve(&BackupPlugin{})
}
