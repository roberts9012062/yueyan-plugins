// cmd/qq-music-plugin/qq/parse_test.go
// parsePtuiCB 纯函数单元测试：覆盖 ptqrlogin 的等待/已扫/成功/过期/转义/缺参响应。
package qq

import "testing"

// TestParsePtuiCB 各状态码与跳转 URL 解析。
func TestParsePtuiCB(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantCode int
		wantJump string
	}{
		{"等待扫码 66", "ptuiCB('66','0','','0','二维码未失效。','')", 66, ""},
		{"已扫待确认 67", "ptuiCB('67','0','','0','二维码认证中，请确认','')", 67, ""},
		{"过期 65（缺第6参数）", "ptuiCB('65','0','','0','二维码已失效。')", 65, ""},
		{"登录成功（URL 在第3参数）", "ptuiCB('0','0','https://ssl.ptlogin2.qq.com/jump?clientuin=123&key=abc','0','登录成功！','QQ音乐')", 0, "https://ssl.ptlogin2.qq.com/jump?clientuin=123&key=abc"},
		{"登录成功（HTML 转义 &amp;）", "ptuiCB('0','0','https://ssl.ptlogin2.qq.com/jump?a=1&amp;b=2','0','登录成功！','QQ音乐')", 0, "https://ssl.ptlogin2.qq.com/jump?a=1&b=2"},
		{"登录成功（check_sig 真实格式）", "ptuiCB('0','0','https://ssl.ptlogin2.graph.qq.com/check_sig?pttype=1&uin=250467554&service=ptqrlogin&nodirect=0&ptsigx=abc123&s_url=https%3A%2F%2Fgraph.qq.com%2Foauth2.0%2Flogin_jump&f_url=&ptlang=2052&ptredirect=100&aid=716027609&daid=383&j_later=0&low_login_hour=0&regmaster=0&pt_login_type=3&pt_aid=0&pt_aaid=16','0','登录成功！', '')", 0, "https://ssl.ptlogin2.graph.qq.com/check_sig?pttype=1&uin=250467554&service=ptqrlogin&nodirect=0&ptsigx=abc123&s_url=https%3A%2F%2Fgraph.qq.com%2Foauth2.0%2Flogin_jump&f_url=&ptlang=2052&ptredirect=100&aid=716027609&daid=383&j_later=0&low_login_hour=0&regmaster=0&pt_login_type=3&pt_aid=0&pt_aaid=16"},
		{"登录成功（尾随分号换行）", "ptuiCB('0','0','https://x.com/jump?a=1','0','登录成功！','QQ音乐');\n", 0, "https://x.com/jump?a=1"},
		{"非法响应", "not-a-ptui-callback", -1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, jump := parsePtuiCB(tc.raw)
			if code != tc.wantCode || jump != tc.wantJump {
				t.Fatalf("parsePtuiCB(%q) = (%d, %q)，期望 (%d, %q)", tc.raw, code, jump, tc.wantCode, tc.wantJump)
			}
		})
	}
}
