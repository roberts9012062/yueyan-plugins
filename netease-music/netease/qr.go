// cmd/netease-music-plugin/netease/qr.go
// 网易云音乐扫码登录（weapi）：生成 unikey/二维码 → 轮询状态 → 校验并持久化登录态。
package netease

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// qrUnikeyResp 扫码 unikey 响应。
type qrUnikeyResp struct {
	Code   int    `json:"code"`
	UniKey string `json:"unikey"`
	QrURL  string `json:"qrurl"`
}

// QrUnikey 获取扫码登录 unikey 与二维码内容。
// 返回：unikey 轮询键；qrContent 二维码内容（URL，前端生成二维码或后端出图）。
func (c *Client) QrUnikey() (string, string, error) {
	body, err := c.WeapiRequestQR("/weapi/login/qrcode/unikey", map[string]any{"type": 1})
	if err != nil {
		return "", "", err
	}
	var resp qrUnikeyResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", err
	}
	if resp.Code != 200 || resp.UniKey == "" {
		return "", "", fmt.Errorf("获取二维码失败（code=%d）", resp.Code)
	}
	qrContent := resp.QrURL
	if qrContent == "" {
		qrContent = "https://music.163.com/login?codekey=" + resp.UniKey
	}
	return resp.UniKey, qrContent, nil
}

// randomCSRF 生成随机 32 位 hex（扫码 check 的 csrf_token；纯函数）。
// 说明：官网在 802 之后给 check 请求拼上随机 csrf_token，缺失会被网易云判为异常返回 8821。
func randomCSRF() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// QrCheck 查询扫码状态（800 过期/取消，801 待扫码，802 已扫码待确认，803 成功）。
// 说明：返回 803 时登录 cookie 已写入会话（后续 AccountProfile 校验并持久化）。
func (c *Client) QrCheck(unikey string) (int, error) {
	body, err := c.WeapiRequestQR("/weapi/login/qrcode/client/login?csrf_token="+randomCSRF(), map[string]any{"key": unikey, "type": 1})
	if err != nil {
		return 0, err
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	return resp.Code, nil
}

// AccountProfile 校验登录态并取用户信息（扫码 803 后调用）。
func (c *Client) AccountProfile() (*Profile, error) {
	body, err := c.WeapiRequest("/weapi/w/nuser/account/get", map[string]any{"csrf_token": ""})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Code    int `json:"code"`
		Profile struct {
			UserID    int64  `json:"userId"`
			Nickname  string `json:"nickname"`
			AvatarURL string `json:"avatarUrl"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, fmt.Errorf("登录态校验失败（code=%d）", resp.Code)
	}
	return &Profile{UserID: resp.Profile.UserID, Nickname: resp.Profile.Nickname, AvatarURL: resp.Profile.AvatarURL}, nil
}

// QrLoginSuccess 扫码成功后校验登录态并持久化（cookie + profile）。
func (c *Client) QrLoginSuccess() (*Profile, error) {
	profile, err := c.AccountProfile()
	if err != nil {
		return nil, err
	}
	if err := c.UpdateSession(c.Cookies(), profile.Nickname, profile.UserID, profile.AvatarURL); err != nil {
		return nil, fmt.Errorf("登录态保存失败: %w", err)
	}
	return profile, nil
}
