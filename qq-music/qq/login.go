// cmd/qq-music-plugin/qq/login.go
// QQ 音乐登录态管理：导入 cookie（uin + musickey）/ 登出 / 状态查询。
// 说明：QQ 音乐无公开登录接口，站长在 y.qq.com 扫码登录后复制 cookie 粘贴导入。
package qq

import (
	"os"
	"strings"
)

// SessionState 会话状态（登录态 + 用户信息）。
type SessionState struct {
	LoggedIn bool   // 是否已登录
	Uin      string // QQ 号
}

// ImportCookie 导入 cookie 字符串（提取 uin + musickey + login_type 并持久化）。
// 参数：cookieStr 完整 cookie 字符串（如 uin=xxx; qm_keyst=xxx; login_type=1; ...）。
func (c *Client) ImportCookie(cookieStr string) error {
	// 解析 cookie 字符串为 map
	values := parseCookie(cookieStr)
	uin := values["uin"]
	if uin == "" {
		// 微信登录用 wxuin
		uin = values["wxuin"]
	}
	uin = strings.TrimSpace(uin)
	// uin 只保留数字
	uin = keepDigits(uin)
	if uin == "" {
		return errMissingUin
	}
	musickey := values["qm_keyst"]
	if musickey == "" {
		musickey = values["qqmusic_key"]
	}
	if musickey == "" {
		return errMissingMusickey
	}
	loginType := values["login_type"]

	c.mu.Lock()
	c.uin = uin
	c.musickey = musickey
	c.loginType = loginType
	c.mu.Unlock()
	return c.saveState(stateData{Uin: uin, Musickey: musickey, LoginType: loginType})
}

// Logout 登出并清除本地登录态。
func (c *Client) Logout() error {
	c.mu.Lock()
	c.uin = ""
	c.musickey = ""
	c.loginType = ""
	c.mu.Unlock()
	return os.Remove(c.statePath)
}

// State 返回当前登录态只读快照。
func (c *Client) State() SessionState {
	c.mu.Lock()
	uin := c.uin
	c.mu.Unlock()
	return SessionState{LoggedIn: uin != "", Uin: uin}
}

// parseCookie 解析 cookie 字符串为 map（纯函数；分号分隔 key=value）。
func parseCookie(cookieStr string) map[string]string {
	values := make(map[string]string)
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		values[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return values
}

// keepDigits 提取字符串中的数字（纯函数；uin 清洗用）。
func keepDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// 登录态导入错误（缺少必要字段）。
var errMissingUin = errImport("cookie 缺少 uin（QQ 号）")
var errMissingMusickey = errImport("cookie 缺少 qm_keyst / qqmusic_key（登录密钥）")

// errImport 导入错误类型。
type errImport string

func (e errImport) Error() string { return string(e) }
