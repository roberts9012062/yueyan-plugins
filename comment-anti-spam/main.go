// marketplace-repo/comment-anti-spam/main.go
// 评论反垃圾插件（进程外，免费）：评论提交前的多级垃圾识别。
//
// 能力划分：
//   - hooks：订阅 comment.before_save（同步可拦截）——黑名单词 → 外链检测 → AI 智能判定
//   - ai + data.read：经宿主数据服务调用站点已配置的 AI 模型判定疑似垃圾（可选开关）
//   - settings：拦截策略（block 拒绝 / review 放行并留痕）、外链开关、黑名单词、AI 开关与模型
//
// 降级原则：AI 未配置/调用失败一律放行（反垃圾是增强能力，不阻断正常评论）；
// review 策略下命中只记录审计日志（插件数据目录 logs/audit.log），供后台巡查。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与 plugin.json / yueyan-plugin.json 一致）。
const pluginID = "comment-anti-spam"

// AntiSpamPlugin 评论反垃圾插件实现（进程外）。
type AntiSpamPlugin struct {
	audit *auditLog // 审计日志（OnActivate 初始化；review 策略留痕用）
}

// Info 插件信息（与商城清单一致；能力声明 + 设置项）。
func (p *AntiSpamPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:           pluginID,
		Name:         "评论反垃圾",
		Version:      "2.1.0",
		Author:       "月言官方",
		Description:  "评论提交前多级反垃圾：黑名单词 → 外链拦截 → AI 智能识别（可选），支持拒绝与留痕两种策略。",
		Capabilities: []string{"hooks", "ai", "settings", "data.read"},
		Settings: []sdk.SettingField{
			{Key: "policy", Label: "拦截策略（block=直接拒绝，review=放行并记录日志）", Type: "select", Default: "block", Options: []string{"block", "review"}},
			{Key: "block_links", Label: "拦截含外链评论", Type: "switch", Default: "on"},
			{Key: "blacklist_words", Label: "黑名单关键词（逗号或空格分隔）", Type: "text", Default: ""},
			{Key: "ai_check", Label: "AI 智能识别（需已在后台配置 AI；失败自动放行）", Type: "switch", Default: "off"},
			{Key: "ai_model", Label: "AI 模型（自动=第一个可用模型；选项来自后台 AI 设置）", Type: "select", Default: "auto", Options: aiModelOptions()},
		},
	}
}

// OnActivate 启用回调：初始化审计日志（插件数据目录 logs/audit.log）。
func (p *AntiSpamPlugin) OnActivate(ctx context.Context) error {
	log, err := newAuditLog(pluginID)
	if err != nil {
		return err
	}
	p.audit = log
	return nil
}

// OnDeactivate 停用回调：释放日志句柄（磁盘日志保留）。
func (p *AntiSpamPlugin) OnDeactivate(ctx context.Context) error {
	if p.audit != nil {
		_ = p.audit.close()
		p.audit = nil
	}
	return nil
}

// Hooks 订阅钩子：评论保存前同步检测（serial，可拦截）。
func (p *AntiSpamPlugin) Hooks() []sdk.Hook {
	return []sdk.Hook{
		{
			Name: "comment.before_save", Sync: true, Priority: 50,
			Handler: p.handleComment,
		},
	}
}

// aiModelAuto 下拉"自动"选项值（选中时 AI 判定取第一个可用模型）。
const aiModelAuto = "auto"

// aiModelOptions AI 模型下拉选项：经宿主数据服务读取后台 AI 设置已配置的模型
// （GetAIModels 返回供应商分组，展开为平铺模型名列表并去重——多供应商可能配置
// 同名模型）。数据服务未连接（插件启动初期的 Info 校验）或站点未配置 AI 时仅返回
// "自动"一项——设置页打开时插件已 running，能拿到完整列表。
func aiModelOptions() []string {
	options := []string{aiModelAuto}
	seen := map[string]bool{aiModelAuto: true}
	svc := sdk.Data(context.Background())
	if svc == nil {
		return options
	}
	models, err := svc.GetAIModels(context.Background())
	if err != nil {
		return options
	}
	for _, group := range models {
		for _, model := range group.Models {
			if !seen[model] {
				seen[model] = true
				options = append(options, model)
			}
		}
	}
	return options
}

// handleComment 评论检测入口：解析载荷 → 多级检测 → 按策略放行/拒绝/留痕。
// 载荷契约：主进程以评论内容字符串序列化（见 internal/service/comment.go Dispatch）。
func (p *AntiSpamPlugin) handleComment(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
	var content string
	if err := jsonUnmarshal(ev.Payload, &content); err != nil {
		return sdk.Result{OK: true}, nil // 载荷异常不拦截（宁可漏杀不可错杀正常评论）
	}
	cfg := sdk.Config(ctx)
	verdict := checkComment(ctx, cfg, content)
	if verdict == nil {
		return sdk.Result{OK: true}, nil // 未命中任何规则
	}
	// 留痕（无论策略，命中即记录——block 模式也有账可查）
	if p.audit != nil {
		p.audit.record(verdict.Rule, verdict.Detail, content)
	}
	if configPolicy(cfg) == policyReview {
		return sdk.Result{OK: true}, nil // review：放行 + 日志留痕，供后台巡查
	}
	return sdk.Result{OK: false, Reason: "评论未通过反垃圾检测：" + verdict.Reason}, nil
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[comment-anti-spam] 进程启动")
	server.Serve(&AntiSpamPlugin{})
}
