// cmd/bilibili-video-plugin/bilibili/qr.go
// B 站 web 扫码登录（passport web qrcode 体系）：
// generate 出二维码 URL + qrcode_key → 浏览器 B 站 App 扫码 → poll 轮询状态
// → 成功时响应 Set-Cookie 携带登录态（SESSDATA / bili_jct / DedeUserID / ac_time_value）。
//
// 扫码会话 cookie 由调用方持有（站长会话入 state；游客会话封装 guest_token），
// 本文件只负责协议交互，不落盘。
package bilibili

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// B 站扫码轮询码值（公开契约）。
const (
	QrCodeWaiting  = 86101 // 待扫码
	QrCodeScanned  = 86090 // 已扫码待确认
	QrCodeExpired  = 86038 // 二维码已过期
	QrCodeSuccess  = 0     // 登录成功
)

// QrInitResult 扫码初始化结果。
type QrInitResult struct {
	QrcodeKey string // 轮询凭证
	QRURL     string // 二维码内容（B 站登录 URL，前端本地渲染成图）
	Cookies   []*http.Cookie // generate 会话 cookie（poll 时回带保持会话一致）
}

// QrPollResult 轮询结果。
type QrPollResult struct {
	Code    int            // 码值（见 QrCode* 常量）
	Message string         // 提示文案
	Cookies []*http.Cookie // 成功时捕获的登录 cookie（Set-Cookie）
}

// qrGenerate 发起扫码初始化（纯协议层；返回轮询凭证与二维码内容）。
func (c *Client) qrGenerate() (*QrInitResult, error) {
	body, setCookies, err := c.doRequest(http.MethodGet, passportBase+"/x/passport-login/web/qrcode/generate", "", nil, nil)
	if err != nil {
		return nil, err
	}
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("B站响应解析失败")
	}
	if r.Code != 0 {
		return nil, &APIError{Code: r.Code, Message: r.Message}
	}
	var d struct {
		URL       string `json:"url"`
		QrcodeKey string `json:"qrcode_key"`
	}
	if err := json.Unmarshal(r.Data, &d); err != nil || d.QrcodeKey == "" {
		return nil, fmt.Errorf("二维码凭证获取失败")
	}
	return &QrInitResult{QrcodeKey: d.QrcodeKey, QRURL: d.URL, Cookies: setCookies}, nil
}

// qrPoll 轮询扫码状态（sessionCookies 为 generate 阶段的会话 cookie）。
// 成功（code=0）时从响应 Set-Cookie 捕获登录态。
func (c *Client) qrPoll(qrcodeKey string, sessionCookies []*http.Cookie) (*QrPollResult, error) {
	pollURL := passportBase + "/x/passport-login/web/qrcode/poll?qrcode_key=" + url.QueryEscape(qrcodeKey)
	body, setCookies, err := c.doRequest(http.MethodGet, pollURL, "", nil, sessionCookies)
	if err != nil {
		return nil, err
	}
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("B站响应解析失败")
	}
	// 轮询失败码（如 86038 过期）在 data.code，信封 code 恒 0
	var d struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		URL     string `json:"url"`
	}
	_ = json.Unmarshal(r.Data, &d)
	if d.Message == "" {
		d.Message = r.Message
	}
	result := &QrPollResult{Code: d.Code, Message: d.Message}
	if d.Code == QrCodeSuccess {
		if len(setCookies) == 0 {
			return nil, fmt.Errorf("登录成功但未捕获到 cookie")
		}
		result.Cookies = setCookies
	}
	return result, nil
}

// QrInit 站长/游客通用扫码初始化（对外入口）。
func (c *Client) QrInit() (*QrInitResult, error) {
	return c.qrGenerate()
}

// QrCheck 站长/游客通用扫码轮询（对外入口）。
func (c *Client) QrCheck(qrcodeKey string, sessionCookies []*http.Cookie) (*QrPollResult, error) {
	return c.qrPoll(qrcodeKey, sessionCookies)
}
