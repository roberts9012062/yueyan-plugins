// cmd/bilibili-video-plugin/bilibili/token.go
// 游客 B 站登录态 token（guest_token）：游客在前台扫码后，其 B 站 cookie
// 由插件 AES 加密封装为自包含 token 返回浏览器保存（localStorage），
// 之后播放请求携带 token、插件解密后代为解析高清流。
//
// 设计动机：服务端不落盘访客凭证（隐私与存储最小化）；token 仅经宿主同源
// 请求往返，密钥由插件进程派生。有效期跟随 B 站 cookie 效期（约 90 天封顶）。
package bilibili

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// guestTokenTTL 游客 token 有效期（秒）。
const guestTokenTTL = 90 * 24 * time.Hour

// guestTokenPayload token 明文结构。
type guestTokenPayload struct {
	Cookies  []*http.Cookie `json:"cookies"`  // 游客 B 站登录 cookie
	Nickname string         `json:"nickname"` // 游客 B 站昵称（前端展示）
	IssuedAt int64          `json:"issued_at"`// 签发时间戳（秒）
}

// guestTokenKey 派生 token 加密密钥（机器固定；纯函数）。
func guestTokenKey() []byte {
	sum := sha256.Sum256([]byte("yueyan-bilibili-guest-token" + string(os.PathSeparator) + "v1"))
	return sum[:]
}

// SealGuestToken 封装游客登录态为 token（纯函数）。
func SealGuestToken(cookies []*http.Cookie, nickname string) (string, error) {
	payload, err := json.Marshal(guestTokenPayload{
		Cookies:  cookies,
		Nickname: nickname,
		IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		return "", err
	}
	enc, err := aesGCMEncrypt(payload, guestTokenKey())
	if err != nil {
		return "", err
	}
	return string(enc), nil
}

// OpenGuestToken 解封游客 token（过期/伪造返回错误；纯函数）。
func OpenGuestToken(token string) ([]*http.Cookie, string, error) {
	if token == "" {
		return nil, "", fmt.Errorf("token 为空")
	}
	plain, err := aesGCMDecrypt([]byte(token), guestTokenKey())
	if err != nil {
		return nil, "", fmt.Errorf("token 无效")
	}
	var p guestTokenPayload
	if err := json.Unmarshal(plain, &p); err != nil {
		return nil, "", fmt.Errorf("token 解析失败")
	}
	if time.Since(time.Unix(p.IssuedAt, 0)) > guestTokenTTL {
		return nil, "", fmt.Errorf("token 已过期，请重新扫码")
	}
	if len(p.Cookies) == 0 {
		return nil, "", fmt.Errorf("token 无登录态")
	}
	return p.Cookies, p.Nickname, nil
}
