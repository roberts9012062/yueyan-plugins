// cmd/netease-music-plugin/netease/login.go
// 网易云音乐登录/登出/状态查询（手机号+密码，eapi 加密）。
// 说明：手机号登录必须走 eapi 接口 /eapi/w/login/cellphone（weapi 版本现会触发 8821 行为验证码）。
package netease

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Profile 登录用户资料（脱敏展示用）。
type Profile struct {
	UserID    int64  `json:"user_id"`    // 用户 ID
	Nickname  string `json:"nickname"`   // 昵称
	AvatarURL string `json:"avatar_url"` // 头像
}

// loginResp 登录响应（eapi/weapi 通用，仅取用到的字段）。
type loginResp struct {
	Code    int    `json:"code"` // 200=成功
	Msg     string `json:"msg"`  // 部分接口用 msg 而非 message
	Message string `json:"message"`
	Profile struct {
		UserID    int64  `json:"userId"`
		Nickname  string `json:"nickname"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"profile"`
}

// md5Hex 计算字符串 md5 小写 hex（纯函数）。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// errMsg 取响应错误信息（msg 与 message 二选一，非空优先；纯函数）。
func errMsg(msg string, message string) string {
	if msg != "" {
		return msg
	}
	return message
}

// loginCellphone 手机号登录公共实现（eapi 加密；成功持久化 cookie）。
// params 需含 phone/countrycode/remember/type/https，且 password 与 captcha 至少一个。
func (c *Client) loginCellphone(params map[string]any) (*Profile, error) {
	body, err := c.EapiRequest("/eapi/w/login/cellphone", params)
	if err != nil {
		return nil, err
	}
	var resp loginResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("登录响应解析失败: %w", err)
	}
	if resp.Code != 200 {
		msg := errMsg(resp.Msg, resp.Message)
		if msg == "" {
			msg = fmt.Sprintf("登录失败（code=%d）", resp.Code)
		}
		return nil, fmt.Errorf("网易云登录失败：%s", msg)
	}
	profile := &Profile{UserID: resp.Profile.UserID, Nickname: resp.Profile.Nickname, AvatarURL: resp.Profile.AvatarURL}
	if err := c.UpdateSession(c.Cookies(), profile.Nickname, profile.UserID, profile.AvatarURL); err != nil {
		return nil, fmt.Errorf("登录态保存失败: %w", err)
	}
	return profile, nil
}

// Login 手机号+密码登录。
// 常见错误：509=密码错误、8821=需行为验证码、-460=风控。
func (c *Client) Login(phone string, password string) (*Profile, error) {
	return c.loginCellphone(map[string]any{
		"phone":       phone,
		"countrycode": "86",
		"password":    md5Hex(password),
		"remember":    "true",
		"type":        "1",
		"https":       "true",
	})
}

// LoginWithCaptcha 手机号+验证码登录（先 SendSMS 获取短信验证码）。
func (c *Client) LoginWithCaptcha(phone string, captcha string) (*Profile, error) {
	return c.loginCellphone(map[string]any{
		"phone":       phone,
		"countrycode": "86",
		"captcha":     captcha,
		"remember":    "true",
		"type":        "1",
		"https":       "true",
	})
}

// SendSMS 发送登录验证码（weapi /weapi/sms/captcha/sent）。
// 说明：secrete 为 PC 登录专用；验证码 24 小时内最多发送 5 次。
func (c *Client) SendSMS(phone string) error {
	body, err := c.WeapiRequest("/weapi/sms/captcha/sent", map[string]any{
		"cellphone": phone,
		"ctcode":    86,
		"secrete":   "music_middleuser_pclogin",
	})
	if err != nil {
		return err
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data bool   `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("验证码发送响应解析失败: %w", err)
	}
	if resp.Code != 200 || !resp.Data {
		msg := resp.Msg
		if msg == "" {
			msg = fmt.Sprintf("验证码发送失败（code=%d）", resp.Code)
		}
		return fmt.Errorf("验证码发送失败：%s", msg)
	}
	return nil
}

// Logout 登出并清除本地登录态。
func (c *Client) Logout() error {
	// 服务端登出（可选，失败不阻断本地清除）
	_, _ = c.WeapiRequest("/weapi/logout", map[string]any{"csrf_token": ""})
	return c.ClearSession()
}
