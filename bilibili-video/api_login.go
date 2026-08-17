// cmd/bilibili-video-plugin/api_login.go
// 自定义 API · 站长登录类端点（后台登录页调用，经宿主代理需登录）：
// 扫码初始化/轮询、短信发送/登录、cookie 导入、登出、状态查询。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/roberts9012062/yueyan-plugins/bilibili-video/bilibili"
	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// registerLoginAPI 注册站长登录类端点。
func (p *BilibiliPlugin) registerLoginAPI(api *sdk.APIMux) {
	// 扫码初始化（无参数；返回轮询凭证 + 二维码 + 会话 token）
	api.Handle("POST", "/qr-init", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		init, err := c.QrInit()
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		png, err := qrPNGDataURL(init.QRURL)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": "二维码生成失败"}), nil
		}
		// generate 阶段会话 cookie 加密封装为 session_token 回传前端（无状态设计）
		sessionToken, _ := bilibili.SealGuestToken(init.Cookies, "")
		return 200, jsonResp(map[string]any{
			"qrcode_key": init.QrcodeKey, "qr_png": png, "session_token": sessionToken,
		}), nil
	})

	// 扫码轮询（body: {qrcode_key, session_token}；86101 待扫 / 86090 已扫 / 86038 过期 / 0 成功）
	api.Handle("POST", "/qr-check", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			QrcodeKey    string `json:"qrcode_key"`
			SessionToken string `json:"session_token"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.QrcodeKey == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 qrcode_key"}), nil
		}
		sessionCookies, _, _ := bilibili.OpenGuestToken(req.SessionToken)
		result, err := c.QrCheck(req.QrcodeKey, sessionCookies)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		if result.Code == bilibili.QrCodeSuccess {
			// 无条件保存登录态（扫码 cookie 即凭证）；nav 资料校验尽力而为——
			// 带登录 cookie 的 nav 请求可能被 B 站风控临时拦截，不应阻塞保存
			// （资料由 /status 懒刷新补全）
			profile, _ := c.GuestNavProfile(result.Cookies)
			if profile == nil {
				profile = &bilibili.Profile{}
			}
			if err := c.UpdateSession(result.Cookies, profile); err != nil {
				return 200, jsonResp(map[string]any{"code": 0, "error": "登录态保存失败：" + err.Error()}), nil
			}
			logf("站长扫码登录成功（资料校验：%v）", profile.Nickname != "")
		}
		return 200, jsonResp(map[string]any{"code": result.Code, "message": result.Message}), nil
	})

	// 短信发送（body: {tel, country_id}；风控时返回 need_captcha=true）
	api.Handle("POST", "/sms-send", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Tel       string `json:"tel"`
			CountryID int    `json:"country_id"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Tel == "" {
			return 400, jsonResp(map[string]any{"error": "缺少手机号 tel"}), nil
		}
		result, _ := c.SendSMS(req.Tel, req.CountryID, nil)
		return 200, jsonResp(map[string]any{
			"ok": result.OK, "need_captcha": result.NeedCaptcha, "message": result.Message,
		}), nil
	})

	// 短信登录（body: {tel, code, country_id}）
	api.Handle("POST", "/sms-login", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Tel       string `json:"tel"`
			Code      string `json:"code"`
			CountryID int    `json:"country_id"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Tel == "" || req.Code == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 tel / code"}), nil
		}
		result, _ := c.SMSLogin(req.Tel, req.Code, req.CountryID, nil)
		if !result.OK {
			return 200, jsonResp(map[string]any{
				"error": result.Message, "need_captcha": result.NeedCaptcha,
			}), nil
		}
		// 同扫码：无条件保存，nav 资料尽力而为（/status 懒刷新补全）
		profile, _ := c.GuestNavProfile(result.Cookies)
		if profile == nil {
			profile = &bilibili.Profile{}
		}
		if err := c.UpdateSession(result.Cookies, profile); err != nil {
			return 200, jsonResp(map[string]any{"error": "登录态保存失败：" + err.Error()}), nil
		}
		logf("站长短信登录成功")
		return 200, jsonResp(map[string]any{"ok": true}), nil
	})

	// cookie 导入（body: {cookie}；粘贴浏览器复制的 SESSDATA 等，备用通道）
	api.Handle("POST", "/cookie-login", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Cookie string `json:"cookie"`
		}
		if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Cookie) == "" {
			return 400, jsonResp(map[string]any{"error": "参数错误：cookie 不能为空"}), nil
		}
		cookies := parseCookieHeader(req.Cookie)
		if cookies == nil {
			return 200, jsonResp(map[string]any{"error": "未识别到有效 cookie（需含 SESSDATA）"}), nil
		}
		profile, err := profileWithCookies(c, cookies)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		if err := c.UpdateSession(cookies, profile); err != nil {
			return 200, jsonResp(map[string]any{"error": "登录态保存失败：" + err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"ok": true, "nickname": profile.Nickname}), nil
	})

	// 登出（无参数）
	api.Handle("POST", "/logout", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 200, jsonResp(map[string]any{"ok": true}), nil
		}
		_ = c.ClearSession()
		return 200, jsonResp(map[string]any{"ok": true}), nil
	})

	// 状态查询（GET；返回登录态与资料；资料缺失时经 nav 懒刷新补全）
	api.Handle("GET", "/status", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 200, jsonResp(map[string]any{"logged_in": false}), nil
		}
		// 懒刷新：扫码时 nav 被风控拦而未取到资料的，此处补拉（失败静默——资料非必需）
		_ = c.EnsureProfile()
		loggedIn, profile := c.State()
		out := map[string]any{"logged_in": loggedIn}
		if loggedIn && profile != nil {
			out["profile"] = map[string]any{
				"mid": profile.Mid, "nickname": profile.Nickname,
				"avatar": profile.Avatar, "vip": profile.Vip, "level": profile.Level,
			}
		}
		return 200, jsonResp(out), nil
	})
}

// logf 插件日志便捷输出（stderr 经宿主重定向到 logs/plugins/bilibili-video.log）。
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[bilibili-video] "+format+"\n", args...)
}

// profileWithCookies 用给定 cookie 调 nav 校验登录并取资料（辅助函数）。
func profileWithCookies(c *bilibili.Client, cookies []*http.Cookie) (*bilibili.Profile, error) {
	return c.GuestNavProfile(cookies)
}

// parseCookieHeader 解析粘贴的 cookie 文本为 cookie 列表（纯函数；须含 SESSDATA）。
func parseCookieHeader(raw string) []*http.Cookie {
	var cookies []*http.Cookie
	for _, part := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: kv[0], Value: kv[1]})
	}
	for _, ck := range cookies {
		if ck.Name == "SESSDATA" {
			return cookies
		}
	}
	return nil
}
