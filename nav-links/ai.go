// nav-links/ai.go
// AI 智能识别：只给一个 URL，AI 总结站点名称、分类、标签、简介。
// 流程：抓取页面元信息（pagemeta.go）→ 连同 URL 喂给主进程 AI（data.read 数据服务）
// → 解析 JSON 输出回填表单。抓取失败时降级为仅凭 URL 与已填字段生成。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// aiPromptTemplate 智能识别指令（约束 AI 只输出紧凑 JSON，便于稳定解析与控制长度）。
const aiPromptTemplate = "你是网站导航编辑。根据下面的网站信息，总结这个站点并生成收藏信息。" +
	"网站名字：取页面标题主体（去掉「 - 」「 | 」等分隔符后缀），2-20 字；" +
	"简介：一句话概括站点用途与特色，20-60 字，必填；" +
	"分类：2-6 个字（如：开发工具、设计资源、学习教育、资讯媒体、技术博客、生活服务、AI 工具）；" +
	"标签：3-5 个，每个 2-8 字。" +
	"四个字段一个都不能少。只输出一行紧凑 JSON（键名与示例一致，不加换行、空格与代码围栏）：" +
	"{\"name\":\"名字\",\"description\":\"简介\",\"category\":\"分类\",\"tags\":[\"标签\"]}。网站信息："

// aiSuggestResult AI 识别结果（响应直出）。
type aiSuggestResult struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
}

// parseAISuggestInput 解析 AI 识别请求 body（url 必填；name/description 为用户已填参考；纯函数）。
func parseAISuggestInput(body []byte) (string, LinkInput, error) {
	var req struct {
		Model       string   `json:"model"`
		URL         string   `json:"url"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", LinkInput{}, errors.New("请求体需为 JSON 对象")
	}
	if req.Model == "" {
		return "", LinkInput{}, errors.New("请先选择 AI 模型")
	}
	if strings.TrimSpace(req.URL) == "" {
		return "", LinkInput{}, errors.New("请先填写站点地址")
	}
	return req.Model, LinkInput{
		URL:         strings.TrimSpace(req.URL),
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
	}, nil
}

// buildAIContent 组装 AI 输入内容：页面元信息 + 用户已填字段（纯函数）。
func buildAIContent(in LinkInput, meta pageMeta) string {
	var b strings.Builder
	b.WriteString("地址：" + normalizeURL(in.URL))
	if meta.Title != "" {
		b.WriteString("\n页面标题：" + meta.Title)
	}
	if meta.Description != "" {
		b.WriteString("\n页面描述：" + meta.Description)
	}
	if meta.TextDigest != "" {
		b.WriteString("\n正文摘要：" + meta.TextDigest)
	}
	if in.Name != "" {
		b.WriteString("\n用户已填名称：" + in.Name)
	}
	if in.Description != "" {
		b.WriteString("\n用户已填简介：" + in.Description)
	}
	return b.String()
}

// extractJSONObject 从 AI 输出中截取首个 { 到最后一个 } 的子串（容错代码围栏与前后杂文；纯函数）。
func extractJSONObject(text string) (string, bool) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "", false
	}
	return text[start : end+1], true
}

// truncateRunes 按 rune 截断字符串（纯函数）。
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// parseAISuggest 解析并清洗 AI 输出（纯函数；长度与数量收敛到存储约束内）。
func parseAISuggest(text string) (aiSuggestResult, error) {
	obj, ok := extractJSONObject(strings.TrimSpace(text))
	if !ok {
		return aiSuggestResult{}, errors.New("AI 返回格式异常，请重试")
	}
	var parsed aiSuggestResult
	if err := json.Unmarshal([]byte(obj), &parsed); err != nil {
		return aiSuggestResult{}, errors.New("AI 返回解析失败，请重试")
	}
	name := strings.TrimSpace(parsed.Name)
	category := strings.TrimSpace(parsed.Category)
	description := strings.TrimSpace(parsed.Description)
	if category == "" && name == "" {
		return aiSuggestResult{}, errors.New("AI 未给出有效结果，请补充信息后重试")
	}
	result := aiSuggestResult{
		Name:        truncateRunes(name, linkNameMaxLen),
		Category:    truncateRunes(category, linkCatMaxLen),
		Description: truncateRunes(description, linkDescMaxLen),
		Tags:        cleanTags(parsed.Tags),
	}
	if result.Tags == nil {
		result.Tags = []string{}
	}
	return result, nil
}

// suggestViaAI 调用主进程 AI 生成站点识别结果（连接器：经数据服务路由，用量计入站点统计）。
// 页面抓取失败不阻断——降级为仅凭 URL 与已填字段生成（SPA 站点等场景）。
// AI 漏输出 name/description 时用页面元信息兜底（模型对多字段遵循度不一）。
func suggestViaAI(ctx context.Context, svc sdk.DataService, model string, in LinkInput) (aiSuggestResult, error) {
	meta, err := fetchPageMeta(in.URL)
	if err != nil {
		// 降级：无页面内容时把抓取失败原因留给日志，AI 仍可按 URL 字面推断
		fmt.Fprintln(os.Stderr, "[nav-links] 页面抓取降级:", err.Error())
		meta = pageMeta{}
	}
	text, err := svc.GenerateAI(ctx, model, aiPromptTemplate, buildAIContent(in, meta))
	if err != nil {
		return aiSuggestResult{}, errors.New("AI 生成失败：" + err.Error())
	}
	result, err := parseAISuggest(text)
	if err != nil {
		return aiSuggestResult{}, err
	}
	if result.Name == "" && meta.Title != "" {
		result.Name = truncateRunes(meta.Title, linkNameMaxLen)
	}
	if result.Description == "" && meta.Description != "" {
		result.Description = truncateRunes(meta.Description, linkDescMaxLen)
	}
	return result, nil
}
