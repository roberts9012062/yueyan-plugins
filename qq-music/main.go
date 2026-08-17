// cmd/qq-music-plugin/main.go
// QQ 音乐插件（进程外）：站长扫码登录 QQ 音乐 → 获取歌曲真实播放地址 → 前端自研播放器播放。
//
// 能力：api（自定义 API）+ frontend（前端扩展）+ settings + admin.page（后台登录页）。
// 登录：原生 ptlogin2 二维码扫码登录（xlogin → ptqrshow → ptqrlogin → OAuth），
//       另有粘贴 cookie 导入作备用；登录态（uin + musickey）AES 加密持久化到
//       插件数据目录 data/plugins/qq-music/state.json。
//
// 播放地址经 musicu.fcg 的 CgiGetVkey 获取（vkey 拼装直链，有时效）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"

	qq "github.com/roberts9012062/yueyan-plugins/qq-music/qq"
)

// pluginID 插件唯一 ID（与清单一致）。
const pluginID = "qq-music"

// QQMusicPlugin QQ 音乐插件实现（进程外）。
type QQMusicPlugin struct {
	mu     sync.Mutex
	client *qq.Client
}

// Info 插件信息（与商城清单一致；能力 + 设置项）。
func (p *QQMusicPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:           pluginID,
		Name:         "QQ 音乐",
		Version:      "0.2.0",
		Author:       "月言官方",
		Description:  "QQ 音乐嵌入播放：站长登录 QQ 音乐，插件获取真实播放地址，自研播放器双主题播放。",
		Capabilities: []string{"hooks", "api", "frontend", "settings", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "default_quality", Label: "音质", Type: "select", Default: "standard", Options: []string{"standard", "higher"}},
		},
	}
}

// OnActivate 启用回调：创建客户端并恢复登录态。
func (p *QQMusicPlugin) OnActivate(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(wd, "data", "plugins", pluginID)
	client, err := qq.NewClient(dir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
	return nil
}

// OnDeactivate 停用回调：释放客户端。
func (p *QQMusicPlugin) OnDeactivate(ctx context.Context) error {
	p.mu.Lock()
	p.client = nil
	p.mu.Unlock()
	return nil
}

// Hooks 订阅钩子（本插件无同步钩子需求）。
func (p *QQMusicPlugin) Hooks() []sdk.Hook { return nil }

// jsonResp 便捷构造 JSON 响应（纯函数）。
func jsonResp(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

// clientSafe 取当前客户端（未激活时返回 nil）。
func (p *QQMusicPlugin) clientSafe() *qq.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client
}

// RegisterAPI 自定义 API：登录/登出/状态/搜索/播放地址。
func (p *QQMusicPlugin) RegisterAPI(api *sdk.APIMux) {
	// 登录（body: {cookie}，导入 y.qq.com 复制的 cookie）
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
			Cookie string `json:"cookie"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Cookie == "" {
			return 400, jsonResp(map[string]any{"error": "参数错误：cookie 不能为空"}), nil
		}
		if err := c.ImportCookie(req.Cookie); err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"ok": true, "uin": c.State().Uin}), nil
	})

	// 歌单列表（登录用户创建的歌单）
	api.Handle("GET", "/playlists", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		playlists, err := c.Playlists()
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"playlists": playlists}), nil
	})

	// 歌单内歌曲（body: {tid}）
	api.Handle("POST", "/playlist-songs", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Tid string `json:"tid"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Tid == "" {
			return 400, jsonResp(map[string]any{"error": "缺少歌单 tid"}), nil
		}
		songs, err := c.PlaylistSongs(req.Tid)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"songs": songs}), nil
	})

	// 首页背景音乐设置（GET 读取 / POST 保存 body: {enabled, playlist_tid}）
	api.Handle("GET", "/bgm-settings", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		s := c.BgmSettings()
		return 200, jsonResp(map[string]any{"enabled": s.Enabled, "playlist_tid": s.PlaylistTid}), nil
	})

	api.Handle("POST", "/bgm-settings", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		// P1 加固：站点级管理操作（登录导入/登出/配置写入）仅管理员或宿主系统桥接可调用
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Enabled     *bool  `json:"enabled"`
			PlaylistTid string `json:"playlist_tid"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return 400, jsonResp(map[string]any{"error": "参数错误"}), nil
		}
		s := c.BgmSettings()
		if req.Enabled != nil {
			s.Enabled = *req.Enabled
		}
		if req.PlaylistTid != "" {
			s.PlaylistTid = req.PlaylistTid
		}
		if err := c.SaveBgmSettings(s); err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"ok": true, "enabled": s.Enabled, "playlist_tid": s.PlaylistTid}), nil
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
		_ = c.Logout()
		return 200, jsonResp(map[string]any{"ok": true}), nil
	})

	// 状态（无参数 GET）
	api.Handle("GET", "/status", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 200, jsonResp(map[string]any{"logged_in": false}), nil
		}
		st := c.State()
		return 200, jsonResp(map[string]any{"logged_in": st.LoggedIn, "uin": st.Uin}), nil
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

	// 扫码登录：初始化（获取二维码 + qrsig）
	api.Handle("POST", "/qr-init", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		// P1 加固：站点级管理操作（登录导入/登出/配置写入）仅管理员或宿主系统桥接可调用
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		qrsig, qrPNG, err := c.QrInit()
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"qrsig": qrsig, "qr_png": qrPNG}), nil
	})

	// 扫码登录：轮询状态（body: {qrsig}；0=成功，66/67 待扫，65 过期）
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
			Qrsig string `json:"qrsig"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Qrsig == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 qrsig"}), nil
		}
		code, redirectURL, err := c.QrCheck(req.Qrsig)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		if code == 0 && redirectURL != "" {
			// 登录成功：走 OAuth 换取登录态
			if err := c.QrOAuth(redirectURL); err != nil {
				return 200, jsonResp(map[string]any{"code": 0, "error": "登录态获取失败：" + err.Error()}), nil
			}
		}
		return 200, jsonResp(map[string]any{"code": code, "redirect_url": redirectURL}), nil
	})

	// 歌曲详情（body: {songmid}；歌词+搜索两步取歌名/歌手/封面）
	api.Handle("POST", "/song", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			SongMID string `json:"songmid"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.SongMID == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 songmid"}), nil
		}
		song, err := c.SongDetail(req.SongMID)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"song": song}), nil
	})

	// 播放地址（body: {songmid}）
	api.Handle("POST", "/song-url", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			SongMID string `json:"songmid"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.SongMID == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 songmid"}), nil
		}
		u, err := c.SongURL(req.SongMID)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"url": u.URL}), nil
	})

	// ---------- E7 音乐源标准契约端点（宿主 /music/:provider/* 桥接调用，system 身份） ----------

	// 播放地址（body: {src}；src= songmid —— 与 /song-url 同逻辑的契约别名）
	api.Handle("POST", "/music/url", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Src string `json:"src"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Src == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 src"}), nil
		}
		u, err := c.SongURL(req.Src)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"url": u.URL}), nil
	})

	// 背景音乐聚合（配置 + 歌单歌曲一次返回；关闭或未配置时 songs 为空）
	api.Handle("GET", "/music/bgm", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		c := p.clientSafe()
		if c == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		s := c.BgmSettings()
		out := map[string]any{"enabled": s.Enabled, "playlist_tid": s.PlaylistTid, "songs": []any{}}
		if s.Enabled && s.PlaylistTid != "" {
			if songs, err := c.PlaylistSongs(s.PlaylistTid); err == nil && songs != nil {
				out["songs"] = songs
			}
		}
		return 200, jsonResp(out), nil
	})
}

// main 插件进程入口。
func main() {
	fmt.Fprintln(os.Stderr, "[qq-music] 进程启动")
	server.Serve(&QQMusicPlugin{})
}
