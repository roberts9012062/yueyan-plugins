// nav-links/webfetch.go
// 站点网页抓取（两部分：§图标抓取 favicon / §页面元信息 pagemeta）。
// 插件进程侧统一 HTTP 出口：浏览器 UA、限长读取、魔数与编码校验——
// 图标转 base64 dataURL 内嵌存储，元信息作为 AI 识别站点的上下文。
package main


import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// 图标抓取限制常量。
const (
	iconMaxBytes      = 64 << 10  // 单个图标字节上限（64KB，favicon 足够）
	iconHTMLMaxBytes  = 256 << 10 // 首页 HTML 读取上限（只为找 link 标签）
	iconHTTPTimeout   = 5 * time.Second
	iconUserAgent     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	iconDuckHost      = "icons.duckduckgo.com"
)

// iconHTTPClient 图标抓取客户端（超时统一 5 秒，管理端操作可接受短等待）。
var iconHTTPClient = &http.Client{Timeout: iconHTTPTimeout}

// linkTagPattern 提取全部 <link ...> 标签（再逐个解析 rel/href；纯只读正则）。
var linkTagPattern = regexp.MustCompile(`(?i)<link\b[^>]*>`)

// relAttrPattern / hrefAttrPattern 提取标签内属性值（大小写不敏感）。
var (
	relAttrPattern  = regexp.MustCompile(`(?i)\brel\s*=\s*["']([^"']*)["']`)
	hrefAttrPattern = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']*)["']`)
)

// fetchFavicon 抓取站点图标并转 dataURL（纯流程函数）。
// 返回：dataURL、来源描述（favicon/page/duckduckgo，前端提示用）、错误。
func fetchFavicon(pageURL string) (string, string, error) {
	normalized := normalizeURL(pageURL)
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" {
		return "", "", errors.New("站点地址格式不正确")
	}
	origin := parsed.Scheme + "://" + parsed.Host

	// 步骤 1：根路径 favicon.ico（多数站点直接可取）
	if dataURL, ok := downloadIconAsDataURL(origin+"/favicon.ico"); ok {
		return dataURL, "favicon", nil
	}
	// 步骤 2：解析首页声明的图标地址（覆盖自定义路径/SVG 图标场景）
	for _, target := range parseIconLinks(origin) {
		if dataURL, ok := downloadIconAsDataURL(target); ok {
			return dataURL, "page", nil
		}
	}
	// 步骤 3：DuckDuckGo 图标服务兜底（域名哈希图标，覆盖无 favicon 的站点）
	duck := "https://" + iconDuckHost + "/ip3/" + parsed.Hostname() + ".ico"
	if dataURL, ok := downloadIconAsDataURL(duck); ok {
		return dataURL, "duckduckgo", nil
	}
	return "", "", errors.New("未找到可用图标（可稍后重试或留空使用首字母占位）")
}

// parseIconLinks 抓首页 HTML，提取声明的图标绝对地址（纯函数；按出现顺序）。
func parseIconLinks(origin string) []string {
	body, ok := fetchBytes(origin, iconHTMLMaxBytes)
	if !ok {
		return nil
	}
	// 只在 HTML 文本里找（JSON/二进制响应直接放弃）
	if !strings.Contains(strings.ToLower(string(body)), "<link") {
		return nil
	}
	out := make([]string, 0, 2)
	for _, tag := range linkTagPattern.FindAllString(string(body), 12) {
		relMatch := relAttrPattern.FindStringSubmatch(tag)
		hrefMatch := hrefAttrPattern.FindStringSubmatch(tag)
		if relMatch == nil || hrefMatch == nil {
			continue
		}
		rel := strings.ToLower(strings.TrimSpace(relMatch[1]))
		// 排除 mask-icon（mono 蒙版）与 preload；接受 icon/shortcut icon/apple-touch-icon
		if !strings.Contains(rel, "icon") || strings.Contains(rel, "mask") || strings.Contains(rel, "preload") {
			continue
		}
		href := strings.TrimSpace(hrefMatch[1])
		if href == "" {
			continue
		}
		// 相对地址基于站点根绝对化（协议相对 //https:、根相对 /、页相对 ./）
		base, err := url.Parse(origin)
		if err != nil {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil || ref.Scheme == "javascript" {
			continue
		}
		absolute := base.ResolveReference(ref)
		if absolute.Scheme != "http" && absolute.Scheme != "https" {
			continue
		}
		out = append(out, absolute.String())
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// fetchBytes GET 一个地址并限长读取（成功返回字节；纯网络函数）。
func fetchBytes(target string, limit int64) ([]byte, bool) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("User-Agent", iconUserAgent)
	resp, err := iconHTTPClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// sniffMime 按魔数探测图片 MIME（纯函数；ok=false 表示不是可识别的图片）。
func sniffMime(data []byte) (string, bool) {
	head := string(data[:min(len(data), 256)])
	switch {
	case strings.HasPrefix(head, "\x89PNG\r\n\x1a\n"):
		return "image/png", true
	case strings.HasPrefix(head, "\xff\xd8\xff"):
		return "image/jpeg", true
	case strings.HasPrefix(head, "GIF87a"), strings.HasPrefix(head, "GIF89a"):
		return "image/gif", true
	case len(data) >= 12 && strings.HasPrefix(head, "RIFF") && head[8:12] == "WEBP":
		return "image/webp", true
	case len(data) >= 4 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x01 && data[3] == 0x00:
		return "image/x-icon", true
	case strings.Contains(head, "<svg") || strings.HasPrefix(strings.TrimSpace(head), "<?xml"):
		return "image/svg+xml", true
	default:
		return "", false
	}
}

// downloadIconAsDataURL 下载图标并转 dataURL（校验大小与格式；纯流程函数）。
func downloadIconAsDataURL(target string) (string, bool) {
	data, ok := fetchBytes(target, iconMaxBytes+1)
	if !ok || len(data) == 0 || len(data) > iconMaxBytes {
		return "", false
	}
	mime, recognized := sniffMime(data)
	if !recognized {
		return "", false // 拒绝 HTML 登录页/JSON 错误页等伪图标
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), true
}

// ---------- §页面元信息（pagemeta：AI 识别站点的输入上下文） ----------

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
