// cmd/netease-music-plugin/netease/client.go
// 网易云音乐 HTTP 客户端（进程外插件内部）：cookie 会话管理 + weapi 请求 + 登录态持久化。
//
// 登录态（MUSIC_U cookie / csrf_token）AES 加密持久化到插件自身数据目录
// （data/plugins/netease-music/state.json），插件重启后自动恢复登录态。
package netease

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 网易云接口基地址。
const baseURL = "https://music.163.com"

// eapiBaseURL eapi 接口基地址（客户端接口；手机号登录用）。
const eapiBaseURL = "https://interface.music.163.com"

// stateFile 登录态文件名（插件数据目录下）。
const stateFile = "state.json"

// Client 网易云音乐客户端（连接器类，仅用于外部系统接口）。
type Client struct {
	httpClient *http.Client // HTTP 客户端（cookie jar 自动管理会话）
	statePath  string       // 登录态文件路径（data/plugins/netease-music/state.json）
	csrfToken  string       // csrf token（登录后从 cookie 提取）
	profile    *Profile     // 登录用户资料（内存缓存）
	urlCache   map[int64]urlCacheEntry // 播放地址缓存（20 分钟 TTL，减频防风控）
	mu         sync.Mutex   // 状态并发保护
}

// urlCacheEntry 播放地址缓存条目。
type urlCacheEntry struct {
	url       string    // 播放地址（mp3 直链）
	expi      int64     // 有效期（秒）
	fetchedAt time.Time // 获取时间（过期判断用）
}

// stateData 持久化的登录态（仅敏感最小面）。
type stateData struct {
	Cookies   []*http.Cookie `json:"cookies"`    // 会话 cookie（MUSIC_U / __csrf）
	Nickname  string         `json:"nickname"`   // 登录用户昵称
	UserID    int64          `json:"user_id"`    // 登录用户 ID
	AvatarURL string         `json:"avatar_url"` // 登录用户头像
}

// NewClient 创建客户端（dataDir 为插件数据目录绝对路径，如 data/plugins/netease-music）。
func NewClient(dataDir string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second, Jar: jar},
		statePath:  filepath.Join(dataDir, stateFile),
		urlCache:   make(map[int64]urlCacheEntry),
	}
	// 启动恢复登录态（失败不影响——视为未登录）
	_ = c.loadState()
	return c, nil
}

// stateEncKey 派生状态加密密钥（机器固定：进程目录 + 固定盐；纯函数）。
func stateEncKey() []byte {
	sum := sha256.Sum256([]byte("yueyan-netease-music-state" + string(os.PathSeparator) + "v1"))
	return sum[:]
}

// loadState 从文件恢复登录态（cookie 回填 jar；无文件/损坏静默忽略）。
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
	base, _ := url.Parse(baseURL)
	c.httpClient.Jar.SetCookies(base, st.Cookies)
	c.csrfToken = csrfFromCookies(st.Cookies)
	c.profile = &Profile{UserID: st.UserID, Nickname: st.Nickname, AvatarURL: st.AvatarURL}
	return nil
}

// saveState 持久化登录态（AES 加密；登录/登出后调用）。
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

// csrfFromCookies 从 cookie 列表提取 csrf token（__csrf；纯函数）。
func csrfFromCookies(cookies []*http.Cookie) string {
	for _, ck := range cookies {
		if ck.Name == "__csrf" {
			return ck.Value
		}
	}
	return ""
}

// weapiRequest 发起 weapi 请求（POST，表单 params/encSecKey + 会话 cookie）。
// withClientCookie 控制是否带 os=pc 客户端标识 cookie：扫码登录接口（unikey/check）
// 实测带上会被网易云视为 PC 客户端而收不到扫码回调，需不带。
func (c *Client) weapiRequest(path string, data map[string]any, withClientCookie bool) ([]byte, error) {
	params, encSecKey, err := WeapiParams(data)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("params", params)
	form.Set("encSecKey", encSecKey)

	c.mu.Lock()
	token := c.csrfToken
	c.mu.Unlock()
	// 取地址等接口需 csrf_token 拼在 query 上
	fullPath := path
	if token != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		fullPath = path + sep + "csrf_token=" + url.QueryEscape(token)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+fullPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
	req.Header.Set("Referer", baseURL+"/")
	if withClientCookie {
		// 匿名请求也需客户端标识 cookie（搜索/取地址实测缺少会被拒/音质降级）
		req.Header.Set("Cookie", "os=pc; appver=9.3.40")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网易云接口请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网易云接口返回异常状态: %d", resp.StatusCode)
	}
	return body, nil
}

// WeapiRequest 发起 weapi 请求（带客户端标识 cookie；搜索/取地址等业务接口用）。
func (c *Client) WeapiRequest(path string, data map[string]any) ([]byte, error) {
	return c.weapiRequest(path, data, true)
}

// WeapiRequestQR 发起扫码登录 weapi 请求（不带客户端标识 cookie）。
func (c *Client) WeapiRequestQR(path string, data map[string]any) ([]byte, error) {
	return c.weapiRequest(path, data, false)
}

// EapiRequest 发起 eapi 请求（POST params=<大写hex>；客户端接口如手机号登录）。
// 参数：path eapi 路径（如 /eapi/w/login/cellphone）；data 请求体 map。
// 返回：响应 JSON bytes。
func (c *Client) EapiRequest(path string, data map[string]any) ([]byte, error) {
	params, err := EapiParams(path, data)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("params", params)

	req, err := http.NewRequest(http.MethodPost, eapiBaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "NeteaseMusic/9.3.40.1753206443(164);Dalvik/2.1.0 (Linux; U; Android 9; MIX 2 MIUI/V12.0.1.0.PDECNXM)")
	req.Header.Set("Cookie", "os=android; appver=9.3.40")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网易云 eapi 接口请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网易云 eapi 接口返回异常状态: %d", resp.StatusCode)
	}
	return body, nil
}

// Cookies 返回当前会话 cookie（登录后持久化用）。
func (c *Client) Cookies() []*http.Cookie {
	base, _ := url.Parse(baseURL)
	return c.httpClient.Jar.Cookies(base)
}

// UpdateSession 更新会话状态（登录成功后记录 cookie + 用户信息并持久化）。
func (c *Client) UpdateSession(cookies []*http.Cookie, nickname string, userID int64, avatarURL string) error {
	base, _ := url.Parse(baseURL)
	c.httpClient.Jar.SetCookies(base, cookies)
	c.mu.Lock()
	c.csrfToken = csrfFromCookies(cookies)
	c.profile = &Profile{UserID: userID, Nickname: nickname, AvatarURL: avatarURL}
	c.mu.Unlock()
	return c.saveState(stateData{Cookies: cookies, Nickname: nickname, UserID: userID, AvatarURL: avatarURL})
}

// ClearSession 清除会话（登出后清 cookie 与状态文件）。
func (c *Client) ClearSession() error {
	base, _ := url.Parse(baseURL)
	c.httpClient.Jar.SetCookies(base, nil)
	c.mu.Lock()
	c.csrfToken = ""
	c.profile = nil
	c.mu.Unlock()
	return os.Remove(c.statePath)
}

// SessionState 会话状态（登录态 + 用户资料；供插件 API /status 查询）。
type SessionState struct {
	LoggedIn bool     // 是否已登录
	Profile  *Profile // 用户资料（未登录为 nil）
}

// State 返回当前登录态只读快照。
func (c *Client) State() SessionState {
	base, _ := url.Parse(baseURL)
	hasMUSIC := false
	for _, ck := range c.httpClient.Jar.Cookies(base) {
		if ck.Name == "MUSIC_U" && ck.Value != "" {
			hasMUSIC = true
			break
		}
	}
	c.mu.Lock()
	profile := c.profile
	c.mu.Unlock()
	return SessionState{LoggedIn: hasMUSIC, Profile: profile}
}

// aesGCMEncrypt AES-256-GCM 加密（纯函数）。
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
	// base64 便于文件存储
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
