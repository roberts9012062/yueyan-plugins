// cmd/qq-music-plugin/qq/qr.go
// QQ 登录扫码（ptlogin2）：xlogin 拿签名 cookie → ptqrshow 二维码 → ptqrlogin 轮询。
// 说明：QQ 音乐登录框实际走 QQ 登录（ptlogin2）体系，本文件复现其扫码流程。
package qq

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// xloginBase xlogin 接口（获取登录签名 cookie）。
const xloginBase = "https://xui.ptlogin2.qq.com/cgi-bin/xlogin"

// ptqrshowBase ptqrshow 接口（获取二维码）。
const ptqrshowBase = "https://xui.ptlogin2.qq.com/ssl/ptqrshow"

// ptqrloginBase ptqrlogin 接口（轮询扫码状态）。
const ptqrloginBase = "https://xui.ptlogin2.qq.com/ssl/ptqrlogin"

// ptOAuthJump OAuth 跳转地址（登录成功后回调）。
const ptOAuthJump = "https://graph.qq.com/oauth2.0/login_jump"

// ptAppid ptlogin2 应用 ID（QQ 音乐网页版 QQ 登录）。
const ptAppid = "716027609"

// ptDaid ptlogin2 daid。
const ptDaid = "383"

// ptThirdAid 第三方应用 ID（y.qq.com）。
const ptThirdAid = "100497308"

// ptUA ptlogin2 请求 UA。
const ptUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"

// hash33 计算 ptqrtoken（ptlogin2 哈希算法；纯函数）。
func hash33(s string) int {
	h := 0
	for _, r := range s {
		h += (h << 5) + int(r)
	}
	return h & 0x7fffffff
}

// QrInit 初始化扫码登录：xlogin 拿签名 cookie → ptqrshow 拿二维码。
// 返回：qrsig 轮询签名；qrPNG 二维码 data URL；错误。
func (c *Client) QrInit() (string, string, error) {
	// 1. xlogin 拿登录签名 cookie（pt_login_sig 等，进 jar）
	xloginURL := xloginBase + "?appid=" + ptAppid + "&daid=" + ptDaid + "&style=33&login_text=%E7%99%BB%E5%BD%95" +
		"&hide_title_bar=1&hide_border=1&target=self&s_url=" + url.QueryEscape(ptOAuthJump) +
		"&pt_3rd_aid=" + ptThirdAid + "&pt_feedback_link=https%3A%2F%2Fsupport.qq.com%2Fproducts%2F77942%3FcustomInfo%3D.appid100497308&theme=2&verify_theme="
	if _, err := c.qrGet(xloginURL); err != nil {
		return "", "", err
	}

	// 2. ptqrshow 拿二维码 + qrsig
	ts := fmt.Sprintf("%.6f", float64(time.Now().UnixNano())/1e9)
	qrURL := ptqrshowBase + "?appid=" + ptAppid + "&e=2&l=M&s=3&d=72&v=4&t=" + ts +
		"&daid=" + ptDaid + "&pt_3rd_aid=" + ptThirdAid + "&u1=" + url.QueryEscape(ptOAuthJump)
	png, err := c.qrGet(qrURL)
	if err != nil {
		return "", "", err
	}
	qrsig := c.cookieValue("qrsig")
	if qrsig == "" {
		return "", "", fmt.Errorf("获取扫码签名失败")
	}
	return qrsig, "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// QrCheck 轮询扫码状态。
// 返回：状态码（66 待扫码 / 67 已扫待确认 / 0 成功 / 65 过期）；
//       跳转 URL（成功时非空，OAuth 换取登录态用）。
func (c *Client) QrCheck(qrsig string) (int, string, error) {
	ptqrtoken := hash33(qrsig)
	loginSig := c.cookieValue("pt_login_sig")
	ts := fmt.Sprintf("0-0-%d", time.Now().UnixMilli())
	loginURL := ptqrloginBase + "?u1=" + url.QueryEscape(ptOAuthJump) +
		"&ptqrtoken=" + fmt.Sprint(ptqrtoken) + "&ptredirect=0&h=1&t=1&g=1&from_ui=1&ptlang=2052" +
		"&action=" + ts + "&js_ver=26071711&js_type=1&login_sig=" + url.QueryEscape(loginSig) +
		"&pt_uistyle=40&aid=" + ptAppid + "&daid=" + ptDaid + "&pt_3rd_aid=" + ptThirdAid + "&pt_js_version=c1987b96"
	body, err := c.qrGet(loginURL)
	if err != nil {
		return 0, "", err
	}
	// 解析 JSONP：ptuiCB('code','0','跳转URL','0','msg','应用名')
	code, redirectURL := parsePtuiCB(string(body))
	if code != 66 {
		c.debugf("ptqrlogin code=%d redirect=%q body_len=%d raw=%s", code, redirectURL, len(body), truncateBytes(string(body), 1500))
	}
	return code, redirectURL, nil
}

// QrOAuth 登录成功后走 OAuth 换取 QQ 音乐登录态。
// 说明：访问 ptqrlogin 返回的跳转 URL（ssl.ptlogin2.qq.com/jump → graph.qq.com OAuth → y.qq.com），
//       跟随重定向，最终 y.qq.com 设置 uin + musickey cookie，提取后持久化登录态。
func (c *Client) QrOAuth(redirectURL string) error {
	if redirectURL == "" {
		return fmt.Errorf("缺少跳转地址")
	}
	c.debugf("QrOAuth start redirect=%q", redirectURL)
	// 复制共享客户端：为本次 OAuth 挂重定向打点（不影响其他请求）。
	hc := *c.httpClient
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		c.debugf("QrOAuth redirect (%d) -> %s", len(via)+1, req.URL.String())
		return nil
	}
	// 访问跳转 URL（跟随重定向；cookie 自动进 jar）
	req, err := http.NewRequest(http.MethodGet, redirectURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ptUA)
	req.Header.Set("Referer", "https://xui.ptlogin2.qq.com/")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("OAuth 跳转请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	finalURL := ""
	if resp.Request != nil {
		finalURL = resp.Request.URL.String()
	}
	c.debugf("QrOAuth check_sig final status=%d url=%q body=%s", resp.StatusCode, finalURL, truncateBytes(string(body), 400))

	// 2. 提交 authorize 拿授权码（官网流程：login_jump postMessage 后由 show 页 JS 提交 authorize 表单，
	//    302 到 y.qq.com/portal/wx_redirect.html?code=...）。
	authQuery := "response_type=code&client_id=" + ptThirdAid +
		"&redirect_uri=" + url.QueryEscape("https://y.qq.com/portal/wx_redirect.html?login_type=1&surl=https%3A%2F%2Fy.qq.com%2F") +
		"&scope=get_user_info%2Cget_app_friends&state=state"
	code, err := c.oauthAuthorize(&hc, authQuery)
	if err != nil {
		return err
	}

	// 3. 用 code 调 QQConnectLogin.QQLogin 换 QQ 音乐登录态（Set-Cookie qm_keyst/uin 进 jar）。
	if err := c.qqConnectLogin(&hc, code); err != nil {
		return err
	}

	// 从 y.qq.com 域读取登录态 cookie（先打点全部 cookie 名，便于排障）
	names := make([]string, 0)
	for _, ck := range c.jar.Cookies(mustURL("https://y.qq.com/")) {
		names = append(names, ck.Name)
	}
	c.debugf("QrOAuth y.qq.com cookies=%v", names)
	uin := c.cookieValueDomain("uin", "y.qq.com")
	musickey := c.cookieValueDomain("qm_keyst", "y.qq.com")
	if musickey == "" {
		musickey = c.cookieValueDomain("qqmusic_key", "y.qq.com")
	}
	loginType := c.cookieValueDomain("login_type", "y.qq.com")
	if uin == "" || musickey == "" {
		return fmt.Errorf("OAuth 未返回登录态（uin=%s musickey 长度=%d）", uin, len(musickey))
	}
	uin = keepDigits(uin)
	c.mu.Lock()
	c.uin = uin
	c.musickey = musickey
	c.loginType = loginType
	c.mu.Unlock()
	c.debugf("QrOAuth success uin=%s musickey_len=%d", uin, len(musickey))
	return c.saveState(stateData{Uin: uin, Musickey: musickey, LoginType: loginType})
}

// oauthAuthorize 提交 authorize 换取授权码（纯 POST，参数与官网一致，含 g_tk CSRF 签名）。
// 返回：授权码 code（302 落在 wx_redirect.html?code=... 时提取）。
func (c *Client) oauthAuthorize(hc *http.Client, authQuery string) (string, error) {
	// 组装表单：基础授权参数（解码还原后统一 Encode，避免双重编码）+ 官网固定附加字段。
	form := url.Values{}
	for _, kv := range strings.Split(authQuery, "&") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			val, err := url.QueryUnescape(parts[1])
			if err != nil {
				val = parts[1]
			}
			form.Set(parts[0], val)
		}
	}
	pSkey := c.cookieValueDomain("p_skey", "graph.qq.com")
	form.Set("switch", "")
	form.Set("from_ptlogin", "1")
	form.Set("src", "1")
	form.Set("update_auth", "1")
	form.Set("openapi", "1010_1030")
	form.Set("g_tk", strconv.Itoa(gtk33(pSkey)))
	form.Set("auth_time", strconv.FormatInt(time.Now().UnixMilli(), 10))
	form.Set("ui", uuidV4())
	postBody := form.Encode()
	c.debugf("QrOAuth authorize(POST) p_skey_len=%d body=%s", len(pSkey), postBody)

	req, err := http.NewRequest(http.MethodPost, "https://graph.qq.com/oauth2.0/authorize", strings.NewReader(postBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ptUA)
	req.Header.Set("Referer", "https://graph.qq.com/oauth2.0/login_jump")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("authorize POST 失败: %w", err)
	}
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	final := ""
	if resp.Request != nil {
		final = resp.Request.URL.String()
	}
	c.debugf("QrOAuth authorize(POST) status=%d url=%s", resp.StatusCode, final)
	code := extractCode(final)
	if code == "" {
		return "", fmt.Errorf("authorize 未返回授权码（status=%d url=%s）", resp.StatusCode, final)
	}
	return code, nil
}

// qqConnectLogin 用授权码调 QQConnectLogin.QQLogin 换 QQ 音乐登录态（Set-Cookie qm_keyst/uin 进 jar）。
func (c *Client) qqConnectLogin(hc *http.Client, code string) error {
	payload := `{"comm":{"g_tk":5381,"platform":"yqq","ct":24,"cv":0},"req":{"module":"QQConnectLogin.LoginServer","method":"QQLogin","param":{"code":"` + code + `"}}}`
	req, err := http.NewRequest(http.MethodPost, "https://u.y.qq.com/cgi-bin/musicu.fcg", strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ptUA)
	req.Header.Set("Referer", "https://y.qq.com/")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("QQLogin 请求失败: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	c.debugf("QrOAuth qqLogin status=%d body=%s", resp.StatusCode, truncateBytes(string(body), 300))
	return nil
}

// gtk33 计算 g_tk（QQ CSRF 签名；初始 5381，与 ptqrtoken 的 hash33 初始 0 不同；纯函数）。
func gtk33(s string) int {
	h := 5381
	for _, r := range s {
		h += (h << 5) + int(r)
	}
	return h & 0x7fffffff
}

// extractCode 从最终跳转 URL 提取 OAuth 授权码（纯函数）。
func extractCode(finalURL string) string {
	u, err := url.Parse(finalURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("code")
}

// uuidV4 生成随机 UUID v4（authorize 表单 ui 字段；纯函数）。
func uuidV4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// truncateBytes 按字节截断字符串（防日志刷屏；纯函数）。
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// mustURL 构造 URL（仅静态常量使用，失败时返回空 URL；纯函数）。
func mustURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

// cookieValueDomain 从 cookie jar 读取指定域名下的 cookie 值。
func (c *Client) cookieValueDomain(name string, domain string) string {
	if c.jar == nil {
		return "" // 健壮性防护：客户端未完成初始化（jar 空）时无会话 cookie 可读
	}
	u, _ := url.Parse("https://" + domain + "/")
	for _, ck := range c.jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// qrGet 发起 ptlogin2 GET 请求（cookie 自动进 jar；返回响应体）。
func (c *Client) qrGet(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ptUA)
	req.Header.Set("Referer", "https://xui.ptlogin2.qq.com/")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ptlogin2 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// cookieValue 从 cookie jar 读取指定 cookie 值（ptlogin2.qq.com 域）。
func (c *Client) cookieValue(name string) string {
	if c.jar == nil {
		return "" // 健壮性防护：客户端未完成初始化（jar 空）时无会话 cookie 可读
	}
	u, _ := url.Parse("https://ptlogin2.qq.com/")
	for _, ck := range c.jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// parsePtuiCB 解析 ptqrlogin JSONP 响应（纯函数；手写按单引号切分，兼容响应尾部任意格式）。
// 返回：状态码（66 待扫 / 67 已扫待确认 / 0 成功 / 65 过期）；跳转 URL（仅成功时非空）。
// 响应形如 ptuiCB('0','0','https://ssl.ptlogin2.graph.qq.com/check_sig?...','0','登录成功！','')
func parsePtuiCB(raw string) (int, string) {
	idx := strings.Index(raw, "ptuiCB(")
	if idx < 0 {
		return -1, ""
	}
	s := raw[idx+len("ptuiCB("):]
	// 依次提取最多 6 个单引号字符串参数（不关心尾随字符与空白）。
	args := make([]string, 0, 6)
	p := 0
	for len(args) < 6 {
		q1 := strings.IndexByte(s[p:], '\'')
		if q1 < 0 {
			break
		}
		p += q1 + 1
		q2 := strings.IndexByte(s[p:], '\'')
		if q2 < 0 {
			break
		}
		args = append(args, s[p:p+q2])
		p += q2 + 1
	}
	if len(args) < 3 {
		return -1, ""
	}
	code, err := strconv.Atoi(args[0])
	if err != nil {
		return -1, ""
	}
	// 跳转 URL 在第 3 参数；为空时兜底第 6 参数（部分老版本响应）。
	jump := strings.ReplaceAll(args[2], "&amp;", "&")
	if !strings.HasPrefix(jump, "http") && len(args) > 5 {
		jump = strings.ReplaceAll(args[5], "&amp;", "&")
	}
	if !strings.HasPrefix(jump, "http") {
		jump = ""
	}
	return code, jump
}

// strings 保留引用（部分构建环境需要）
var _ = strings.TrimSpace
