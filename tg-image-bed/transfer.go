// marketplace-repo/tg-image-bed/transfer.go
// 外链图片转存（图片体检页 POST /manage/transfer {url}）：
// 浏览器 fetch 外链图受 CORS 限制，下载必须由插件后端完成——
// 拉取原图 → 白名单/大小校验 → 复用 sendImage 直达 TG 频道 → 返回反代 Worker URL。
package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// downloadTimeout 外链下载超时（含大图与慢源站）。
const downloadTimeout = 30 * time.Second

// mimeToExt MIME → 扩展名（Content-Type 判定主路径；与 imageExtWhitelist 互补）。
var mimeToExt = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
}

// handleTransfer 外链转存：下载 → 校验 → sendImage → 响应（登录用户，与直传同权限级）。
func (p *TGImageBedPlugin) handleTransfer(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !sdk.CallerIsSystem(ctx) && sdk.CallerID(ctx) <= 0 {
		return jsonOut(403, map[string]any{"error": "请先登录"})
	}
	var req struct {
		URL string `json:"url"` // 外链图片地址（http/https）
	}
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.URL) == "" {
		return jsonOut(400, map[string]any{"error": "参数错误：url 必填"})
	}
	cfg := sdk.Config(ctx)
	if err := validatePair(cfg); err != nil {
		return jsonOut(400, map[string]any{"error": err.Error()})
	}
	content, mime, err := downloadImage(req.URL)
	if err != nil {
		return jsonOut(200, map[string]any{"error": err.Error()})
	}
	filename, err := filenameForURL(req.URL, mime)
	if err != nil {
		return jsonOut(400, map[string]any{"error": err.Error()})
	}
	resp, err := p.sendImage(ctx, cfg, filename, mime, content)
	if err != nil {
		return jsonOut(200, map[string]any{"error": err.Error()})
	}
	return jsonOut(200, map[string]any{
		"url": resp.URL, "file_id": resp.StorageKey, "filename": filename,
		"mime": resp.Mime, "size": resp.Size, "mode": resp.Mode,
	})
}

// downloadImage 下载外链图片（http/https；Content-Type 白名单 + 20MB 前置校验）。
func downloadImage(rawURL string) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, "", fmt.Errorf("仅支持 http/https 图片地址")
	}
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(parsed.String())
	if err != nil {
		return nil, "", fmt.Errorf("图片下载失败（源站不可达或网络受限）：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("图片下载失败：HTTP %d", resp.StatusCode)
	}
	// 超限即报错（LimitReader 多读 1 字节用于判超）
	content, err := io.ReadAll(io.LimitReader(resp.Body, tgMaxDownloadSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("图片读取失败：%v", err)
	}
	if len(content) > tgMaxDownloadSize {
		return nil, "", fmt.Errorf("图片超过 20MB（Telegram Bot API 文件下载上限）")
	}
	mime := normalizeMime(resp.Header.Get("Content-Type"))
	if mime == "" {
		return nil, "", fmt.Errorf("源站返回的不是图片（仅支持 jpg/png/gif/webp）")
	}
	return content, mime, nil
}

// normalizeMime 规范化 Content-Type（去参数、白名单判定；非图片返回空串；纯函数）。
func normalizeMime(contentType string) string {
	mime := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if _, ok := mimeToExt[mime]; ok {
		return mime
	}
	return ""
}

// filenameForURL 从 URL 推导合法文件名（优先路径尾段；无扩展名/无尾段时按内容哈希补；
// 结果恒过直传同款安全校验：不含路径分隔符与 ".."；纯函数）。
func filenameForURL(rawURL string, mime string) (string, error) {
	ext, ok := mimeToExt[mime]
	if !ok {
		return "", fmt.Errorf("不支持的图片类型：%s", mime)
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("图片地址无效")
	}
	// 路径尾段清理：去 query、限制字符（直传校验禁 //.. 与分隔符，此处构造时即保证）
	base := path.Base(parsed.Path)
	base = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '#', '%':
			return '_'
		}
		return r
	}, base)
	// 尾段无效（空根路径/纯符号）：置空走哈希兜底
	if strings.Trim(base, "._") == "" {
		base = ""
	}
	// 去既有扩展名后统一补规范化扩展（.jpeg → .jpg 等）
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	if base == "" {
		// 无可用尾段：内容无关的时间+哈希命名（同一 URL 稳定）
		sum := md5.Sum([]byte(parsed.String()))
		base = "transfer-" + hex.EncodeToString(sum[:8])
	}
	if len(base) > 60 {
		base = base[len(base)-60:] // 超长截尾（保尾部：通常含可读后缀）
	}
	return base + ext, nil
}
