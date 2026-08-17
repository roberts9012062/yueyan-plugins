// cmd/qq-music-plugin/qq/client.go
// QQ 音乐 HTTP 客户端（进程外插件内部）：cookie 会话管理 + vkey 获取 + 登录态持久化。
// 登录态（uin QQ 号 + musickey 密钥）AES 加密持久化到插件数据目录
// （data/plugins/qq-music/state.json），插件重启后自动恢复登录态。
package qq

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// vkeyBaseURL 获取 vkey 的接口基地址。
const vkeyBaseURL = "https://u.y.qq.com/cgi-bin/musicu.fcg"

// searchURL QQ 音乐搜索接口（公开）。
const searchURL = "https://c.y.qq.com/soso/fcgi-bin/client_search_cp"

// stateFile 登录态文件名（插件数据目录下）。
const stateFile = "state.json"

// Client QQ 音乐客户端（连接器类，仅用于外部系统接口）。
type Client struct {
	httpClient *http.Client   // HTTP 客户端（cookie jar 管理扫码会话）
	jar        *cookiejar.Jar // cookie jar（扫码登录会话 cookie）
	stateDir   string         // 插件数据目录（登录态 + 调试日志）
	statePath  string         // 登录态文件路径
	uin        string         // QQ 号（登录后）
	musickey   string         // musickey（qm_keyst / qqmusic_key）
	loginType  string         // 登录类型（1=QQ 2=微信）
	mu         sync.Mutex     // 状态并发保护
}

// stateData 持久化的登录态（仅敏感最小面）。
type stateData struct {
	Uin       string `json:"uin"`
	Musickey  string `json:"musickey"`
	LoginType string `json:"login_type"`
}

// NewClient 创建客户端（dataDir 为插件数据目录绝对路径）。
func NewClient(dataDir string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second, Jar: jar},
		jar:        jar,
		stateDir:   dataDir,
		statePath:  filepath.Join(dataDir, stateFile),
	}
	_ = c.loadState()
	return c, nil
}

// stateEncKey 派生状态加密密钥（机器固定：固定盐 + v1；纯函数）。
func stateEncKey() []byte {
	sum := sha256.Sum256([]byte("yueyan-qq-music-state" + string(os.PathSeparator) + "v1"))
	return sum[:]
}

// loadState 从文件恢复登录态（无文件/损坏静默忽略）。
func (c *Client) loadState() error {
	raw, err := os.ReadFile(c.statePath)
	if err != nil {
		return err
	}
	plain, err := aesGCMDecrypt(raw, stateEncKey())
	if err != nil {
		return err
	}
	var st stateData
	if err := json.Unmarshal(plain, &st); err != nil {
		return err
	}
	c.mu.Lock()
	c.uin = st.Uin
	c.musickey = st.Musickey
	c.loginType = st.LoginType
	c.mu.Unlock()
	return nil
}

// saveState 持久化登录态（AES 加密）。
func (c *Client) saveState(st stateData) error {
	plain, err := json.Marshal(st)
	if err != nil {
		return err
	}
	enc, err := aesGCMEncrypt(plain, stateEncKey())
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(c.statePath), 0o755)
	return os.WriteFile(c.statePath, enc, 0o600)
}

// debugf 追加一行调试日志到插件数据目录 debug.log（失败静默；扫码登录链路排障用）。
func (c *Client) debugf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(c.stateDir, "debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05")+" "+format+"\n", args...)
}

// randomGuid 生成随机 guid（10 位小写字母数字；纯函数）。
func randomGuid() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:10]
}

// cookieHeader 生成请求 Cookie 头（登录态 + 基础标识）。
func (c *Client) cookieHeader() string {
	c.mu.Lock()
	uin := c.uin
	key := c.musickey
	c.mu.Unlock()
	parts := []string{"uin=" + uin}
	if key != "" {
		parts = append(parts, "qm_keyst="+key, "qqmusic_key="+key)
	}
	return strings.Join(parts, "; ")
}

// vkeyResp musicu.fcg CgiGetVkey 响应结构（仅取用到的字段）。
type vkeyResp struct {
	Req0 struct {
		Code int `json:"code"`
		Data struct {
			Sip        []string `json:"sip"`
			Midurlinfo []struct {
				Songmid  string `json:"songmid"`
				Filename string `json:"filename"`
				Vkey     string `json:"vkey"`
				Result   int    `json:"result"`
			} `json:"midurlinfo"`
		} `json:"data"`
	} `json:"req_0"`
}

// GetVkey 获取歌曲 vkey（musicu.fcg CgiGetVkey）。
// 返回：vkey 密钥、filename 文件名、sip 服务器前缀（第一项）、guid（与 vkey 绑定，播放地址必须复用）。
func (c *Client) GetVkey(songmid string) (string, string, string, string, error) {
	c.mu.Lock()
	uin := c.uin
	c.mu.Unlock()
	guid := randomGuid()
	body := map[string]any{
		"req_0": map[string]any{
			"module": "vkey.GetVkeyServer",
			"method": "CgiGetVkey",
			"param": map[string]any{
				"guid":      guid,
				"songmid":   []string{songmid},
				"songtype":  []int{0},
				"uin":       uin,
				"loginflag": 1,
				"platform":  "20",
			},
		},
		"comm": map[string]any{"uin": 0, "format": "json", "ct": 24, "cv": 0},
	}
	raw, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, vkeyBaseURL, strings.NewReader(string(raw)))
	if err != nil {
		return "", "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
	req.Header.Set("Referer", "https://y.qq.com/")
	req.Header.Set("Cookie", c.cookieHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", "", "", fmt.Errorf("QQ音乐 vkey 接口请求失败: %w", err)
	}
	defer resp.Body.Close()
	bodyRaw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", "", "", err
	}
	var vr vkeyResp
	if err := json.Unmarshal(bodyRaw, &vr); err != nil {
		return "", "", "", "", fmt.Errorf("vkey 响应解析失败: %w", err)
	}
	if len(vr.Req0.Data.Midurlinfo) == 0 {
		return "", "", "", "", fmt.Errorf("歌曲不存在或无法获取播放地址")
	}
	info := vr.Req0.Data.Midurlinfo[0]
	if info.Result != 0 {
		return "", "", "", "", fmt.Errorf("该歌曲受版权或会员限制（result=%d），无法获取播放地址", info.Result)
	}
	if info.Vkey == "" || info.Filename == "" {
		return "", "", "", "", fmt.Errorf("该歌曲需登录或受版权限制，无法获取播放地址")
	}
	sip := ""
	if len(vr.Req0.Data.Sip) > 0 {
		sip = vr.Req0.Data.Sip[0]
	}
	return info.Vkey, info.Filename, sip, guid, nil
}

// aesGCMEncrypt AES-256-GCM 加密（纯函数；base64 便于文件存储）。
func aesGCMEncrypt(plaintext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return []byte(base64.StdEncoding.EncodeToString(sealed)), nil
}

// aesGCMDecrypt AES-256-GCM 解密（纯函数）。
func aesGCMDecrypt(encoded []byte, key []byte) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("状态密文过短")
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
