// marketplace-repo/comment-anti-spam/check.go
// 评论检测规则（纯函数为主）：黑名单词 → 外链 → AI 智能判定，逐级短路。
package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// 拦截策略取值（与设置项 policy 的 select 选项一致）。
const (
	policyBlock = "block" // 直接拒绝（返回用户可读原因）
	policyReview = "review" // 放行并记录审计日志，供后台巡查
)

// verdict 检测命中结论（未命中返回 nil）。
type verdict struct {
	Rule   string // 命中规则：blacklist / link / ai
	Reason string // 用户可读拒绝原因（block 策略直接展示）
	Detail string // 留痕明细（命中词/判定依据）
}

// linkPattern 外链识别：http/https 链接、www. 域名前缀（覆盖常见变体，误报可控）。
var linkPattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s]+`)

// jsonUnmarshal 薄封装（统一导入处；也供 main.go 使用）。
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// configPolicy 读拦截策略（非法值回退 block——默认从严）。
func configPolicy(cfg map[string]string) string {
	if cfg["policy"] == policyReview {
		return policyReview
	}
	return policyBlock
}

// configSwitch 读开关（"on" 为开，其余一律关）。
func configSwitch(cfg map[string]string, key string) bool {
	return strings.TrimSpace(cfg[key]) == "on"
}

// checkComment 多级检测入口（顺序：黑名单 → 外链 → AI；纯规则同步短路，AI 逐条调用）。
func checkComment(ctx context.Context, cfg map[string]string, content string) *verdict {
	if v := checkBlacklist(cfg, content); v != nil {
		return v
	}
	if v := checkLink(cfg, content); v != nil {
		return v
	}
	if configSwitch(cfg, "ai_check") {
		return checkByAI(ctx, cfg, content)
	}
	return nil
}

// checkBlacklist 黑名单词检测（词表逗号/空格分隔；命中即中）。
func checkBlacklist(cfg map[string]string, content string) *verdict {
	for _, word := range strings.FieldsFunc(cfg["blacklist_words"], func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == '\t' }) {
		word = strings.TrimSpace(word)
		if word != "" && strings.Contains(content, word) {
			return &verdict{Rule: "blacklist", Reason: "包含黑名单词「" + word + "」", Detail: "词：" + word}
		}
	}
	return nil
}

// checkLink 外链检测（开关开启且正文含链接即中）。
func checkLink(cfg map[string]string, content string) *verdict {
	if !configSwitch(cfg, "block_links") {
		return nil
	}
	if match := linkPattern.FindString(content); match != "" {
		return &verdict{Rule: "link", Reason: "包含外部链接「" + match + "」", Detail: "链接：" + match}
	}
	return nil
}

// aiPrompt AI 判定提示词（要求只回答 yes/no，便于稳定解析）。
const aiPrompt = "你是博客评论审核员。判断给定评论是否为垃圾评论（广告推销、灌水刷屏、诱导链接、无意义内容）。只回答 yes 或 no，不要任何其他内容。评论内容："

// checkByAI AI 智能判定：调用宿主 AI 服务（data.read 授权获得）；
// 未授权/未配置/调用失败/无法解析一律返回 nil 放行（降级原则：反垃圾不阻断正常评论）。
func checkByAI(ctx context.Context, cfg map[string]string, content string) *verdict {
	svc := sdk.Data(ctx)
	if svc == nil {
		return nil // 未声明 data.read 或宿主未下发连接：跳过 AI 检测
	}
	model := strings.TrimSpace(cfg["ai_model"])
	if model == "" || model == aiModelAuto { // 空/自动：取第一个可用模型
		models, err := svc.GetAIModels(ctx)
		if err != nil || len(models) == 0 || len(models[0].Models) == 0 {
			return nil // 站点未配置 AI：跳过
		}
		model = models[0].Models[0]
	}
	answer, err := svc.GenerateAI(ctx, model, aiPrompt, content)
	if err != nil {
		return nil // AI 调用失败：降级放行
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if strings.Contains(answer, "yes") {
		return &verdict{Rule: "ai", Reason: "AI 判定为垃圾评论", Detail: "AI 回答：" + strings.TrimSpace(answer)}
	}
	return nil
}
