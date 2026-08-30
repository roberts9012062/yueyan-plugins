// nav-links/pagemeta.go
// 页面信息抓取：GET 站点首页，提取 <title>、meta description / og:description、
// 正文纯文本摘要——作为 AI 识别站点（名称/分类/标签/简介）的上下文。
// 中文站点存在 GBK/GB18030 编码，统一转 UTF-8 后再解析（golang.org/x/text）。
package main

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// 抓取限制常量。
const (
	metaHTMLMaxBytes = 512 << 10 // 首页 HTML 读取上限（只为提取元信息）
	metaDigestRunes  = 1200      // 正文摘要长度（rune；足够 AI 判断站点主题）
)

// pageMeta 页面元信息（AI 识别的输入上下文）。
type pageMeta struct {
	Title       string // <title> 文本
	Description string // meta description / og:description
	TextDigest  string // 正文纯文本摘要（去脚本样式标签后压空白）
}

// titlePattern 提取 <title>…</title>（含跨行；纯只读正则）。
var titlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// metaTagPattern 提取全部 <meta ...> 标签（再按 name/property 匹配 description）。
var metaTagPattern = regexp.MustCompile(`(?is)<meta\b[^>]*>`)

// metaAttrPattern 提取 meta 标签内属性（name / property / content）。
var metaAttrPattern = regexp.MustCompile(`(?is)(name|property|content)\s*=\s*["']([^"']*)["']`)

// scriptBlockPattern / styleBlockPattern / noscriptBlockPattern
// 去除脚本、样式、noscript 块（含内容整体剔除；RE2 不支持反向引用，逐块声明）。
var (
	scriptBlockPattern   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	styleBlockPattern    = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	noscriptBlockPattern = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript\s*>`)
)

// tagPattern 去除其余 HTML 标签（保留文本）。
var tagPattern = regexp.MustCompile(`(?s)<[^>]+>`)

// decodeHTMLBytes 按页面编码声明解码为 UTF-8 字符串（GBK/GB18030 常见中文站；纯函数）。
// 说明：页面声明 charset=gbk/gb2312/gb18030 时按 GB18030（向下兼容）解码，解码失败回退原字节；
// 其余（UTF-8 或无声明）直接返回原字节——UTF-8 站点无需转换。
// 不用 utf8.Valid 做前置判断：GBK 双字节序列可能碰巧构成合法 UTF-8，反之亦然，以声明为准。
func decodeHTMLBytes(data []byte) string {
	// 头部嗅探：剥除引号后匹配（兼容 charset="gbk" / charset=gbk / 单引号三种写法）
	head := strings.ToLower(string(data[:min(len(data), 2048)]))
	head = strings.NewReplacer(`"`, "", `'`, "").Replace(head)
	isGBK := strings.Contains(head, "charset=gbk") || strings.Contains(head, "charset=gb2312") || strings.Contains(head, "charset=gb18030")
	if isGBK {
		if out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
			return string(out)
		}
	}
	return string(data)
}

// extractMetaDescription 从全部 meta 标签中提取 description（name= 优先，og:description 兜底；纯函数）。
func extractMetaDescription(htmlText string) string {
	nameDesc := ""
	ogDesc := ""
	for _, tag := range metaTagPattern.FindAllString(htmlText, 40) {
		attrs := make(map[string]string)
		for _, m := range metaAttrPattern.FindAllStringSubmatch(tag, 3) {
			attrs[strings.ToLower(m[1])] = strings.TrimSpace(m[2])
		}
		content := attrs["content"]
		if content == "" {
			continue
		}
		key := strings.ToLower(attrs["name"] + " " + attrs["property"])
		switch {
		case strings.Contains(key, "description") && !strings.Contains(key, "og:"):
			if nameDesc == "" {
				nameDesc = content
			}
		case strings.Contains(key, "og:description"):
			if ogDesc == "" {
				ogDesc = content
			}
		}
	}
	if nameDesc != "" {
		return nameDesc
	}
	return ogDesc
}

// extractTextDigest 抽取正文纯文本摘要（去 script/style/标签 → 压空白 → 截断；纯函数）。
func extractTextDigest(htmlText string) string {
	text := scriptBlockPattern.ReplaceAllString(htmlText, " ")
	text = styleBlockPattern.ReplaceAllString(text, " ")
	text = noscriptBlockPattern.ReplaceAllString(text, " ")
	text = tagPattern.ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ") // 压缩连续空白与换行
	r := []rune(text)
	if len(r) > metaDigestRunes {
		r = r[:metaDigestRunes]
	}
	return string(r)
}

// parsePageMeta 从 HTML 文本解析元信息（纯函数）。
func parsePageMeta(htmlText string) pageMeta {
	title := ""
	if m := titlePattern.FindStringSubmatch(htmlText); m != nil {
		title = strings.Join(strings.Fields(strings.TrimSpace(m[1])), " ")
	}
	return pageMeta{
		Title:       title,
		Description: extractMetaDescription(htmlText),
		TextDigest:  extractTextDigest(htmlText),
	}
}

// fetchPageMeta 抓取站点首页并提取元信息（连接器；失败返回错误由调用方降级）。
func fetchPageMeta(pageURL string) (pageMeta, error) {
	normalized := normalizeURL(pageURL)
	data, ok := fetchBytes(normalized, metaHTMLMaxBytes)
	if !ok {
		return pageMeta{}, errors.New("页面抓取失败（站点不可达或拒绝访问）")
	}
	htmlText := decodeHTMLBytes(data)
	meta := parsePageMeta(htmlText)
	if meta.Title == "" && meta.Description == "" && meta.TextDigest == "" {
		return pageMeta{}, errors.New("页面无可识别内容（可能是 SPA 应用或纯接口）")
	}
	return meta, nil
}
