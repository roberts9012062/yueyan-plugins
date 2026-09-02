// nav-links/private.go
// 私有导航访问控制：配置存储（访问模式 + 密码哈希 + token 签名密钥）与门禁 API。
//
// 设计要点：
//   - 访问模式 mode：self=仅自己可见（站长登录态）；password=密码访问（访客解锁 token）；
//   - 访问密码仅存 SHA-256(salt+password) 哈希，绝不落明文（故不放宿主插件设置——那是明文库）；
//   - 解锁 token："{expUnix}.{HMAC-SHA256(secret, "unlock:"+expUnix)}"，7 天有效；
//     修改密码即轮换 secret，全部旧 token 随之失效；
//   - 存储文件：插件数据目录 private.json（临时文件 + rename 原子写，同 links.json）。
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// 私有访问模式取值。
const (
	privateModeSelf     = "self"     // 仅自己可见（站长登录态）
	privateModePassword = "password" // 密码访问（访客凭密码解锁）
)

// token 有效期与密码长度边界。
const (
	privateTokenTTL   = 7 * 24 * time.Hour // 解锁 token 有效期（7 天）
	privatePassMinLen = 6                  // 访问密码最小长度
	privatePassMaxLen = 64                 // 访问密码最大长度
)

// privateConfig 落盘结构（PasswordHash/Salt/Secret 均为 hex；Title/Subtitle 为私有页文案）。
type privateConfig struct {
	Mode         string `json:"mode"`
	PasswordHash string `json:"password_hash"`
	Salt         string `json:"salt"`
	Secret       string `json:"secret"`
	Title        string `json:"title"`
	Subtitle     string `json:"subtitle"`
}

// PrivateStore 私有访问配置存储（文件系统连接器：互斥锁 + 原子写）。
type PrivateStore struct {
	mu   sync.Mutex
	path string        // private.json 绝对路径
	cfg  privateConfig // 当前配置（Load 后有效）
}

// NewPrivateStore 创建存储（dir 为插件数据目录；默认 self 模式，Load 后生效）。
func NewPrivateStore(dir string) *PrivateStore {
	return &PrivateStore{
		path: filepath.Join(dir, "private.json"),
		cfg: privateConfig{
			Mode:     privateModeSelf,
			Title:    "私有导航",
			Subtitle: "仅对站长与获准访客可见的收藏站点",
		},
	}
}

// Load 从磁盘加载（文件不存在视为首次使用，保持默认值不落盘；损坏返回错误阻断激活）。
func (s *PrivateStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cfg privateConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return errors.New("private.json 数据损坏：" + err.Error())
	}
	// 兜底：非法模式回退 self（保守可见面最小）
	if cfg.Mode != privateModePassword {
		cfg.Mode = privateModeSelf
	}
	if cfg.Title == "" {
		cfg.Title = "私有导航"
	}
	if cfg.Subtitle == "" {
		cfg.Subtitle = "仅对站长与获准访客可见的收藏站点"
	}
	s.cfg = cfg
	return nil
}

// saveLocked 落盘（调用方须已持锁；临时文件 + rename 原子替换）。
func (s *PrivateStore) saveLocked() error {
	raw, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot 返回配置副本（只读出口；Secret/PasswordHash 仅供包内校验使用）。
func (s *PrivateStore) Snapshot() privateConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Update 更新配置（mode 必填；password 非空才重设并轮换 secret；纯校验后落盘）。
// password 模式要求「已设密码或本次提供」，否则拒绝切换。
func (s *PrivateStore) Update(mode string, password string, title string, subtitle string) error {
	if mode != privateModeSelf && mode != privateModePassword {
		return errors.New("访问方式需为 self（仅自己）或 password（密码访问）")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "私有导航"
	}
	if len([]rune(title)) > 30 {
		return errors.New("私有页标题不能超过 30 字")
	}
	subtitle = strings.TrimSpace(subtitle)
	if len([]rune(subtitle)) > 60 {
		return errors.New("私有页副标题不能超过 60 字")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Mode = mode
	next.Title = title
	next.Subtitle = subtitle
	if password != "" {
		runeCount := len([]rune(password))
		if runeCount < privatePassMinLen || runeCount > privatePassMaxLen {
			return errors.New("访问密码长度需在 6-64 位之间")
		}
		salt, err := randomHex(16)
		if err != nil {
			return errors.New("密码处理失败：" + err.Error())
		}
		secret, err := randomHex(32)
		if err != nil {
			return errors.New("密码处理失败：" + err.Error())
		}
		next.Salt = salt
		next.PasswordHash = hashPrivatePassword(salt, password)
		next.Secret = secret // 轮换签名密钥：旧解锁 token 全部失效
	}
	if next.Mode == privateModePassword && next.PasswordHash == "" {
		return errors.New("密码访问需先设置访问密码")
	}
	s.cfg = next
	if err := s.saveLocked(); err != nil {
		return errors.New("保存失败：" + err.Error())
	}
	return nil
}

// VerifyPassword 校验访问密码（未设置密码返回 false）。
func (s *PrivateStore) VerifyPassword(password string) bool {
	cfg := s.Snapshot()
	if cfg.PasswordHash == "" || cfg.Salt == "" {
		return false
	}
	return hmacEqual(cfg.PasswordHash, hashPrivatePassword(cfg.Salt, password))
}

// IssueToken 签发解锁 token（当前 secret；返回 token 与过期 unix 秒）。
func (s *PrivateStore) IssueToken() (string, int64) {
	cfg := s.Snapshot()
	exp := time.Now().Add(privateTokenTTL).Unix()
	return signPrivateToken(cfg.Secret, exp), exp
}

// VerifyToken 校验解锁 token（格式/过期/HMAC 三关；secret 轮换后自然失效）。
func (s *PrivateStore) VerifyToken(token string) bool {
	cfg := s.Snapshot()
	exp, ok := parsePrivateTokenExp(token)
	if !ok || time.Now().Unix() > exp {
		return false
	}
	return hmacEqual(signPrivateToken(cfg.Secret, exp), token)
}

// hashPrivatePassword 密码哈希：SHA-256(salt + password) → hex（纯函数）。
func hashPrivatePassword(salt string, password string) string {
	sum := sha256.Sum256([]byte(salt + password))
	return hex.EncodeToString(sum[:])
}

// signPrivateToken 组装 token："{exp}.{HMAC-SHA256(secret, "unlock:"+exp)}"（纯函数）。
func signPrivateToken(secretHex string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secretHex))
	mac.Write([]byte("unlock:" + strconv.FormatInt(exp, 10)))
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

// parsePrivateTokenExp 解析 token 的 exp 段（格式非法返回 false；纯函数）。
func parsePrivateTokenExp(token string) (int64, bool) {
	dot := strings.Index(token, ".")
	if dot <= 0 {
		return 0, false
	}
	exp, err := strconv.ParseInt(token[:dot], 10, 64)
	if err != nil || exp <= 0 {
		return 0, false
	}
	return exp, true
}

// hmacEqual 恒定时间比较两个 hex 串（长度不等直接 false；纯函数）。
func hmacEqual(a string, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// randomHex 生成 n 字节随机数的 hex 编码（盐/密钥生成用）。
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// registerPrivateAPI 注册私有导航门禁 API：
//   - GET  /private/config  管理页读取配置（TrustedCaller）
//   - POST /private/config  管理页保存配置（TrustedCaller；body {mode,password?,title?,subtitle?}）
//   - POST /private/meta    公开元数据（宿主桥接 → 前台门禁 UI；不含任何密钥材料）
//   - POST /private/unlock  密码解锁（宿主桥接；body {password} → {token,expires_in}）
//   - POST /private/links   私有数据（宿主桥接；body {admin,token} 按鉴权矩阵放行）
func registerPrivateAPI(api *sdk.APIMux, p *NavLinksPlugin) {
	// metaPayload 公开元数据载荷（纯函数：配置 + 私有条数 → 前台可安全展示的字段）。
	// 注意 store 在 handler 内实时获取（注册时插件可能尚未激活）。
	metaPayload := func() map[string]any {
		priv := p.privSafe()
		if priv == nil {
			return map[string]any{"mode": privateModeSelf, "has_password": false, "title": "私有导航", "subtitle": "", "count": 0}
		}
		cfg := priv.Snapshot()
		count := 0
		if st := p.storeSafe(); st != nil {
			count = len(st.ListPrivate())
		}
		return map[string]any{
			"mode":         cfg.Mode,
			"has_password": cfg.PasswordHash != "",
			"title":        cfg.Title,
			"subtitle":     cfg.Subtitle,
			"count":        count,
		}
	}

	// 读取配置（管理页「私有设置」卡片回显）
	api.Handle("GET", "/private/config", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		return 200, jsonResp(metaPayload()), nil
	})

	// 保存配置（密码非空才更新哈希并轮换 token 密钥）
	api.Handle("POST", "/private/config", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		priv := p.privSafe()
		if priv == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Mode     string `json:"mode"`
			Password string `json:"password"`
			Title    string `json:"title"`
			Subtitle string `json:"subtitle"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return 400, jsonResp(map[string]any{"error": "请求体需为 JSON 对象"}), nil
		}
		if err := priv.Update(req.Mode, req.Password, req.Title, req.Subtitle); err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(metaPayload()), nil
	})

	// 公开元数据（前台门禁 UI 数据源；宿主 System 桥接，登录用户直调亦无敏感泄露）
	api.Handle("POST", "/private/meta", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if p.privSafe() == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		return 200, jsonResp(metaPayload()), nil
	})

	// 密码解锁（self 模式不给密码通道；正确返回 token）
	api.Handle("POST", "/private/unlock", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		priv := p.privSafe()
		if priv == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		cfg := priv.Snapshot()
		if cfg.Mode != privateModePassword {
			return 403, jsonResp(map[string]any{"error": "当前访问方式为「仅自己可见」，不支持密码解锁", "code": "self_only"}), nil
		}
		var req struct {
			Password string `json:"password"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return 400, jsonResp(map[string]any{"error": "请求体需为 JSON 对象"}), nil
		}
		if !priv.VerifyPassword(req.Password) {
			return 401, jsonResp(map[string]any{"error": "访问密码不正确", "code": "bad_password"}), nil
		}
		token, exp := priv.IssueToken()
		return 200, jsonResp(map[string]any{"token": token, "expires_at": exp}), nil
	})

	// 私有数据（鉴权矩阵：admin 直通；password 模式 token 放行；否则 self_only/need_password）
	api.Handle("POST", "/private/links", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		st := p.storeSafe()
		priv := p.privSafe()
		if st == nil || priv == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Admin bool   `json:"admin"`
			Token string `json:"token"`
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				return 400, jsonResp(map[string]any{"error": "请求体需为 JSON 对象"}), nil
			}
		}
		cfg := priv.Snapshot()
		allowed := req.Admin || (cfg.Mode == privateModePassword && priv.VerifyToken(req.Token))
		if !allowed {
			if cfg.Mode == privateModeSelf {
				return 403, jsonResp(map[string]any{"error": "此导航仅站长可见", "code": "self_only"}), nil
			}
			return 401, jsonResp(map[string]any{"error": "请输入访问密码解锁", "code": "need_password"}), nil
		}
		links := st.ListPrivate()
		settings := publicSettings(sdk.Config(ctx))
		return 200, jsonResp(map[string]any{
			"links":      links,
			"categories": aggregateCategories(links),
			"tags":       aggregateTags(links),
			"settings": map[string]string{
				"page_title":    cfg.Title,
				"page_subtitle": cfg.Subtitle,
				"open_new_tab":  settings["open_new_tab"],
			},
		}), nil
	})
}
