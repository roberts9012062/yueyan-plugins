// cmd/bilibili-video-plugin/api_video.go
// 自定义 API · 视频端点与游客登录端点：
//   - POST /resolve       编辑器解析 B 站地址 → 视频信息 + 清晰度档位（登录编辑者调用）
//   - POST /video/url     播放地址解析（公开桥接 System 或登录用户；guest_token 优先）
//   - POST /guest-qr-check 游客前台扫码轮询（成功签发 guest_token）
//   - POST /guest-status   游客 token 有效性查询（前端刷新菜单用）
package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/roberts9012062/yueyan-plugins/bilibili-video/bilibili"
	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// bvPattern BV 号模式（BV + 10 位字母数字）。
var bvPattern = regexp.MustCompile(`BV[0-9A-Za-z]{10}`)

// registerVideoAPI 注册视频与游客端点。
func (p *BilibiliPlugin) registerVideoAPI(api *sdk.APIMux) {
	// 编辑器解析：body {url}（支持完整网页地址 / 纯 BV 号 / b23.tv 短链）
	api.Handle("POST", "/resolve", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.URL) == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 url"}), nil
		}
		bvid := extractBVID(c, req.URL)
		if bvid == "" {
			return 200, jsonResp(map[string]any{"error": "未识别到 B 站视频地址（需含 BV 号）"}), nil
		}
		info, err := c.View(bvid)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		loggedIn, _ := c.State()
		return 200, jsonResp(map[string]any{
			"video":           info,
			"qualities":       qualityTable(),
			"admin_logged_in": loggedIn,
		}), nil
	})

	// 播放地址：body {bvid, cid, qn, guest_token?}；cookie 优先级 guest_token > 站长（开关允许时）> 匿名
	api.Handle("POST", "/video/url", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Bvid       string `json:"bvid"`
			Cid        int64  `json:"cid"`
			Qn         int    `json:"qn"`
			GuestToken string `json:"guest_token"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Bvid == "" || req.Cid == 0 {
			return 400, jsonResp(map[string]any{"error": "缺少 bvid / cid"}), nil
		}
		if req.Qn != bilibili.QN360 && req.Qn != bilibili.QN480 && req.Qn != bilibili.QN720 && req.Qn != bilibili.QN1080 {
			req.Qn = bilibili.QN480
		}
		info, source, err := p.resolvePlay(c, ctx, req.Bvid, req.Cid, req.Qn, req.GuestToken)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{
			"quality": info.Quality, "quality_desc": bilibili.QualityDesc(info.Quality),
			"durl": info.Durl, "dash": info.Dash, "timelength": info.Timelength, "source": source,
		}), nil
	})

	// 游客扫码轮询：body {qrcode_key, session_token}；成功签发 guest_token（cookie 不落盘）
	api.Handle("POST", "/guest-qr-check", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
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
		if result.Code != bilibili.QrCodeSuccess {
			return 200, jsonResp(map[string]any{"code": result.Code, "message": result.Message}), nil
		}
		// 无条件签发 guest_token（扫码 cookie 即凭证）；nav 资料尽力而为——
		// 带登录 cookie 的 nav 请求可能被 B 站风控临时拦截，不应阻塞签发
		nickname := ""
		vip := false
		if profile, err := c.GuestNavProfile(result.Cookies); err == nil {
			nickname = profile.Nickname
			vip = profile.Vip
		}
		token, err := bilibili.SealGuestToken(result.Cookies, nickname)
		if err != nil {
			return 200, jsonResp(map[string]any{"code": 0, "error": "token 签发失败"}), nil
		}
		logf("游客扫码登录成功（资料校验：%v）", nickname != "")
		return 200, jsonResp(map[string]any{
			"code": 0, "guest_token": token,
			"nickname": nickname, "vip": vip,
		}), nil
	})

	// 游客 token 有效性：body {guest_token}
	api.Handle("POST", "/guest-status", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			GuestToken string `json:"guest_token"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.GuestToken == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 guest_token"}), nil
		}
		cookies, nickname, err := bilibili.OpenGuestToken(req.GuestToken)
		if err != nil {
			return 200, jsonResp(map[string]any{"valid": false, "message": err.Error()}), nil
		}
		if _, err := c.GuestNavProfile(cookies); err != nil {
			return 200, jsonResp(map[string]any{"valid": false, "message": "B站登录态已失效，请重新扫码"}), nil
		}
		return 200, jsonResp(map[string]any{"valid": true, "nickname": nickname}), nil
	})
}

// RegisterAPI 自定义 API 总注册（登录类 + 视频类）。
func (p *BilibiliPlugin) RegisterAPI(api *sdk.APIMux) {
	p.registerLoginAPI(api)
	p.registerVideoAPI(api)
}

// resolvePlay 解析播放地址（cookie 三级降级：guest_token > 站长会话 > 匿名）。
// 返回：播放信息 + 实际使用的来源标识（guest / admin / anonymous）。
func (p *BilibiliPlugin) resolvePlay(c *bilibili.Client, ctx context.Context, bvid string, cid int64, qn int, guestToken string) (*bilibili.PlayInfo, string, error) {
	// 1. 游客自己的 B 站账号（token 失效自动降级）
	if guestToken != "" {
		if cookies, _, err := bilibili.OpenGuestToken(guestToken); err == nil {
			if info, err := c.PlayURL(bvid, cid, qn, cookies); err == nil {
				return info, "guest", nil
			}
		}
	}
	// 2. 站长账号（设置项 allow_guest_hd 控制游客是否可借用）
	cfg := sdk.Config(ctx)
	if cfg["allow_guest_hd"] != "off" {
		if adminCookies := c.SessionCookies(); len(adminCookies) > 0 {
			if info, err := c.PlayURL(bvid, cid, qn, adminCookies); err == nil {
				return info, "admin", nil
			}
		}
	}
	// 3. 匿名（B 站自动降级到匿名可达清晰度）
	info, err := c.PlayURL(bvid, cid, qn, nil)
	return info, "anonymous", err
}

// qualityTable 构造清晰度档位表（纯函数；四档标准档位 + 是否需登录标注）。
func qualityTable() []map[string]any {
	table := make([]map[string]any, 0, 4)
	for _, qn := range []int{bilibili.QN360, bilibili.QN480, bilibili.QN720, bilibili.QN1080} {
		table = append(table, map[string]any{
			"qn": qn, "desc": bilibili.QualityDesc(qn), "need_login": bilibili.NeedLogin(qn),
		})
	}
	return table
}

// extractBVID 从用户输入提取 BV 号（支持纯 BV / 网页地址 / b23.tv 短链展开）。
func extractBVID(c *bilibili.Client, input string) string {
	trimmed := strings.TrimSpace(input)
	if m := bvPattern.FindString(trimmed); m != "" {
		return m
	}
	if strings.Contains(trimmed, "b23.tv") {
		if final, err := c.ExpandShortLink(extractFirstURL(trimmed)); err == nil {
			return bvPattern.FindString(final)
		}
	}
	return ""
}

// extractFirstURL 从文本中提取第一个 http(s) URL（纯函数）。
func extractFirstURL(text string) string {
	start := strings.Index(text, "http")
	if start < 0 {
		return text
	}
	rest := text[start:]
	if end := strings.IndexAny(rest, " \t\r\n\"'<>"); end >= 0 {
		return rest[:end]
	}
	return rest
}
