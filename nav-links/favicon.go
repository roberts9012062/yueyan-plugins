// nav-links/favicon.go
// 站点图标自动抓取：三步策略（根路径 favicon.ico → 首页 <link rel="icon"> 声明 →
// DuckDuckGo 图标服务兜底），成功后转 base64 dataURL 返回——前端内嵌展示，
// 不依赖任何外部网络资源（绕开防盗链与 CSP 限制）。
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
