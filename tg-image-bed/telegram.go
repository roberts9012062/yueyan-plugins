// marketplace-repo/tg-image-bed/telegram.go
// Telegram Bot API 客户端（图床渠道）：发送图片到频道/群、配对探测、删除消息。
// 机制对齐 telegraph-Image 的 TG 渠道（sendDocument/sendPhoto + getFile）：
// Bot Token 只存在于插件进程内存，绝不进入任何返回给前端的 URL 或响应。
// 配对信息每次调用传入（配置即时生效，无进程内缓存）；api_proxy 可选（大陆服务器中转）。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tgConfig TG 渠道配对配置（从插件设置读取；纯数据）。
type tgConfig struct {
	BotToken string // Bot Token（@BotFather 创建，如 123456:AAxxx）
	ChatID   string // 频道/群 chat_id（数字负数或 @公开频道用户名）
	Proxy    string // 可选 HTTP 代理地址（空=直连；大陆服务器建议配置）
}

// 超时：探测快速失败，上传放宽（含大图与跨代理传输）。
const (
	tgProbeTimeout  = 10 * time.Second
	tgUploadTimeout = 60 * time.Second
)

// tgMaxDownloadSize Bot API getFile 下载上限（20MB）——上传前置校验，避免传上去取不回。
const tgMaxDownloadSize = 20 << 20

// newTGClient 按可选代理构造 HTTP 客户端（纯函数；https 经 CONNECT 隧道走代理）。
func newTGClient(proxyAddr string, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{}
	if trimmed := strings.TrimSpace(proxyAddr); trimmed != "" {
		proxyURL, err := url.Parse(trimmed)
		if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
			return nil, fmt.Errorf("代理地址无效（需形如 http://127.0.0.1:7890）：%s", trimmed)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// tgEnvelope Bot API 通用响应包络（result 按端点二次解析；失败时 description 为用户可读原因）。
type tgEnvelope struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// callTG 调用 Bot API 查询式端点（GET；getMe/getChat/getFile/deleteMessage 用）。
func callTG(client *http.Client, token string, method string, query url.Values) (json.RawMessage, error) {
	target := "https://api.telegram.org/bot" + token + "/" + method
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	resp, err := client.Get(target)
	if err != nil {
		return nil, fmt.Errorf("Telegram 请求失败（检查服务器网络或代理设置）：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env tgEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("Telegram 响应解析失败（HTTP %d）", resp.StatusCode)
	}
	if !env.OK {
		return nil, fmt.Errorf("Telegram %s 失败：%s", method, env.Description)
	}
	return env.Result, nil
}

// tgProbe 配对探测：getMe 验 Token + getChat 验 Chat ID（返回 bot 用户名与聊天标题）。
// 常见失败：token 无效（getMe 400/401）、bot 未加入频道或非管理员（getChat 400）。
func tgProbe(cfg tgConfig) (botName string, chatTitle string, err error) {
	client, err := newTGClient(cfg.Proxy, tgProbeTimeout)
	if err != nil {
		return "", "", err
	}
	meRaw, err := callTG(client, cfg.BotToken, "getMe", nil)
	if err != nil {
		return "", "", err
	}
	var me struct {
		Username string `json:"username"`
	}
	_ = json.Unmarshal(meRaw, &me)
	chatRaw, err := callTG(client, cfg.BotToken, "getChat", url.Values{"chat_id": {cfg.ChatID}})
	if err != nil {
		return me.Username, "", fmt.Errorf("%w（确认 Bot 已加入频道且为管理员）", err)
	}
	var chat struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal(chatRaw, &chat)
	return me.Username, chat.Title, nil
}

// tgSendResult 发送文件结果。
type tgSendResult struct {
	FileID    string // 最大尺寸图片/文档的 file_id（同时是访问 URL 键，公开无风险）
	MessageID int64  // 频道消息 ID（删除消息用）
	Size      int64  // 文件字节数（TG 回报）
}

// tgSendFile 发送图片到频道：mode=document 保原图 / photo=TG 服务端压缩重编码。
// sendPhoto 不支持动图，photo 模式遇 gif 自动回退 document（TG 语义限制）。
func tgSendFile(cfg tgConfig, mode string, filename string, mimeType string, content []byte) (*tgSendResult, error) {
	client, err := newTGClient(cfg.Proxy, tgUploadTimeout)
	if err != nil {
		return nil, err
	}
	endpoint, field := "sendDocument", "document"
	if mode == "photo" && !strings.Contains(strings.ToLower(mimeType), "gif") {
		endpoint, field = "sendPhoto", "photo"
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("chat_id", cfg.ChatID); err != nil {
		return nil, err
	}
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(content); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	target := "https://api.telegram.org/bot" + cfg.BotToken + "/" + endpoint
	req, err := http.NewRequest(http.MethodPost, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Telegram 上传失败（检查服务器网络或代理设置）：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env tgEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("Telegram 响应解析失败（HTTP %d）", resp.StatusCode)
	}
	if !env.OK {
		return nil, fmt.Errorf("Telegram %s 失败：%s", endpoint, env.Description)
	}
	return parseSendResult(env.Result)
}

// parseSendResult 从 send* 响应提取 file_id/message_id/size（纯函数）。
// 兼容 TG 服务器自动转码（sendDocument 的两处隐性行为，实测 v0.4.1 踩坑）：
//   - webp 符合贴纸规格 → 转为 sticker（响应无 document；file_id 经 getFile 可下载回 webp）；
//   - gif 动图 → 转码为 animation/mp4（响应无 document；下载回的是 mp4，README 已注明）。
func parseSendResult(result json.RawMessage) (*tgSendResult, error) {
	var msg struct {
		MessageID int64 `json:"message_id"`
		Photo     []struct {
			FileID   string `json:"file_id"`
			FileSize int64  `json:"file_size"`
		} `json:"photo"`
		Document struct {
			FileID   string `json:"file_id"`
			FileSize int64  `json:"file_size"`
		} `json:"document"`
		Sticker struct {
			FileID   string `json:"file_id"`
			FileSize int64  `json:"file_size"`
		} `json:"sticker"`
		Animation struct {
			FileID   string `json:"file_id"`
			FileSize int64  `json:"file_size"`
		} `json:"animation"`
	}
	if err := json.Unmarshal(result, &msg); err != nil {
		return nil, fmt.Errorf("发送响应解析失败：%w", err)
	}
	out := &tgSendResult{MessageID: msg.MessageID}
	switch {
	case len(msg.Photo) > 0:
		largest := msg.Photo[0]
		for _, p := range msg.Photo {
			if p.FileSize > largest.FileSize {
				largest = p
			}
		}
		out.FileID, out.Size = largest.FileID, largest.FileSize
	case msg.Document.FileID != "":
		out.FileID, out.Size = msg.Document.FileID, msg.Document.FileSize
	case msg.Sticker.FileID != "": // webp 被转贴纸（同 file_id 语义，Worker 直链照常）
		out.FileID, out.Size = msg.Sticker.FileID, msg.Sticker.FileSize
	case msg.Animation.FileID != "": // gif 被转 mp4 动画
		out.FileID, out.Size = msg.Animation.FileID, msg.Animation.FileSize
	default:
		return nil, fmt.Errorf("发送响应缺少 photo/document 字段")
	}
	return out, nil
}

// tgDeleteMessage 删除频道消息（尽力而为：错误返回给调用方决定是否阻塞记录移除）。
func tgDeleteMessage(cfg tgConfig, messageID int64) error {
	client, err := newTGClient(cfg.Proxy, tgProbeTimeout)
	if err != nil {
		return err
	}
	_, err = callTG(client, cfg.BotToken, "deleteMessage", url.Values{
		"chat_id": {cfg.ChatID}, "message_id": {fmt.Sprint(messageID)},
	})
	return err
}
