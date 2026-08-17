// cmd/qq-music-plugin/qq/oauth_test.go
// OAuth 辅助纯函数单元测试：gtk33 / extractCode / uuidV4。
package qq

import (
	"regexp"
	"testing"
)

// TestGtk33 g_tk 计算（初始 5381；空串为 5381；真实 p_skey 回归值）。
func TestGtk33(t *testing.T) {
	if got := gtk33(""); got != 5381 {
		t.Fatalf("gtk33(空串) = %d，期望 5381", got)
	}
	// 官网抓包实测：g_tk=1607500505 对应 p_skey=Z9UChyggnzEQZUpjsZu6el6-AXC0DSlsW2x0cBilSGA_
	if got := gtk33("Z9UChyggnzEQZUpjsZu6el6-AXC0DSlsW2x0cBilSGA_"); got != 1607500505 {
		t.Fatalf("gtk33(p_skey) = %d，期望 1607500505", got)
	}
}

// TestExtractCode 从最终跳转 URL 提取授权码。
func TestExtractCode(t *testing.T) {
	if got := extractCode("https://y.qq.com/portal/wx_redirect.html?login_type=1&surl=https%3A%2F%2Fy.qq.com%2F&code=8F042A6B&state=state"); got != "8F042A6B" {
		t.Fatalf("extractCode = %q，期望 8F042A6B", got)
	}
	if got := extractCode("https://graph.qq.com/oauth2.0/show?which=Login"); got != "" {
		t.Fatalf("extractCode 无 code 时应为空，实际 %q", got)
	}
}

// TestUuidV4 UUID v4 格式（8-4-4-4-12 十六进制）。
func TestUuidV4(t *testing.T) {
	re := regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
	u := uuidV4()
	if !re.MatchString(u) {
		t.Fatalf("uuidV4() = %q 不符合 v4 格式", u)
	}
	if uuidV4() == u {
		t.Fatalf("uuidV4 两次结果相同，随机性异常")
	}
}
