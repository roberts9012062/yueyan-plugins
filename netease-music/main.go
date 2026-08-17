// cmd/netease-music-plugin/main.go
// 网易云音乐插件（进程外）：站长登录网易云 → 获取歌曲真实播放地址 → 前端自研播放器播放。
//
// 能力：api（自定义 API）+ frontend（前端扩展）+ settings + admin.page（后台登录页）。
// 登录态 AES 加密持久化到插件数据目录 data/plugins/netease-music/state.json。
//
// 注意：主进程代理转发的 path 不含 query 参数（gin *path 通配仅取路径段），
//       因此带参数接口统一用 POST + JSON body（GET 仅用于无参的 /status）。
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"

	netease "github.com/roberts9012062/yueyan-plugins/netease-music/netease"
)

// pluginID 插件唯一 ID（与清单一致）。
const pluginID = "netease-music"

// NeteaseMusicPlugin 网易云音乐插件实现（进程外）。
type NeteaseMusicPlugin struct {
	mu     sync.Mutex
	client *netease.Client // 网易云 API 客户端（登录态）
}

// Info 插件信息（与商城清单一致；能力 + 设置项）。
func (p *NeteaseMusicPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:          pluginID,
		Name:        "网易云音乐",
		Version:     "0.1.0",
		Author:      "月言官方",
		Description: "网易云音乐嵌入播放：站长登录网易云，插件获取真实播放地址，自研播放器双主题播放。",
		Capabilities: []string{"hooks", "api", "frontend", "settings", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "autoplay", Label: "访客进入自动播放", Type: "switch", Default: "off"},
			{Key: "default_level", Label: "音质", Type: "select", Default: "standard", Options: []string{"standard", "higher", "exhigh"}},
		},
	}
}

// OnActivate 启用回调：创建客户端并恢复登录态。
func (p *NeteaseMusicPlugin) OnActivate(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(wd, "data", "plugins", pluginID)
	client, err := netease.NewClient(dir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
	return nil
}

// OnDeactivate 停用回调：释放客户端。
func (p *NeteaseMusicPlugin) OnDeactivate(ctx context.Context) error {
	p.mu.Lock()
	p.client = nil
	p.mu.Unlock()
	return nil
}

// Hooks 订阅钩子（本插件无同步钩子需求）。
func (p *NeteaseMusicPlugin) Hooks() []sdk.Hook { return nil }

// jsonResp 便捷构造 JSON 响应（纯函数）。
func jsonResp(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

// clientSafe 取当前客户端（未激活时返回 nil，调用方判空）。
func (p *NeteaseMusicPlugin) clientSafe() *netease.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client
}

// RegisterAPI 自定义 API：登录/登出/状态/搜索/详情/播放地址。
// 统一经主进程代理 /api/v1/plugins/netease-music/**（登录用户可用）。
func (p *NeteaseMusicPlugin) RegisterAPI(api *sdk.APIMux) {
	// 登录（body: {phone, password} 密码登录 或 {phone, captcha} 验证码登录）
	api.Handle("POST", "/login", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		// P1 加固：站点级管理操作（登录导入/登出/配置写入）仅管理员或宿主系统桥接可调用
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Phone    string `json:"phone"`
			Password string `json:"password"`
			Captcha  string `json:"captcha"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Phone == "" {
			return 400, jsonResp(map[string]any{"error": "参数错误：phone 不能为空"}), nil
		}
		var profile *netease.Profile
		var err error
		if req.Captcha != "" {
			profile, err = c.LoginWithCaptcha(req.Phone, req.Captcha)
		} else if req.Password != "" {
			profile, err = c.Login(req.Phone, req.Password)
		} else {
			return 400, jsonResp(map[string]any{"error": "参数错误：password 或 captcha 至少一个"}), nil
		}
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"profile": profile}), nil
	})

	// 发送登录验证码（body: {phone}；短信验证码登录第一步）
	api.Handle("POST", "/sms-send", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		// P1 加固：站点级管理操作（登录导入/登出/配置写入）仅管理员或宿主系统桥接可调用
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Phone string `json:"phone"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Phone == "" {
			return 400, jsonResp(map[string]any{"error": "缺少手机号 phone"}), nil
		}
		if err := c.SendSMS(req.Phone); err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"ok": true}), nil
	})

	// 登出（无参数）
	api.Handle("POST", "/logout", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		// P1 加固：站点级管理操作（登录导入/登出/配置写入）仅管理员或宿主系统桥接可调用
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 200, jsonResp(map[string]any{"ok": true}), nil
		}
		if err := c.Logout(); err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"ok": true}), nil
	})

	// 状态（无参数 GET；登录态 + 用户信息）
	api.Handle("GET", "/status", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 200, jsonResp(map[string]any{"logged_in": false}), nil
		}
		st := c.State()
		return 200, jsonResp(map[string]any{"logged_in": st.LoggedIn, "profile": st.Profile}), nil
	})

	// 扫码登录：获取二维码（返回 unikey + PNG 二维码 data URL）
	api.Handle("POST", "/qr-unikey", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		// P1 加固：站点级管理操作（登录导入/登出/配置写入）仅管理员或宿主系统桥接可调用
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		unikey, qrContent, err := c.QrUnikey()
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		png, err := qrcode.Encode(qrContent, qrcode.Medium, 256)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": "二维码生成失败"}), nil
		}
		return 200, jsonResp(map[string]any{
			"unikey": unikey,
			"qr_png": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		}), nil
	})

	// 扫码登录：轮询状态（body: {unikey}；返回 code + 成功时 profile）
	api.Handle("POST", "/qr-check", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		// P1 加固：站点级管理操作（登录导入/登出/配置写入）仅管理员或宿主系统桥接可调用
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Unikey string `json:"unikey"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Unikey == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 unikey"}), nil
		}
		code, err := c.QrCheck(req.Unikey)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		if code == 803 {
			profile, err := c.QrLoginSuccess()
			if err != nil {
				return 200, jsonResp(map[string]any{"code": 803, "error": err.Error()}), nil
			}
			return 200, jsonResp(map[string]any{"code": 803, "profile": profile}), nil
		}
		return 200, jsonResp(map[string]any{"code": code}), nil
	})

	// 搜索（body: {q, limit}）
	api.Handle("POST", "/search", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Q     string `json:"q"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Q == "" {
			return 400, jsonResp(map[string]any{"error": "缺少关键词 q"}), nil
		}
		songs, err := c.Search(req.Q, req.Limit)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"songs": songs}), nil
	})

	// 歌曲详情（body: {id}）
	api.Handle("POST", "/song", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		id := int64FromBody(body, "id")
		if id <= 0 {
			return 400, jsonResp(map[string]any{"error": "缺少歌曲 id"}), nil
		}
		song, err := c.SongDetail(id)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"song": song}), nil
	})

	// 播放地址（body: {id}）
	api.Handle("POST", "/song-url", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		id := int64FromBody(body, "id")
		if id <= 0 {
			return 400, jsonResp(map[string]any{"error": "缺少歌曲 id"}), nil
		}
		// 音质读插件配置（未配置默认 standard）
		level := "standard"
		if cfg := sdk.Config(ctx); cfg["default_level"] != "" {
			level = cfg["default_level"]
		}
		u, err := c.SongURL(id, level)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		if u.URL == "" {
			return 200, jsonResp(map[string]any{"error": "该歌曲无版权或需会员，无法获取播放地址"}), nil
		}
		return 200, jsonResp(map[string]any{"url": u.URL, "expi": u.Expi}), nil
	})

	// ---------- E7 音乐源标准契约端点（宿主 /music/:provider/* 桥接调用，system 身份） ----------
	// 播放地址（body: {src}；src= 歌曲 id —— 与 /song-url 同逻辑的契约别名）
	api.Handle("POST", "/music/url", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		src := int64FromBody(body, "src")
		if src <= 0 {
			return 400, jsonResp(map[string]any{"error": "缺少 src"}), nil
		}
		// 音质读插件配置（未配置默认 standard；与 /song-url 一致）
		level := "standard"
		if cfg := sdk.Config(ctx); cfg["default_level"] != "" {
			level = cfg["default_level"]
		}
		u, err := c.SongURL(src, level)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		if u.URL == "" {
			return 200, jsonResp(map[string]any{"error": "该歌曲无版权或需会员，无法获取播放地址"}), nil
		}
		return 200, jsonResp(map[string]any{"url": u.URL, "expi": u.Expi}), nil
	})
}

// int64FromBody 从 JSON body 提取 int64 字段（纯函数；key 如 "id"）。
func int64FromBody(body []byte, key string) int64 {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case string:
		id, _ := strconv.ParseInt(v, 10, 64)
		return id
	default:
		return 0
	}
}

// main 插件进程入口。
func main() {
	fmt.Fprintln(os.Stderr, "[netease-music] 进程启动")
	server.Serve(&NeteaseMusicPlugin{})
}
