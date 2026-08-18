// marketplace-repo/tts-reader/tts.go
// 语音合成引擎：内置微软 Edge readaloud 免费 WebSocket 端点 + 可选自定义 HTTP 端点。
//
// Edge 协议（社区逆向稳定的免费接口，无需密钥）：
//   - 连接 wss://speech.platform.bing.com/...?TrustedClientToken=<固定token>&ConnectionId=<uuid>
//   - 先发一条 JSON 配置（音频输出格式），再逐块发送 SSML 消息（每块独立 X-RequestId）
//   - 二进制消息为 MP3 音频分片；文本消息 Path:turn.end 标记当前块结束
//   - 长文本按固定 rune 数分段、单连接内顺序合成，MP3 分片直接拼接（帧流格式可续播）
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// edge 免费引擎常量（Token 为公开客户端标识，社区通用）。
const (
	edgeWSSURL        = "wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1"
	edgeTrustedToken  = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	edgeGECVersion    = "1-143.0.3650.75" // Sec-MS-GEC-Version（Chromium 版本形态；服务端校验版本下限，过旧即 403）
	edgeOrigin        = "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold" // Read Aloud 扩展 Origin
	edgeChunkMaxRunes = 400 // 单块合成最大字数（分块防超限，单连接串行组合）
	edgeOutputFormat  = "audio-24khz-48kbitrate-mono-mp3"
)

// browserUA Edge 合成接口要求的浏览器 UA（Edg/ 后缀 + 版本与 Sec-MS-GEC-Version 对齐）。
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"

// xmlEscaper XML 文本转义（SSML 注入防护：text 中的 & < > " ' 必须先转义）。
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

// xmlEscape 转义 XML 文本节点内容（纯函数）。
func xmlEscape(s string) string {
	return xmlEscaper.Replace(s)
}

// newRandID 生成随机十六进制 ID（UUID 形态的请求/连接标识；纯函数）。
func newRandID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// edgeDRMToken 生成握手防盗链令牌 Sec-MS-GEC（纯函数）。
// 算法（对齐官方 edge-tts 现行实现；2023 年末起强制，缺失/过旧版本即 403）：
//   Unix 秒 → 转 Windows FILETIME 秒（+11644473600）→ 向下对齐 5 分钟
//   → ×1e7（转 100ns 刻度）→ 与 TrustedClientToken 拼成**十进制字符串**
//   → SHA256 → 大写 hex（注意：哈希对象是 ASCII 字符串而非二进制刻度）。
func edgeDRMToken() string {
	const winEpochDeltaSec = int64(11644473600) // 1601-01-01 → 1970-01-01 秒差
	const tickWindowSec = int64(300)            // 5 分钟窗口
	ticks := time.Now().Unix() + winEpochDeltaSec
	ticks -= ticks % tickWindowSec
	ticks *= 10_000_000 // 秒 → 100ns（Windows FILETIME 刻度）
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d%s", ticks, edgeTrustedToken)))
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

// splitRunes 按指定 rune 数将字符串分块（纯函数；避免按字节切割破坏多字节字符）。
func splitRunes(s string, size int) []string {
	runes := []rune(s)
	if len(runes) == 0 {
		return nil
	}
	chunks := make([]string, 0, (len(runes)+size-1)/size)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

// edgeTimestamp JS 风格时间戳（服务端解析此形态；纯函数）。
func edgeTimestamp() string {
	return time.Now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT+0000 (Coordinated Universal Time)")
}

// edgeConfigMessage 构造首条 speech.config 消息（协议头 + 音频格式 JSON；纯函数）。
// 注意：服务端严格要求文本消息携带协议头，裸 JSON 会被 close 1007 拒绝。
func edgeConfigMessage() []byte {
	var sb strings.Builder
	sb.WriteString("X-Timestamp:")
	sb.WriteString(edgeTimestamp())
	sb.WriteString("\r\nContent-Type:application/json; charset=utf-8\r\nPath:speech.config\r\n\r\n")
	sb.WriteString(`{"context":{"synthesis":{"audio":{"metadataoptions":`)
	sb.WriteString(`{"sentenceBoundaryEnabled":"false","wordBoundaryEnabled":"false"},`)
	sb.WriteString(`"outputFormat":"`)
	sb.WriteString(edgeOutputFormat)
	sb.WriteString(`"}}}}`)
	sb.WriteString("\r\n")
	return []byte(sb.String())
}

// edgeSSMLMessage 构造单块 SSML 合成消息（HTTP 风格头 + SSML 正文；纯函数）。
// Content-Type 必须为 application/ssml+xml（服务端按头分发消息，错值即 close 1007）。
func edgeSSMLMessage(reqID string, text string, voice string, rate string) []byte {
	var sb strings.Builder
	sb.WriteString("X-RequestId:")
	sb.WriteString(reqID)
	sb.WriteString("\r\nContent-Type:application/ssml+xml\r\nX-Timestamp:")
	sb.WriteString(edgeTimestamp())
	sb.WriteString("Z\r\nPath:ssml\r\n\r\n")
	// SSML 正文（voice 名称与 prosody 语速由合成方取值——调用侧已做默认值回落与转义）
	sb.WriteString("<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='zh-CN'>")
	sb.WriteString("<voice name='")
	sb.WriteString(xmlEscape(voice))
	sb.WriteString("'><prosody pitch='+0Hz' rate='")
	sb.WriteString(xmlEscape(rate))
	sb.WriteString("' volume='+0%'>")
	sb.WriteString(xmlEscape(text))
	sb.WriteString("</prosody></voice></speak>")
	return []byte(sb.String())
}

// synthesizeEdgeEdge 经 Edge WebSocket 免费引擎合成整段文本音频（MP3 字节；纯引擎函数）。
// 长文本按 edgeChunkMaxRunes 分段，单连接内顺序合成、二进制分片直接拼接。
func synthesizeEdge(ctx context.Context, text string, voice string, rate string) ([]byte, error) {
	chunks := splitRunes(text, edgeChunkMaxRunes)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("朗读内容为空")
	}
	// 建立 WebSocket 连接（并入 context 取消；对齐官方 edge-tts 握手要件：
	// Sec-MS-GEC 防盗链令牌 + 版本号 + 扩展 Origin + muid Cookie，缺一即 403）
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	header := http.Header{}
	header.Set("User-Agent", browserUA)
	header.Set("Origin", edgeOrigin)
	header.Set("Pragma", "no-cache")
	header.Set("Cache-Control", "no-cache")
	header.Set("Cookie", "muid="+strings.ToUpper(newRandID())+";") // 随机 muid（官方库同款）
	connURL := fmt.Sprintf(
		"%s?TrustedClientToken=%s&Sec-MS-GEC=%s&Sec-MS-GEC-Version=%s&ConnectionId=%s",
		edgeWSSURL, edgeTrustedToken, edgeDRMToken(), edgeGECVersion, newRandID())
	conn, _, err := dialer.DialContext(ctx, connURL, header)
	if err != nil {
		return nil, fmt.Errorf("连接合成服务失败：%w", err)
	}
	defer func() { _ = conn.Close() }()

	// 首条：音频输出格式配置（带协议头的 speech.config 文本消息）
	if err := conn.WriteMessage(websocket.TextMessage, edgeConfigMessage()); err != nil {
		return nil, fmt.Errorf("发送合成配置失败：%w", err)
	}

	// 逐块合成（每块新 X-RequestId，读至该块 turn.end）
	var audio bytes.Buffer
	for _, chunk := range chunks {
		chunkID := newRandID()
		if err := conn.WriteMessage(websocket.TextMessage, edgeSSMLMessage(chunkID, chunk, voice, rate)); err != nil {
			return nil, fmt.Errorf("发送合成文本失败：%w", err)
		}
		// 块内读循环（读超时防挂死；二进制=带协议头的音频帧，文本 turn.end=块结束）
		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			return nil, err
		}
		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return nil, fmt.Errorf("读取合成结果失败：%w", err)
			}
			if msgType == websocket.BinaryMessage {
				// 二进制帧结构：2 字节大端头长 + 协议头 + 有效载荷。
				// Path:audio 帧剥头保留音频体；Path:audio.metadata 帧丢弃。
				if body, ok := edgeAudioPayload(payload); ok {
					_, _ = audio.Write(body)
				}
				continue
			}
			if bytes.Contains(payload, []byte("Path:turn.end")) {
				break // 本块结束
			}
		}
	}
	return audio.Bytes(), nil
}

// edgeAudioPayload 解析二进制帧（纯函数）：返回音频体与是否为音频帧。
// 结构：前 2 字节大端为协议头长度，其后为 HTTP 风格头（含 Path:audio 或
// Path:audio.metadata），剩余部分为有效载荷（仅 Path:audio 帧含音频字节）。
// 畸形帧按非音频帧处理（跳过——不因单帧损坏中断整段合成）。
func edgeAudioPayload(payload []byte) ([]byte, bool) {
	if len(payload) < 2 {
		return nil, false
	}
	headerLen := int(binary.BigEndian.Uint16(payload[:2]))
	if len(payload) < 2+headerLen {
		return nil, false
	}
	header := payload[2 : 2+headerLen]
	if !bytes.Contains(header, []byte("Path:audio\r\n")) && !bytes.HasSuffix(header, []byte("Path:audio")) {
		return nil, false // audio.metadata 帧或未知帧：丢弃
	}
	return payload[2+headerLen:], true
}

// synthesizeCustom 经自定义 HTTP 端点合成（POST JSON {text,voice,rate} → 音频字节）。
// 键格式：与远端约定一致（如 OpenAI 兼容 /audio/speech 的自托管代理、piper 等）；
// 配置了 custom_key 时附加 Authorization: Bearer 头。
func synthesizeCustom(ctx context.Context, endpoint string, key string, text string, voice string, rate string) ([]byte, error) {
	payload, err := json.Marshal(map[string]string{
		"text": text, "voice": voice, "rate": rate,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("自定义端点请求失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("自定义端点返回 %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8MB 上限兜底
}
