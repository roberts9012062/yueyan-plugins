// marketplace-repo/markdown-plus/enhance_test.go
// 增强函数单测：正则合法性（RE2 无反向引用）+ 幂等 + 基本改写行为。
package main

import (
	"strings"
	"testing"
)

// TestExternalLinks 外链增强：外链补属性、站内链接不动、已有 target 跳过。
func TestExternalLinks(t *testing.T) {
	in := `<p><a href="https://example.com/a">外链</a> <a href="/posts/1">站内</a> ` +
		`<a href="https://example.com/b" target="_top">已带</a></p>`
	out := enhanceExternalLinks(in)
	if !strings.Contains(out, `href="https://example.com/a" target="_blank" rel="noopener noreferrer"`) {
		t.Errorf("外链未补安全属性：%s", out)
	}
	if strings.Contains(out, `href="/posts/1" target`) {
		t.Errorf("站内链接被误改：%s", out)
	}
	if !strings.Contains(out, `target="_top"`) || strings.Contains(out, `_top" rel="noopener`) {
		t.Errorf("已有 target 的链接被重复处理：%s", out)
	}
}

// TestLazyImages 图片懒加载：补属性且幂等。
func TestLazyImages(t *testing.T) {
	in := `<img src="a.png">`
	out := enhanceLazyImages(in)
	if !strings.Contains(out, `loading="lazy" decoding="async"`) {
		t.Errorf("未补懒加载属性：%s", out)
	}
	if again := enhanceLazyImages(out); again != out {
		t.Errorf("非幂等：%s → %s", out, again)
	}
}

// TestHeadingAnchors 标题锚点：配对标题加 id、跨号不误伤、重复 slug 加序号。
func TestHeadingAnchors(t *testing.T) {
	in := "<h2>Hello World</h2><h3>你好世界</h3><h3>Hello World</h3>"
	out := addHeadingAnchors(in)
	if !strings.Contains(out, `<h2 id="hello-world">`) {
		t.Errorf("英文标题锚点缺失：%s", out)
	}
	if !strings.Contains(out, `<h3 id="你好世界">`) { // 中文标题直接作锚点（Unicode 字母保留）
		t.Errorf("中文标题锚点缺失：%s", out)
	}
	if !strings.Contains(out, `<h3 id="hello-world-2">`) {
		t.Errorf("重复 slug 未加序号：%s", out)
	}
}

// TestHeadingAnchorsIdempotent 已有 id 的标题跳过（幂等）。
func TestHeadingAnchorsIdempotent(t *testing.T) {
	in := `<h2 id="keep">Keep</h2>`
	if out := addHeadingAnchors(in); out != in {
		t.Errorf("已有 id 被改动：%s", out)
	}
}

// TestNofollow nofollow 追加：新 rel 与已有 rel 合并。
func TestNofollow(t *testing.T) {
	in := `<a href="https://example.com">a</a>`
	out := addLinkNofollow(in)
	if !strings.Contains(out, `rel="nofollow"`) {
		t.Errorf("未加 nofollow：%s", out)
	}
	if again := addLinkNofollow(out); strings.Count(again, "nofollow") != 1 {
		t.Errorf("nofollow 重复追加：%s", again)
	}
}
