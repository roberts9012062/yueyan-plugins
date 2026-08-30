// nav-links/ai.go
// AI 智能分类+标签：经数据服务（data.read）调用主进程 AI，按站点地址/名称/简介
// 生成分类与标签建议（模式对齐 seo-optimizer：模型列表 GetAIModels + 生成 GenerateAI）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// aiPromptTemplate 分类标签生成指令（约束 AI 只输出 JSON，便于稳定解析）。
const aiPromptTemplate = "你是网站导航编辑。根据下面的网站信息判断它最合适的分类与标签。" +
	"分类用 2-6 个字（例如：开发工具、设计资源、学习教育、资讯媒体、技术博客、生活服务、娱乐休闲、购物电商）；" +
	"标签给 3-6 个，每个 2-8 个字。只输出 JSON，格式 {\"category\":\"分类\",\"tags\":[\"标签1\",\"标签2\"]}，不要输出任何其他文字。网站信息："

// aiSuggestResult AI 建议结果（响应直出）。
type aiSuggestResult struct {
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
}

// parseAISuggestInput 解析 AI 建议请求 body（纯函数）。
func parseAISuggestInput(body []byte) (string, LinkInput, error) {
	var req struct {
		Model       string   `json:"model"`
		URL         string   `json:"url"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", LinkInput{}, errors.New("请求体需为 JSON 对象")
	}
	if req.Model == "" {
		return "", LinkInput{}, errors.New("请先选择 AI 模型")
	}
	if strings.TrimSpace(req.URL) == "" && strings.TrimSpace(req.Name) == "" {
		return "", LinkInput{}, errors.New("请先填写站点地址或网站名字")
	}
	return req.Model, LinkInput{
		URL:         strings.TrimSpace(req.URL),
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Tags:        req.Tags,
	}, nil
}

// buildAIContent 组装 AI 输入内容（纯函数）。
func buildAIContent(in LinkInput) string {
	parts := make([]string, 0, 3)
	if in.URL != "" {
		parts = append(parts, "地址："+normalizeURL(in.URL))
	}
	if in.Name != "" {
		parts = append(parts, "名称："+in.Name)
	}
	if in.Description != "" {
		parts = append(parts, "简介："+in.Description)
	}
	return strings.Join(parts, "；")
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
	category := strings.TrimSpace(parsed.Category)
	if category == "" {
		return aiSuggestResult{}, errors.New("AI 未给出分类，请补充站点信息后重试")
	}
	if utf8.RuneCountInString(category) > linkCatMaxLen {
		category = string([]rune(category)[:linkCatMaxLen])
	}
	tags := cleanTags(parsed.Tags)
	if len(tags) == 0 {
		tags = []string{}
	}
	return aiSuggestResult{Category: category, Tags: tags}, nil
}

// suggestViaAI 调用主进程 AI 生成分类标签建议（连接器：经数据服务路由，用量计入站点统计）。
func suggestViaAI(ctx context.Context, svc sdk.DataService, model string, in LinkInput) (aiSuggestResult, error) {
	content := buildAIContent(in)
	if content == "" {
		return aiSuggestResult{}, errors.New("站点信息为空")
	}
	text, err := svc.GenerateAI(ctx, model, aiPromptTemplate, content)
	if err != nil {
		return aiSuggestResult{}, errors.New("AI 生成失败：" + err.Error())
	}
	return parseAISuggest(text)
}
