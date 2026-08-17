// cmd/bilibili-video-plugin/bilibili/sms.go
// B 站手机号验证码登录（passport web sms 体系）。
//
// 风控说明：B 站 2023 起短信接口绑定极验滑块（recaptcha_token / 极验四件套），
// 服务器端无浏览器环境时发送可能被拒（-105 等）——调用方收到 NeedCaptcha=true
// 时应提示用户改用扫码登录（本插件双通道设计的降级路径）。
package bilibili

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// SMSResult 短信操作结果。
type SMSResult struct {
	OK          bool            // 是否成功
	NeedCaptcha bool            // 是否被极验风控拦截（前端提示改用扫码）
	Message     string          // 提示文案
	Cookies     []*http.Cookie // 登录成功时的 cookie（仅 login 有值）
}

// smsCaptchaCodes 需要滑块验证的 B 站错误码集合（判定依据，纯数据）。
var smsCaptchaCodes = map[int]bool{
	-105: true, // 验证码校验失败（需要极验）
	-799: true, // 需要验证码
	886:  true, // 极验 challenge 校验失败
	-403: true, // 风控拦截
}

// SendSMS 发送短信验证码（countryCode 国际区号，默认 86）。
func (c *Client) SendSMS(tel string, countryCode int, sessionCookies []*http.Cookie) (*SMSResult, error) {
	if countryCode <= 0 {
		countryCode = 86
	}
	form := url.Values{}
	form.Set("tel", tel)
	form.Set("country_id", strconv.Itoa(countryCode))
	form.Set("recaptcha_token", "")

	data, setCookies, err := c.postForm(passportBase+"/x/passport-login/web/sms/send", form.Encode(), sessionCookies)
	if err != nil {
		return smsErrorResult(err), nil
	}
	var d struct {
		RecaptchaURL string `json:"recaptcha_url"`
	}
	_ = json.Unmarshal(data, &d)
	if d.RecaptchaURL != "" {
		return &SMSResult{OK: false, NeedCaptcha: true, Message: "B站要求滑块验证，请改用扫码登录"}, nil
	}
	return &SMSResult{OK: true, Message: "验证码已发送，请查收短信", Cookies: setCookies}, nil
}

// SMSLogin 短信验证码登录（code 为 6 位验证码；成功捕获登录 cookie）。
func (c *Client) SMSLogin(tel string, code string, countryCode int, sessionCookies []*http.Cookie) (*SMSResult, error) {
	if countryCode <= 0 {
		countryCode = 86
	}
	form := url.Values{}
	form.Set("tel", tel)
	form.Set("code", code)
	form.Set("country_id", strconv.Itoa(countryCode))

	_, setCookies, err := c.postForm(passportBase+"/x/passport-login/web/login/sms", form.Encode(), sessionCookies)
	if err != nil {
		return smsErrorResult(err), nil
	}
	if len(setCookies) == 0 {
		return &SMSResult{OK: false, Message: "登录成功但未捕获到 cookie"}, nil
	}
	return &SMSResult{OK: true, Message: "登录成功", Cookies: setCookies}, nil
}

// smsErrorResult 把接口错误转为 SMSResult（识别极验风控码；纯函数）。
func smsErrorResult(err error) *SMSResult {
	if apiErr, ok := err.(*APIError); ok {
		needCaptcha := smsCaptchaCodes[apiErr.Code]
		msg := apiErr.Message
		if msg == "" {
			msg = "B站接口返回错误"
		}
		if needCaptcha {
			msg = "B站要求滑块验证，请改用扫码登录（" + msg + "）"
		}
		return &SMSResult{OK: false, NeedCaptcha: needCaptcha, Message: msg}
	}
	return &SMSResult{OK: false, Message: "B站接口请求失败：" + err.Error()}
}
