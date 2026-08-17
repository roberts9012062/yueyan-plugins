// cmd/bilibili-video-plugin/bilibili/client.go
// B 站 HTTP 客户端（进程外插件内部）：显式 cookie 会话管理 + 登录态持久化 + 基础风控 cookie。
//
// 设计要点（与音乐插件差异）：所有请求显式携带 Cookie 头、不使用 cookie jar——
// 因为游客扫码（guest_token）与站长登录态并存，共享 jar 会互相污染。
// 站长登录态（SESSDATA / bili_jct / DedeUserID 等）AES 加密持久化到
// 插件数据目录 data/plugins/bilibili-video/state.json，重启自动恢复。
package bilibili

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// B 站接口基地址常量。
const (
	apiBase      = "https://api.bilibili.com"
	passportBase = "https://passport.bilibili.com"
)

// desktopUA 桌面浏览器 UA（B 站接口风控基础）。
const desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// stateFile 站长登录态文件名（插件数据目录下）。
const stateFile = "state.json"

// Profile 登录用户资料（nav 接口脱敏快照）。
type Profile struct {
	Mid      int64  `json:"mid"`       // 用户 ID
	Nickname string `json:"nickname"`  // 昵称
	Avatar   string `json:"avatar"`    // 头像 URL
	Vip      bool   `json:"vip"`       // 是否大会员（1080P 高清解锁参考）
	Level    int    `json:"level"`     // 当前等级
}

// stateData 持久化的站长登录态（仅敏感最小面：cookie + 资料）。
type stateData struct {
	Cookies  []*http.Cookie `json:"cookies"`  // 登录会话 cookie（SESSDATA 等）
	Profile  *Profile       `json:"profile"`  // 登录用户资料
	SavedAt  int64          `json:"saved_at"` // 保存时间戳（秒）
}

// Client B 站客户端（连接器类，仅用于外部系统接口）。
type Client struct {
	httpClient    *http.Client   // HTTP 客户端（无 jar，cookie 显式管理）
	statePath     string         // 登录态文件路径（data/plugins/bilibili-video/state.json）
	baseCookies   []*http.Cookie // 基础风控 cookie（buvid3/buvid4，匿名也携带）
	cookies       []*http.Cookie // 站长登录 cookie（内存；空 = 未登录）
	profile       *Profile       // 站长资料（内存缓存；可能为空，懒刷新补全）
	mu            sync.Mutex     // 状态并发保护
}

// NewClient 创建客户端（dataDir 为插件数据目录绝对路径）。
// 初始化即拉取 buvid3 基础 cookie（失败不阻断——B 站部分接口无 buvid3 也可用）。
func NewClient(dataDir string) (*Client, error) {
	c := &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		statePath:  filepath.Join(dataDir, stateFile),
	}
	// 匿名基础 cookie（buvid3/buvid2，finger/spi 接口获取）
	c.fetchBaseCookies()
	// 启动恢复站长登录态（失败不影响——视为未登录）
	_ = c.loadState()
	return c, nil
}

// fetchBaseCookies 拉取匿名基础 cookie（buvid3；B 站 2024 起多数接口要求）。
func (c *Client) fetchBaseCookies() {
	req, err := http.NewRequest(http.MethodGet, apiBase+"/x/frontend/finger/spi", nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", desktopUA)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}
	var spi struct {
		Data struct {
			B3 string `json:"b_3"`
			B2 string `json:"b_2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &spi); err != nil {
		return
	}
	if spi.Data.B3 != "" {
		c.mu.Lock()
		c.baseCookies = []*http.Cookie{
			{Name: "buvid3", Value: spi.Data.B3, Domain: ".bilibili.com", Path: "/"},
			{Name: "buvid2", Value: spi.Data.B2, Domain: ".bilibili.com", Path: "/"},
		}
		c.mu.Unlock()
	}
}

// ---------- 请求基础设施 ----------

// doRequest 发起 HTTP 请求（显式 Cookie 头；extraCookies 附加在基础 cookie 之后）。
// 返回：响应 body 与响应 Set-Cookie 解析结果（扫码/登录场景需捕获新 cookie）。
func (c *Client) doRequest(method string, url string, contentType string, payload io.Reader, extraCookies []*http.Cookie) ([]byte, []*http.Cookie, error) {
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", desktopUA)
	// 注意：不携带 Referer——B 站 WAF 对「非浏览器 TLS 指纹 + UA + Referer 组合」
	// 返回 412（实测仅 UA 或仅 Referer 均放行，二者叠加必拦），故只保留 UA。
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// 基础 cookie（buvid3）+ 会话 cookie 合并（会话优先——同名覆盖）
	c.mu.Lock()
	merged := append(append([]*http.Cookie{}, c.baseCookies...), c.cookies...)
	c.mu.Unlock()
	merged = append(merged, extraCookies...)
	req.Header.Set("Cookie", CookieHeader(merged))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("B站接口请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("B站接口响应读取失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("B站接口返回异常状态: %d", resp.StatusCode)
	}
	return body, resp.Cookies(), nil
}

// apiResp B 站 API 通用响应信封。
type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// getJSON GET 请求并解包 B 站信封（code!=0 转错误）。
func (c *Client) getJSON(url string, extraCookies []*http.Cookie) (json.RawMessage, error) {
	body, _, err := c.doRequest(http.MethodGet, url, "", nil, extraCookies)
	if err != nil {
		return nil, err
	}
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("B站响应解析失败")
	}
	if r.Code != 0 {
		return nil, &APIError{Code: r.Code, Message: r.Message}
	}
	return r.Data, nil
}

// postForm POST 表单请求并解包 B 站信封（返回响应 body 与 Set-Cookie）。
func (c *Client) postForm(url string, form string, extraCookies []*http.Cookie) (json.RawMessage, []*http.Cookie, error) {
	body, setCookies, err := c.doRequest(http.MethodPost, url, "application/x-www-form-urlencoded", strings.NewReader(form), extraCookies)
	if err != nil {
		return nil, nil, err
	}
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, fmt.Errorf("B站响应解析失败")
	}
	if r.Code != 0 {
		return nil, nil, &APIError{Code: r.Code, Message: r.Message}
	}
	return r.Data, setCookies, nil
}

// APIError B 站业务错误（code 语义：-101 未登录 / -403 权限风控 / -105 需要验证码）。
type APIError struct {
	Code    int
	Message string
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	return fmt.Sprintf("B站接口错误 %d: %s", e.Code, e.Message)
}

// ---------- 登录态存取 ----------

// stateEncKey 派生状态加密密钥（机器固定：固定盐 + 版本；纯函数）。
func stateEncKey() []byte {
	sum := sha256.Sum256([]byte("yueyan-bilibili-state" + string(os.PathSeparator) + "v1"))
	return sum[:]
}

// loadState 从文件恢复站长登录态（无文件/损坏静默忽略）。
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
	c.cookies = st.Cookies
	c.profile = st.Profile
	c.mu.Unlock()
	return nil
}

// saveState 持久化站长登录态（AES 加密）。
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

// UpdateSession 更新站长会话（登录成功后记录 cookie + 资料并持久化）。
func (c *Client) UpdateSession(cookies []*http.Cookie, profile *Profile) error {
	c.mu.Lock()
	c.cookies = cookies
	c.profile = profile
	c.mu.Unlock()
	return c.saveState(stateData{
		Cookies: cookies,
		Profile: profile,
		SavedAt: time.Now().Unix(),
	})
}

// ClearSession 清除站长会话（登出：清内存与状态文件）。
func (c *Client) ClearSession() error {
	c.mu.Lock()
	c.cookies = nil
	c.profile = nil
	c.mu.Unlock()
	return os.Remove(c.statePath)
}

// SessionCookies 返回站长登录 cookie（只读副本；未登录为空）。
func (c *Client) SessionCookies() []*http.Cookie {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*http.Cookie{}, c.cookies...)
}

// State 返回站长登录态只读快照。
func (c *Client) State() (loggedIn bool, profile *Profile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	loggedIn = false
	for _, ck := range c.cookies {
		if ck.Name == "SESSDATA" && ck.Value != "" {
			loggedIn = true
			break
		}
	}
	return loggedIn, c.profile
}

// EnsureProfile 确保已登录时有资料（缺失则经 nav 补拉并回写内存与磁盘；失败静默）。
// 场景：扫码成功时 nav 可能被 B 站风控临时拦截，资料留空——后续查询时补全。
func (c *Client) EnsureProfile() error {
	c.mu.Lock()
	hasSession := false
	for _, ck := range c.cookies {
		if ck.Name == "SESSDATA" && ck.Value != "" {
			hasSession = true
			break
		}
	}
	profile := c.profile
	cookies := append([]*http.Cookie{}, c.cookies...)
	c.mu.Unlock()
	if !hasSession || (profile != nil && profile.Nickname != "") {
		return nil
	}
	fresh, err := c.GuestNavProfile(cookies)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.profile = fresh
	saved := c.cookies
	c.mu.Unlock()
	return c.saveState(stateData{Cookies: saved, Profile: fresh, SavedAt: time.Now().Unix()})
}

// CookieHeader 把 cookie 列表拼成请求头值（纯函数；跳过空值项）。
func CookieHeader(cookies []*http.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, ck := range cookies {
		if ck.Name == "" || ck.Value == "" {
			continue
		}
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}

// MergeCookies 合并两组 cookie（后者同名覆盖前者；纯函数）。
func MergeCookies(base []*http.Cookie, overlay []*http.Cookie) []*http.Cookie {
	index := make(map[string]int, len(base)+len(overlay))
	out := make([]*http.Cookie, 0, len(base)+len(overlay))
	for _, ck := range base {
		if ck.Name == "" || ck.Value == "" {
			continue
		}
		if i, ok := index[ck.Name]; ok {
			out[i] = ck
			continue
		}
		index[ck.Name] = len(out)
		out = append(out, ck)
	}
	for _, ck := range overlay {
		if ck.Name == "" || ck.Value == "" {
			continue
		}
		if i, ok := index[ck.Name]; ok {
			out[i] = ck
			continue
		}
		index[ck.Name] = len(out)
		out = append(out, ck)
	}
	return out
}

// aesGCMEncrypt AES-256-GCM 加密（纯函数；base64 编码便于文件存储）。
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
