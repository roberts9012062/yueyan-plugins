// marketplace-repo/markdown-plus/enhance.go
// 正文增强函数（全部纯函数：不修改入参，返回新串；幂等——已增强的标签跳过）。
package main

import (
	"regexp"
	"strings"
	"unicode"
)

// 标签匹配模式（宽松匹配开标签整体，逐个回调判断处理）。
// 说明：Go RE2 不支持反向引用，闭合标题号用独立捕获组（组 4），
// 由回调校验与开标签号（组 1）一致才算命中。
var (
	anchorPattern  = regexp.MustCompile(`<a\s[^>]*>`)
	imagePattern   = regexp.MustCompile(`<img\s[^>]*>`)
	headingPattern = regexp.MustCompile(`(?s)<h([1-6])(\s[^>]*)?>(.*?)</h([1-6])>`)
	hrefPattern    = regexp.MustCompile(`href="(https?://[^"]*)"`)
)

// switchOn 开关设置判定（"on" 为开，其余一律关；纯函数）。
func switchOn(cfg map[string]string, key string) bool {
	return strings.TrimSpace(cfg[key]) == "on"
}

// hasAttr 标签串中是否已含指定属性名（幂等保护；纯函数）。
func hasAttr(tag string, attr string) bool {
	return strings.Contains(tag, attr+"=")
}

// enhanceExternalLinks 外链新窗口安全打开：给 http(s) 外链 a 标签补
// target="_blank" rel="noopener noreferrer"（已有 target 的跳过——尊重作者手写）。
func enhanceExternalLinks(content string) string {
	return anchorPattern.ReplaceAllStringFunc(content, func(tag string) string {
		if hasAttr(tag, "target") {
			return tag
		}
		if hrefPattern.FindStringSubmatch(tag) == nil {
			return tag // 站内相对链接：不动
		}
		return strings.TrimSuffix(tag, ">") + ` target="_blank" rel="noopener noreferrer">`
	})
}

// addLinkNofollow 外链加 nofollow（SEO 防权重流失；已有 rel 的合并追加）。
func addLinkNofollow(content string) string {
	return anchorPattern.ReplaceAllStringFunc(content, func(tag string) string {
		if hrefPattern.FindStringSubmatch(tag) == nil || strings.Contains(tag, "nofollow") {
			return tag
		}
		if idx := strings.Index(tag, "rel=\""); idx >= 0 {
			// 已有 rel：在值首插入 nofollow（保留其余值）
			return tag[:idx+5] + "nofollow " + tag[idx+5:]
		}
		return strings.TrimSuffix(tag, ">") + ` rel="nofollow">`
	})
}

// enhanceLazyImages 图片懒加载：img 标签补 loading="lazy" decoding="async"。
func enhanceLazyImages(content string) string {
	return imagePattern.ReplaceAllStringFunc(content, func(tag string) string {
		if hasAttr(tag, "loading") {
			return tag
		}
		return strings.TrimSuffix(tag, ">") + ` loading="lazy" decoding="async">`
	})
}

// addHeadingAnchors 标题锚点：h1-h6 补 id（标题文本 slug 化：字母数字保留、
// 其余折叠为连字符；重复 slug 追加序号保证同页唯一）。
func addHeadingAnchors(content string) string {
	used := make(map[string]int)
	return headingPattern.ReplaceAllStringFunc(content, func(tag string) string {
		groups := headingPattern.FindStringSubmatch(tag)
		// 组 1=开标签号、组 4=闭标签号：一致才是配对的标题标签（RE2 无反向引用）
		if len(groups) != 5 || groups[1] != groups[4] || strings.Contains(groups[2], "id=") {
			return tag // 未配对 / 已有锚点：跳过
		}
		text := stripTags(groups[3])
		slug := slugify(text)
		if slug == "" {
			return tag // 空标题（纯符号/图标）：无法生成有意义的锚点
		}
		if used[slug] > 0 {
			slug = slug + "-" + itoa(used[slug]+1)
		}
		used[slug]++
		open := "<h" + groups[1] + groups[2]
		return open + ` id="` + slug + `">` + groups[3] + "</h" + groups[1] + ">"
	})
}

// stripTags 去除串内全部 HTML 标签（锚点文本提取用；纯函数）。
func stripTags(s string) string {
	return regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
}

// slugify 文本 slug 化（Unicode 字母数字保留——中文标题可直接作锚点；
// 空白与符号折叠为单个连字符；纯函数）。
func slugify(text string) string {
	var b strings.Builder
	lastDash := true // 抑制首部连字符
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// itoa 十进制整数字符串（避免多处 strconv 导入；纯函数）。
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
