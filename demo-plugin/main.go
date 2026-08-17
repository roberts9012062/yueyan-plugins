// cmd/demo-plugin/main.go
// 演示插件（M3.3 进程外验证）：订阅 post.before_publish（同步拦截）、
// post.after_publish（异步通知）、search.query（同步改写观察），暴露 GET /ping 自定义 API。
// 编译产物：data/plugins/demo-plugin/plugin(.exe)（./scripts/build-demo-plugin.sh）。
//
// 验证入口（冒烟）：
//   - 发帖标题含 [demo] → 400 拦截（同步钩子链路）
//   - 正常发帖 → 插件日志记录 after_publish（异步链路）
//   - 搜索触发 → 插件日志记录 search.query（同步链路）
//   - GET /api/v1/plugins/demo-plugin/ping → {"pong":true}（API 代理链路）
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// DemoPlugin 演示插件实现（进程外）。
type DemoPlugin struct{}

// Info 插件信息（与安装清单一致；主进程校验 ID）。
// Settings 声明设置项（M3.7：主进程经 Info RPC 收集 → 设置页 schema 驱动渲染 → 保存后下发）。
// Capabilities 声明能力（M3.8 授权模型：data.read=经 broker 查询主进程只读脱敏数据）。
func (p *DemoPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:          "demo-plugin",
		Name:        "演示插件",
		Version:     "0.2.0",
		Author:      "月言官方",
		Description: "M3.3 进程外插件演示：钩子 + 自定义 API",
		Capabilities: []string{"data.read"},
		Settings: []sdk.SettingField{
			{Key: "greeting", Label: "页脚问候语", Type: "text", Default: "你好，月言访客"},
			{Key: "show_badge", Label: "显示演示徽章", Type: "switch", Default: "on"},
			{Key: "theme", Label: "卡片主题", Type: "select", Default: "auto", Options: []string{"auto", "light", "dark"}},
		},
	}
}

// OnActivate 启用回调（初始化资源）。
func (p *DemoPlugin) OnActivate(ctx context.Context) error {
	logf("已激活")
	return nil
}

// OnDeactivate 停用回调（释放资源）。
func (p *DemoPlugin) OnDeactivate(ctx context.Context) error {
	logf("已停用")
	return nil
}

// postPayload 帖子事件载荷（宽松解析：仅取标题/状态字段，字段缺失不报错）。
type postPayload struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Hooks 订阅钩子（同步拦截 + 异步通知 + 同步改写观察）。
func (p *DemoPlugin) Hooks() []sdk.Hook {
	return []sdk.Hook{
		{
			Name: "post.before_publish", Sync: true, Priority: 100,
			Handler: func(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
				var payload postPayload
				if err := json.Unmarshal(ev.Payload, &payload); err != nil {
					return sdk.Result{OK: true}, nil
				}
				// 标题含 [demo] → 拒绝（验证同步拦截链路）
				if strings.Contains(payload.Title, "[demo]") {
					logf("拦截发布：%s", payload.Title)
					return sdk.Result{OK: false, Reason: "演示插件拦截：[demo] 标题禁止发布"}, nil
				}
				return sdk.Result{OK: true}, nil
			},
		},
		{
			Name: "post.after_publish", Sync: false, Priority: 100,
			Handler: func(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
				var payload postPayload
				_ = json.Unmarshal(ev.Payload, &payload)
				logf("异步钩子 after_publish：标题「%s」状态「%s」", payload.Title, payload.Status)
				return sdk.Result{OK: true}, nil
			},
		},
		{
			Name: "search.query", Sync: true, Priority: 100,
			Handler: func(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
				logf("同步钩子 search.query：关键词「%s」", string(ev.Payload))
				return sdk.Result{OK: true}, nil
			},
		},
		{
			// comment.after_save（异步）：验证 M3.8 补全接线 + 双 dispatch 修复（评论保存应只触发一次）
			Name: "comment.after_save", Sync: false, Priority: 100,
			Handler: func(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
				logf("异步钩子 comment.after_save 触发")
				return sdk.Result{OK: true}, nil
			},
		},
		{
			// content.render（同步，M3.9）：改写帖子正文（追加插件标记，演示内容渲染管道）
			Name: "content.render", Sync: true, Priority: 100,
			Handler: func(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
				var payload struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal(ev.Payload, &payload); err != nil {
					return sdk.Result{OK: true}, nil
				}
				logf("content.render 改写正文（%d 字）", len(payload.Content))
				return sdk.Result{OK: true, Modify: []byte(`{"content":"` +
					jsonEscape(payload.Content+"\n\n> 本文由演示插件渲染") + `"}`)}, nil
			},
		},
		{
			// api.middleware（同步，M3.9）：拦截删除帖子请求（演示"防误删"类中间件）
			Name: "api.middleware", Sync: true, Priority: 100,
			Handler: func(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
				var payload struct {
					Method string `json:"method"`
					Path   string `json:"path"`
				}
				if err := json.Unmarshal(ev.Payload, &payload); err != nil {
					return sdk.Result{OK: true}, nil
				}
				if payload.Method == "DELETE" && strings.HasPrefix(payload.Path, "/api/v1/posts/") {
					logf("api.middleware 拦截：%s %s", payload.Method, payload.Path)
					return sdk.Result{OK: false, Reason: "演示插件拦截：帖子删除操作已保护"}, nil
				}
				return sdk.Result{OK: true}, nil
			},
		},
		{
			// ai.after_generate（异步，M3.9）：AI 生成完成通知
			Name: "ai.after_generate", Sync: false, Priority: 100,
			Handler: func(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
				logf("异步钩子 ai.after_generate：%s", string(ev.Payload))
				return sdk.Result{OK: true}, nil
			},
		},
	}
}

// RegisterAPI 自定义 API（主进程挂 /api/plugins/demo-plugin/** 代理）。
func (p *DemoPlugin) RegisterAPI(api *sdk.APIMux) {
	api.Handle("GET", "/ping", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		logf("自定义 API：%s %s", method, path)
		return 200, []byte(`{"pong":true,"plugin":"demo-plugin"}`), nil
	})
		// /pro-status：付费功能演示（M3.5 许可证链路——FeatureEnabled 由主进程下发的许可决定）
		api.Handle("GET", "/pro-status", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
			lic := sdk.License(ctx)
			pro := lic.FeatureEnabled("demo_pro")
			logf("pro-status 查询：edition=%s pro=%v degraded=%v", lic.Edition, pro, lic.Degraded)
			return 200, []byte(fmt.Sprintf(`{"pro":%v,"edition":%q,"degraded":%v}`, pro, lic.Edition, lic.Degraded)), nil
		})
		// /settings：当前配置快照（M3.7 设置链路验证——主进程保存配置后经 SetConfig 下发）
		api.Handle("GET", "/settings", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
			cfg := sdk.Config(ctx)
			raw, _ := json.Marshal(cfg)
			logf("配置快照查询：%s", string(raw))
			return 200, raw, nil
		})
		// /data-demo：数据服务演示（M3.8——声明 data.read 后经 broker 查询主进程脱敏数据；
		// 未授权时 sdk.Data 返回 nil，按降级响应）
		api.Handle("GET", "/data-demo", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
			svc := sdk.Data(ctx)
			if svc == nil {
				return 200, []byte(`{"authorized":false,"reason":"未授权数据服务（需声明 data.read 能力）"}`), nil
			}
			user, err := svc.GetUser(ctx, 1) // 查询用户 1（管理员）脱敏信息
			if err != nil {
				return 200, []byte(fmt.Sprintf(`{"authorized":true,"error":%q}`, err.Error())), nil
			}
			settings, _ := svc.GetSettings(ctx)
			logf("数据服务查询：user=%s role=%s settings=%d 键", user.Nickname, user.Role, len(settings))
			return 200, []byte(fmt.Sprintf(
				`{"authorized":true,"user":{"id":%d,"nickname":%q,"role":%q},"settings_keys":%d}`,
				user.ID, user.Nickname, user.Role, len(settings))), nil
		})
}

// logf 插件日志（stderr → 主进程重定向到 logs/plugins/demo-plugin.log）。
func logf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "[demo-plugin] "+format+"\n", args...)
}

// jsonEscape JSON 字符串转义（content.render 改写正文拼 JSON 用）。
func jsonEscape(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw[1 : len(raw)-1])
}

// main 插件进程入口（server.Serve 完成握手与 gRPC 服务注册）。
func main() {
	// 启动探针（握手前写 stderr，验证子进程 stderr 管道链路）
	fmt.Fprintln(os.Stderr, "[demo-plugin] 进程启动")
	server.Serve(&DemoPlugin{})
}
