// marketplace-repo/tg-image-bed/main.go
// TG图床插件（进程外，免费）：图片上传直达 Telegram 频道（Bot API），
// 访客经站长自备的反代 Worker 读取（Bot Token 不出服务端，浏览器永远拿不到）。
//
// 架构（配对三件套，同 image-cdn 插件模式）：
//   - Telegram Bot（@BotFather 创建并拉入频道为管理员，配置 Token + Chat ID）
//   - 反代 Worker（站长自行部署，见同目录 worker/ 参考实现：/f/{file_id} 持 token 反代 + 缓存）
//   - 本插件（插件设置填 Token + Chat ID + Worker 地址即用）
//
// 能力：settings（配对五项）+ api（上传/列表/删除 + storage 契约备用）+ admin.page（图库页）。
// 上传历史存 data/plugins/tg-image-bed/history.json（见 history.go）。
// 限制：Bot API getFile 下载上限 20MB（上传前置校验）；file_path 为临时链接，
// 每次访问由 Worker 实时 getFile 解析（对齐 telegraph-Image 的 cfile 机制）。
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与 plugin.json / yueyan-plugin.json 一致）。
const pluginID = "tg-image-bed"

// imageExtWhitelist 上传图片扩展名白名单（对齐 image-cdn Worker；value 为规范化 MIME）。
var imageExtWhitelist = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".gif": "image/gif", ".webp": "image/webp",
}

// TGImageBedPlugin TG图床插件实现（进程外）。
type TGImageBedPlugin struct {
	history *historyStore // 上传历史（OnActivate 打开数据目录初始化）
}

// Info 插件信息（与商城清单一致；能力声明 + 设置项）。
func (p *TGImageBedPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:          pluginID,
		Name:        "TG图床",
		Version:     "0.4.2",
		Author:      "月言官方",
		Description: "Telegram 频道图床：图片上传直达 TG（Bot API 保原图），自备反代 Worker 访问，后台图库管理与 Markdown 插图。",
		Capabilities: []string{"settings", "api", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "tg_bot_token", Label: "Bot Token（@BotFather 创建，如 123456:AAxxx）", Type: "text", Default: ""},
			{Key: "tg_chat_id", Label: "频道/群 Chat ID（如 -1001234567890 或 @channel）", Type: "text", Default: ""},
			{Key: "proxy_base", Label: "反代 Worker 地址（如 https://img.example.com）", Type: "text", Default: ""},
			{Key: "send_mode", Label: "发送模式（document=原图保真 / photo=TG 压缩）", Type: "select", Default: "document", Options: []string{"document", "photo"}},
			{Key: "api_proxy", Label: "TG API 代理（服务器在大陆时填，如 http://127.0.0.1:7890；留空直连）", Type: "text", Default: ""},
		},
	}
}

// OnActivate 启用回调：打开插件数据目录的上传历史（幂等，进程重启自动恢复）。
func (p *TGImageBedPlugin) OnActivate(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	store, err := newHistoryStore(filepath.Join(wd, "data", "plugins", pluginID))
	if err != nil {
		return err
	}
	p.history = store
	return nil
}

// OnDeactivate 停用回调（历史已在每次变更后落盘，无需额外保存）。
func (p *TGImageBedPlugin) OnDeactivate(ctx context.Context) error {
	p.history = nil
	return nil
}

// Hooks 订阅钩子（本插件为独立图库形态，不插入业务钩子管道）。
func (p *TGImageBedPlugin) Hooks() []sdk.Hook { return nil }

// RegisterAPI 契约端点：
//
//	GET  /storage/health   配对探测（宿主存储 seam 契约；当前 seam 提供方为 image-cdn，此端点备用兼容）
//	POST /storage/upload   转存契约（宿主 seam 备用；与 /manage/upload 同链路）
//	POST /manage/upload    图库直传 {filename,mime,content_b64}（登录用户——插图是常规发帖行为）
//	POST /manage/transfer  外链转存 {url}（登录用户——插件后端下载绕过浏览器 CORS，图片体检页用）
//	POST /manage/list      上传历史 {cursor} → {objects,cursor}（登录用户：图库页数据源）
//	POST /manage/delete    批量删除 {file_ids:[]}（管理员：尽力删频道消息 + 移除历史）
//	POST /upload /list     开放网关别名（open_endpoints 声明：浏览器插件等外部应用凭
//	                       API Key 经 /api/v1/open/plugins/tg-image-bed/* 调用，System 身份转发）
func (p *TGImageBedPlugin) RegisterAPI(api *sdk.APIMux) {
	api.Handle("GET", "/storage/health", p.handleHealth)
	api.Handle("POST", "/storage/upload", p.handleUpload)
	api.Handle("POST", "/manage/upload", p.handleUpload)
	api.Handle("POST", "/manage/transfer", p.handleTransfer)
	api.Handle("POST", "/manage/list", p.handleList)
	api.Handle("POST", "/manage/delete", p.handleDelete)
	api.Handle("POST", "/upload", p.handleUpload)
	api.Handle("POST", "/list", p.handleList)
}

// handleHealth 配对探测：配置完整 → getMe/getChat 验 Bot 与频道 → 探测反代 Worker 存活。
func (p *TGImageBedPlugin) handleHealth(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	cfg := sdk.Config(ctx)
	if err := validatePair(cfg); err != nil {
		return jsonOut(200, map[string]any{"ok": false, "error": err.Error()})
	}
	tg := tgConfigFromSettings(cfg)
	botName, chatTitle, err := tgProbe(tg)
	if err != nil {
		return jsonOut(200, map[string]any{"ok": false, "error": err.Error()})
	}
	if err := probeWorker(cfg["proxy_base"]); err != nil {
		return jsonOut(200, map[string]any{"ok": false, "error": "反代 Worker 不可达：" + err.Error()})
	}
	return jsonOut(200, map[string]any{
		"ok": true, "bot": "@" + botName, "chat": chatTitle, "worker": cfg["proxy_base"],
	})
}

// uploadRequest 上传请求体（/manage/upload 与 /storage/upload 同构）。
type uploadRequest struct {
	Filename  string `json:"filename"`
	Mime      string `json:"mime"`
	Content64 string `json:"content_b64"`
}

// uploadResponse 上传响应体（/storage/upload 契约字段 + 图库扩展字段）。
type uploadResponse struct {
	Error      string `json:"error,omitempty"` // 非空=失败原因
	Type       string `json:"type"`            // 媒体类型（恒 image——白名单前置校验）
	StorageKey string `json:"storage_key"`     // = file_id（TG 文件标识）
	URL        string `json:"url"`             // 公开访问地址（{proxy_base}/f/{file_id}）
	Mime       string `json:"mime"`
	Size       int64  `json:"size"`
	Markdown   string `json:"markdown"` // ![文件名](URL)——复制粘贴进正文
	Mode       string `json:"mode"`     // 实际发送模式（photo 遇 gif 回退 document 时与设置不同）
}

// handleUpload 图库直传/契约转存：白名单与大小校验 → 发送 TG → 落历史 → 响应。
func (p *TGImageBedPlugin) handleUpload(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.CallerIsSystem(ctx) && sdk.CallerID(ctx) <= 0 {
		return jsonOut(403, map[string]any{"error": "请先登录"})
	}
	var req uploadRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Content64 == "" || req.Filename == "" {
		return jsonOut(400, map[string]any{"error": "参数错误：filename/mime/content_b64 必填"})
	}
	lower := strings.ToLower(req.Filename)
	dot := strings.LastIndex(lower, ".")
	if dot < 0 {
		return jsonOut(400, map[string]any{"error": "文件缺少扩展名（仅支持 jpg/jpeg/png/gif/webp）"})
	}
	mime, ok := imageExtWhitelist[lower[dot:]]
	if !ok {
		return jsonOut(400, map[string]any{"error": "仅支持图片（jpg/jpeg/png/gif/webp）"})
	}
	if strings.Contains(lower, "..") || strings.ContainsAny(lower, `/\`) {
		return jsonOut(400, map[string]any{"error": "文件名不合法（含路径分隔符）"})
	}
	content, err := base64.StdEncoding.DecodeString(req.Content64)
	if err != nil {
		return jsonOut(400, map[string]any{"error": "内容 base64 解码失败"})
	}
	if len(content) > tgMaxDownloadSize {
		return jsonOut(400, map[string]any{"error": "图片超过 20MB（Telegram Bot API 文件下载上限）"})
	}
	cfg := sdk.Config(ctx)
	if err := validatePair(cfg); err != nil {
		return jsonOut(400, map[string]any{"error": err.Error()})
	}
	resp, err := p.sendImage(ctx, cfg, req.Filename, mime, content)
	if err != nil {
		return jsonOut(200, map[string]any{"error": err.Error()})
	}
	raw, _ := json.Marshal(resp)
	return 200, raw, nil
}

// sendImage 发送图片到 TG 频道并落历史（直传与外链转存共用链路；入参已完成白名单/大小校验）。
func (p *TGImageBedPlugin) sendImage(ctx context.Context, cfg map[string]string, filename string, mime string, content []byte) (*uploadResponse, error) {
	tg := tgConfigFromSettings(cfg)
	mode := cfg["send_mode"]
	sent, err := tgSendFile(tg, mode, filename, mime, content)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(strings.TrimSpace(cfg["proxy_base"]), "/") + "/f/" + sent.FileID
	if mode == "photo" && strings.Contains(mime, "gif") {
		mode = "document" // 响应回告实际模式（gif 不支持 sendPhoto 已自动回退）
	}
	resp := &uploadResponse{
		Type: "image", StorageKey: sent.FileID, URL: url, Mime: mime,
		Size: sent.Size, Markdown: markdownFor(filename, url), Mode: mode,
	}
	if p.history != nil {
		_ = p.history.append(historyEntry{
			FileID: sent.FileID, MessageID: sent.MessageID, FileName: filename,
			Size: sent.Size, Mime: mime, URL: url, Mode: mode, UploaderID: sdk.CallerID(ctx),
		})
	}
	return resp, nil
}

// handleList 上传历史分页（登录用户——图库页数据源；新在前）。
func (p *TGImageBedPlugin) handleList(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.CallerIsSystem(ctx) && sdk.CallerID(ctx) <= 0 {
		return jsonOut(403, map[string]any{"error": "请先登录"})
	}
	var req struct {
		Cursor string `json:"cursor"` // 偏移量十进制串（首页空）
	}
	_ = json.Unmarshal(body, &req)
	if p.history == nil {
		return jsonOut(200, map[string]any{"objects": []any{}, "cursor": ""})
	}
	entries, next := p.history.page(req.Cursor)
	objects := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		objects = append(objects, map[string]any{
			"file_id": e.FileID, "file_name": e.FileName, "url": e.URL,
			"markdown": markdownFor(e.FileName, e.URL), "size": e.Size,
			"mime": e.Mime, "uploaded_at": e.UploadedAt,
		})
	}
	return jsonOut(200, map[string]any{"objects": objects, "cursor": next})
}

// handleDelete 批量删除（仅管理员）：逐条尽力 deleteMessage（失败不阻塞）→ 移除历史记录。
// 说明：删除频道消息后 TG 服务器缓存文件可能仍可经旧 URL 访问一段时间（README 已注明）。
func (p *TGImageBedPlugin) handleDelete(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.TrustedCaller(ctx) {
		return jsonOut(403, map[string]any{"error": "仅管理员可删除图片"})
	}
	var req struct {
		FileIDs []string `json:"file_ids"` // TG file_id 列表
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.FileIDs) == 0 {
		return jsonOut(400, map[string]any{"error": "参数错误：file_ids 必填"})
	}
	cfg := sdk.Config(ctx)
	tg := tgConfigFromSettings(cfg) // 配置不完整时仅移除历史（无法删频道消息）
	if p.history != nil {
		for _, id := range req.FileIDs {
			if e := p.history.find(id); e != nil && e.MessageID > 0 {
				_ = tgDeleteMessage(tg, e.MessageID) // 尽力删除：网络/配置失败不阻塞记录移除
			}
		}
		deleted := p.history.remove(req.FileIDs)
		return jsonOut(200, map[string]any{"deleted": deleted, "total": len(req.FileIDs)})
	}
	return jsonOut(200, map[string]any{"deleted": 0, "total": len(req.FileIDs)})
}

// tgConfigFromSettings 从生效配置提取 TG 配对（纯函数）。
func tgConfigFromSettings(cfg map[string]string) tgConfig {
	return tgConfig{
		BotToken: strings.TrimSpace(cfg["tg_bot_token"]),
		ChatID:   strings.TrimSpace(cfg["tg_chat_id"]),
		Proxy:    strings.TrimSpace(cfg["api_proxy"]),
	}
}

// validatePair 配对配置校验（token/chat_id 非空 + proxy_base 完整 https 地址；
// http:// 仅允许 localhost/127.0.0.1 本地联调，与 image-cdn 规则一致）。
func validatePair(cfg map[string]string) error {
	if strings.TrimSpace(cfg["tg_bot_token"]) == "" || strings.TrimSpace(cfg["tg_chat_id"]) == "" {
		return fmt.Errorf("未配置：请先在插件设置中填写 Bot Token 与 Chat ID")
	}
	base := strings.TrimSpace(cfg["proxy_base"])
	if base == "" {
		return fmt.Errorf("未配置：请先在插件设置中填写反代 Worker 地址")
	}
	if strings.HasPrefix(base, "https://") && len(base) > len("https://x") {
		return nil
	}
	if strings.HasPrefix(base, "http://") &&
		(strings.HasPrefix(base, "http://localhost") || strings.HasPrefix(base, "http://127.0.0.1")) {
		return nil // 本地联调例外（mock Worker）
	}
	return fmt.Errorf("反代 Worker 地址需为 https:// 开头的完整地址")
}

// probeWorker 反代 Worker 存活探测：GET {base}/health → HTTP 200（10s 快速失败）。
func probeWorker(base string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(base, "/") + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d（确认已部署 tg-image-bed worker）", resp.StatusCode)
	}
	return nil
}

// markdownFor 生成 Markdown 图片语法（文件名清理 [ ] ( ) 防语法破坏；纯函数）。
func markdownFor(filename string, url string) string {
	safe := strings.NewReplacer("[", "(", "]", ")", "(", "(", ")", ")").Replace(filename)
	return "![" + safe + "](" + url + ")"
}

// jsonOut JSON 响应封装。
func jsonOut(status int, payload map[string]any) (int, []byte, error) {
	raw, _ := json.Marshal(payload)
	return status, raw, nil
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[tg-image-bed] 进程启动")
	server.Serve(&TGImageBedPlugin{})
}
