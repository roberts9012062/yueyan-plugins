// marketplace-repo/tts-reader/api.go
// 自定义 API 与音频缓存：
//   - POST /tts：接收 {text, voice?, rate?} → 按 text+voice+rate 哈希合成并缓存 → {id}
//   - POST /tts/audio：接收 {id} → 返回缓存音频原始字节（audio/mpeg）
//
// 缓存以「正文哈希 = 文件名」落到插件数据目录 cache/ 下——天然去重、重启可复用、
// id 白名单正则防路径穿越；懒清理过期文件控制磁盘增长。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// cacheTTL 音频缓存有效期（过期即清，防无限增长）。
const cacheTTL = 60 * time.Minute

// audioIDPattern 音频 id 白名单（sha256 hex 恰为 64 位小写十六进制——防路径穿越）。
var audioIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// defaultMaxChars 单次朗读最大字数兜底（设置项异常时使用）。
const defaultMaxChars = 3000

// ttsStore 音频缓存目录句柄（缓存即文件系统）。
type ttsStore struct {
	mu  sync.Mutex
	dir string // 缓存目录（data/plugins/tts-reader/cache）
}

// newTTSStore 创建缓存目录句柄（建目录失败返回错误）。
func newTTSStore(dir string) (*ttsStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &ttsStore{dir: dir}, nil
}

// cachePath 计算音频文件路径（id 已由上层白名单校验；纯函数）。
func (s *ttsStore) cachePath(id string) string {
	return filepath.Join(s.dir, id+".mp3")
}

// get 取有效缓存路径：命中且未过期返回路径，否则返回空串（过期文件顺带删除）。
func (s *ttsStore) get(id string) string {
	if !audioIDPattern.MatchString(id) {
		return ""
	}
	p := s.cachePath(id)
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	if time.Since(st.ModTime()) > cacheTTL {
		_ = os.Remove(p)
		return ""
	}
	return p
}

// put 写入缓存文件。
func (s *ttsStore) put(id string, data []byte) error {
	if !audioIDPattern.MatchString(id) {
		return fmt.Errorf("非法音频 id")
	}
	return os.WriteFile(s.cachePath(id), data, 0o644)
}

// sweep 懒清理过期缓存文件（每次合成前调用；目录小，遍历成本可忽略）。
func (s *ttsStore) sweep() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(s.dir, e.Name())
		if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) > cacheTTL {
			_ = os.Remove(p)
		}
	}
}

// callerAllowed 该 API 是否可被当前调用者触达（System 桥接 或 登录用户；匿名直连不存在——
// 插件 API 挂 authed 组，公开通道仅宿主 System 桥接）。
func callerAllowed(ctx context.Context) bool {
	return sdk.CallerIsSystem(ctx) || sdk.CallerID(ctx) > 0
}

// synthKey 计算合成缓存键（text+voice+rate 的 sha256 hex；不同参数各自缓存）。
func synthKey(text string, voice string, rate string) string {
	digest := sha256.Sum256([]byte(text + "\x00" + voice + "\x00" + rate))
	return hex.EncodeToString(digest[:])
}

// configInt 读设置中的整数（无效回退默认；纯函数）。
func configInt(cfg map[string]string, key string, fallback int) int {
	raw := strings.TrimSpace(cfg[key])
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	if v > 20000 { // 上限兜底：拒绝超大配置防恶意文本轰炸
		return 20000
	}
	return v
}

// synthesizeByConfig 按设置分流合成引擎（内置 Edge 或自定义端点；纯合成分发）。
func synthesizeByConfig(ctx context.Context, cfg map[string]string, text string, voice string, rate string) ([]byte, error) {
	if endpoint := strings.TrimSpace(cfg["custom_endpoint"]); endpoint != "" {
		return synthesizeCustom(ctx, endpoint, strings.TrimSpace(cfg["custom_key"]), text, voice, rate)
	}
	return synthesizeEdge(ctx, text, voice, rate)
}

// jsonErr 构造错误 JSON 响应体（纯函数）。
func jsonErr(code int, msg string) (int, []byte, error) {
	raw, _ := json.Marshal(map[string]any{"error": msg})
	return code, raw, nil
}

// handleSynth POST /tts：合成并缓存音频，返回 {id}（缓存命中直接复用）。
func (p *TtsPlugin) handleSynth(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !callerAllowed(ctx) {
		return jsonErr(403, "无权访问")
	}
	store := p.storeSafe()
	if store == nil {
		return jsonErr(500, "插件未激活")
	}
	var req struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
		Rate  string `json:"rate"`
	}
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Text) == "" {
		return jsonErr(400, "缺少朗读文本 text")
	}
	// 归一化文本：合并连续空白（去除冗余换行/缩进，保留单空格语义）
	text := strings.Join(strings.Fields(req.Text), " ")
	cfg := sdk.Config(ctx)
	maxChars := configInt(cfg, "max_chars", defaultMaxChars)
	if utf8.RuneCountInString(text) > maxChars {
		return jsonErr(400, fmt.Sprintf("朗读文本过长（上限 %d 字）", maxChars))
	}
	// 音色/语速默认值回落（空 → 设置默认 → 内置常量兜底）
	voice := req.Voice
	if voice == "" {
		voice = cfg["default_voice"]
	}
	if voice == "" {
		voice = "zh-CN-XiaoxiaoNeural"
	}
	rate := req.Rate
	if rate == "" {
		rate = cfg["default_rate"]
	}
	if rate == "" {
		rate = "+0%"
	}
	// 磁盘缓存命中直接复用（同名文件未过期）
	id := synthKey(text, voice, rate)
	if store.get(id) != "" {
		raw, _ := json.Marshal(map[string]any{"id": id})
		return 200, raw, nil
	}
	// 合成（首次或已过期）→ 写缓存
	store.sweep()
	data, err := synthesizeByConfig(ctx, cfg, text, voice, rate)
	if err != nil {
		// 可操作错误文案：保留具体原因 + 引导（内置源受网络/服务端风控影响时改配自定义端点）
		msg := "合成失败：" + err.Error()
		if strings.TrimSpace(cfg["custom_endpoint"]) == "" {
			msg += "（可在插件设置中配置自定义合成端点切换音源）"
		}
		return jsonErr(200, msg)
	}
	if err := store.put(id, data); err != nil {
		return jsonErr(500, "缓存写入失败")
	}
	raw, _ := json.Marshal(map[string]any{"id": id})
	return 200, raw, nil
}

// handleAudio POST /tts/audio：按 id 返回缓存音频原始字节（宿主 Audio 端点以 audio/mpeg 输出）。
// 注意：响应体为音频字节而非 JSON——仅供宿主公开桥接 / 登录用户 fetch 使用。
func (p *TtsPlugin) handleAudio(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
	if !callerAllowed(ctx) {
		return jsonErr(403, "无权访问")
	}
	store := p.storeSafe()
	if store == nil {
		return jsonErr(500, "插件未激活")
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &req); err != nil || !audioIDPattern.MatchString(req.ID) {
		return jsonErr(400, "非法音频 id")
	}
	cached := store.get(req.ID)
	if cached == "" {
		return jsonErr(404, "音频不存在或已过期")
	}
	data, err := os.ReadFile(cached)
	if err != nil {
		return jsonErr(404, "音频读取失败")
	}
	return 200, data, nil
}
