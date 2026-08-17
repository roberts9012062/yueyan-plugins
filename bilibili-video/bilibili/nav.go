// cmd/bilibili-video-plugin/bilibili/nav.go
// B 站导航接口（nav）：登录态校验 + 用户资料。
// 说明：播放地址统一走 html5 端点（免 WBI 签名），本文件不再承担 WBI 密钥职责。
package bilibili

import (
	"encoding/json"
	"net/http"
)

// navData nav 接口 data 结构（仅取所需字段）。
type navData struct {
	IsLogin bool   `json:"isLogin"`
	Mid     int64  `json:"mid"`
	Uname   string `json:"uname"`
	Face    string `json:"face"` // 头像 URL
	VipStat int    `json:"vipStatus"` // 1 = 大会员
	LevelInfo struct {
		CurrentLevel int `json:"current_level"`
	} `json:"level_info"`
}

// navProfile 从 nav 数据提取登录资料（纯函数）。
func navProfile(d *navData) *Profile {
	return &Profile{
		Mid:      d.Mid,
		Nickname: d.Uname,
		Avatar:   d.Face,
		Vip:      d.VipStat == 1,
		Level:    d.LevelInfo.CurrentLevel,
	}
}

// fetchNav 调 nav 接口（extraCookies 为空时用站长会话 cookie）。
func (c *Client) fetchNav(extraCookies []*http.Cookie) (*navData, error) {
	raw, err := c.getJSON(apiBase+"/x/web-interface/nav", extraCookies)
	if err != nil {
		return nil, err
	}
	var d navData
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// NavProfile 校验站长登录态并取资料（未登录返回 -101 错误）。
func (c *Client) NavProfile() (*Profile, error) {
	d, err := c.fetchNav(nil)
	if err != nil {
		return nil, err
	}
	if !d.IsLogin {
		return nil, &APIError{Code: -101, Message: "登录态已失效"}
	}
	return navProfile(d), nil
}

// GuestNavProfile 校验给定 cookie 的登录态（扫码/短信/guest_token 解出的 cookie）。
func (c *Client) GuestNavProfile(guestCookies []*http.Cookie) (*Profile, error) {
	d, err := c.fetchNav(guestCookies)
	if err != nil {
		return nil, err
	}
	if !d.IsLogin {
		return nil, &APIError{Code: -101, Message: "B站登录态已失效，请重新扫码"}
	}
	return navProfile(d), nil
}
