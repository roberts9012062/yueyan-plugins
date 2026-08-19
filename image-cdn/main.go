// marketplace-repo/image-cdn/main.go
// CF图床插件（进程外，免费）：媒体上传直达 Cloudflare R2 对象存储 + 图片管理。
//
// 架构（配对三件套）：
//   - Cloudflare Worker（用户自行部署，见同目录 worker/index.js 参考实现）
//   - R2 对象存储（Worker 的 R2BIND 桶绑定）
//   - 本插件（填 Worker URL + API Key 配对即用）
//
// 能力划分：
//   - settings：workers_url / api_key（配对）+ 压缩三参数（开关/质量/最大边长）
//   - api：/storage/* 宿主存储 seam 契约端点；/manage/list 图片列表（登录用户，
//     发帖页图库选择用）、/manage/delete 批量删除（管理员，后台管理页用）
//   - admin.page：后台「CF图床」图片管理页（网格浏览/拖拽上传/删除，见 frontend/）
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与 plugin.json / yueyan-plugin.json 一致）。
const pluginID = "image-cdn"

// ImageBedPlugin CF图床插件实现（进程外）。
type ImageBedPlugin struct{}

// Info 插件信息（与商城清单一致；能力声明 + 设置项）。
func (p *ImageBedPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:           pluginID,
		Name:         "CF图床",
		Version:      "2.1.0",
		Author:       "月言官方",
		Description:  "媒体上传直达 Cloudflare R2：配对即用、服务端压缩、后台图片管理与发帖图库联动。免费。",
		Capabilities: []string{"settings", "api", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "workers_url", Label: "Workers URL（如 https://imgs.example.com）", Type: "text", Default: ""},
			{Key: "api_key", Label: "API Key（wrangler secret put API_KEY 设置的值）", Type: "text", Default: ""},
			{Key: "compress_enabled", Label: "服务端压缩（JPEG 质量/边长缩放；已达标的自动跳过）", Type: "switch", Default: "on"},
			{Key: "compress_quality", Label: "JPEG 压缩质量（30-95）", Type: "text", Default: "80"},
			{Key: "max_dimension", Label: "最大边长像素（超出等比缩小；0=不缩放）", Type: "text", Default: "1920"},
		},
	}
}

// OnActivate 启用回调（无资源需初始化；配对状态经 /storage/health 按需探测）。
func (p *ImageBedPlugin) OnActivate(ctx context.Context) error { return nil }

// OnDeactivate 停用回调。
func (p *ImageBedPlugin) OnDeactivate(ctx context.Context) error { return nil }

// Hooks 订阅钩子（存储接管走宿主 seam，不插入业务钩子管道）。
func (p *ImageBedPlugin) Hooks() []sdk.Hook { return nil }

// RegisterAPI 契约端点：
//
//	GET  /storage/health   配对探测（宿主 seam 用）
//	POST /storage/upload   转存（宿主 seam 用；先按设置压缩再转 R2）
//	POST /manage/list      图片列表 {cursor} → {objects,cursor}（登录用户：发帖图库）
//	POST /manage/upload    后台管理页直传 {filename,mime,content_b64}（管理员；压缩同链路）
//	POST /manage/delete    批量删除 {keys:[]} → {deleted}（管理员：后台管理页）
func (p *ImageBedPlugin) RegisterAPI(api *sdk.APIMux) {
	api.Handle("GET", "/storage/health", handleHealth)
	api.Handle("POST", "/storage/upload", handleUpload)
	api.Handle("POST", "/manage/list", handleList)
	api.Handle("POST", "/manage/upload", handleManageUpload)
	api.Handle("POST", "/manage/delete", handleDelete)
}

// handleManageUpload 后台管理页直传：与 seam 转存同链路（压缩 + Worker），
// 但仅管理员可调且不落 media_assets（图床管理语义——对象进 R2 即出现在列表）。
func handleManageUpload(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.TrustedCaller(ctx) {
		return jsonOut(403, map[string]any{"error": "仅管理员可上传"})
	}
	return handleUpload(ctx, method, path, body)
}

// handleHealth 配对探测：校验 URL/Key 配置 → ping Worker /health。
func handleHealth(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	cfg := sdk.Config(ctx)
	err := validatePair(cfg)
	if err != nil {
		return jsonOut(200, map[string]any{"ok": false, "error": err.Error()})
	}
	if err := pingWorker(cfg["workers_url"], cfg["api_key"]); err != nil {
		return jsonOut(200, map[string]any{"ok": false, "error": "Worker 不可达或密钥不符：" + err.Error()})
	}
	return jsonOut(200, map[string]any{"ok": true})
}

// uploadRequest 宿主 seam 契约请求体。
type uploadRequest struct {
	Filename  string `json:"filename"`
	Mime      string `json:"mime"`
	Content64 string `json:"content_b64"`
}

// handleUpload 转存：按设置压缩 → Worker /upload → 契约响应（原样/压缩后大小回传）。
func handleUpload(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	var req uploadRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Content64 == "" {
		return jsonOut(400, map[string]any{"error": "参数错误：filename/mime/content_b64 必填"})
	}
	cfg := sdk.Config(ctx)
	if err := validatePair(cfg); err != nil {
		return jsonOut(400, map[string]any{"error": err.Error()})
	}
	content64 := req.Content64
	mime := req.Mime
	filename := req.Filename
	// 服务端压缩（不启用/不适用/无收益时原样转发）
	if content, err := base64Decode(content64); err == nil {
		if compressed, outMime, _ := compressImage(parseCompressConfig(cfg), filename, mime, content); compressed != nil {
			content64 = base64Encode(compressed)
			mime = outMime
		}
	}
	result, err := uploadToWorker(cfg["workers_url"], cfg["api_key"], filename, mime, content64)
	if err != nil {
		return jsonOut(200, map[string]any{"error": err.Error()})
	}
	return jsonOut(200, map[string]any{
		"type": result.Type, "storage_key": result.StorageKey,
		"url": result.URL, "mime": result.Mime, "size": result.Size,
	})
}

// listRequest /manage/list 请求体。
type listRequest struct {
	Cursor string `json:"cursor"` // 分页游标（首页空）
}

// handleList 图片列表：转发 Worker /list（登录用户可调——发帖页图库数据源）。
func handleList(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.CallerIsSystem(ctx) && sdk.CallerID(ctx) <= 0 {
		return jsonOut(403, map[string]any{"error": "请先登录"})
	}
	cfg := sdk.Config(ctx)
	if err := validatePair(cfg); err != nil {
		return jsonOut(200, map[string]any{"objects": []any{}, "cursor": "", "error": err.Error()})
	}
	var req listRequest
	_ = json.Unmarshal(body, &req)
	raw, err := listWorker(cfg["workers_url"], cfg["api_key"], req.Cursor)
	if err != nil {
		return jsonOut(200, map[string]any{"objects": []any{}, "cursor": "", "error": err.Error()})
	}
	return 200, raw, nil // Worker 响应透传（objects/cursor 已按契约组装）
}

// deleteRequest /manage/delete 请求体。
type deleteRequest struct {
	Keys []string `json:"keys"` // 对象键列表
}

// handleDelete 批量删除：逐个调 Worker DELETE /f/:key（仅管理员——后台管理页操作）。
func handleDelete(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.TrustedCaller(ctx) {
		return jsonOut(403, map[string]any{"error": "仅管理员可删除图片"})
	}
	cfg := sdk.Config(ctx)
	if err := validatePair(cfg); err != nil {
		return jsonOut(400, map[string]any{"error": err.Error()})
	}
	var req deleteRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Keys) == 0 {
		return jsonOut(400, map[string]any{"error": "参数错误：keys 必填"})
	}
	deleted := 0
	for _, key := range req.Keys {
		if err := deleteWorkerObject(cfg["workers_url"], cfg["api_key"], key); err == nil {
			deleted++
		}
	}
	return jsonOut(200, map[string]any{"deleted": deleted, "total": len(req.Keys)})
}

// validatePair 配对配置校验（URL 完整、Key 非空；纯函数）。
// URL 规则：https:// 完整地址；http:// 仅允许 localhost/127.0.0.1（本地 mock 联调）。
func validatePair(cfg map[string]string) error {
	url := strings.TrimSpace(cfg["workers_url"])
	key := strings.TrimSpace(cfg["api_key"])
	if url == "" || key == "" {
		return fmt.Errorf("未配置：请先在插件设置中填写 Workers URL 与 API Key")
	}
	if strings.HasPrefix(url, "https://") && len(url) > len("https://x") {
		return nil
	}
	if strings.HasPrefix(url, "http://") &&
		(strings.HasPrefix(url, "http://localhost") || strings.HasPrefix(url, "http://127.0.0.1")) {
		return nil // 本地联调例外（mock Worker）
	}
	return fmt.Errorf("Workers URL 需为 https:// 开头的完整地址")
}

// jsonOut JSON 响应封装。
func jsonOut(status int, payload map[string]any) (int, []byte, error) {
	raw, _ := json.Marshal(payload)
	return status, raw, nil
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[image-cdn] 进程启动")
	server.Serve(&ImageBedPlugin{})
}
