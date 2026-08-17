// cmd/netease-music-plugin/netease/eapi.go
// 网易云音乐 eapi 加密（客户端接口：手机号登录用；weapi 登录会触发 8821 行为验证码）。
//
// 算法：对「路径 + 明文 JSON + md5 摘要」整体做 AES-128-ECB 加密，输出大写 hex。
//   1. 路径中 eapi 替换为 api（如 /eapi/w/login/cellphone → /api/w/login/cellphone）
//   2. message = "nobody" + url + "use" + text + "md5forencrypt"
//   3. digest = md5hex(message)
//   4. data = url + "-36cd479b6b5-" + text + "-36cd479b6b5-" + digest
//   5. params = UPPER(hex(AES-128-ECB(data, key=e82ckenh8dichen8)))
package netease

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// eapiKey eapi AES 密钥（客户端固定）。
const eapiKey = "e82ckenh8dichen8"

// eapiSalt eapi 盐串。
const eapiSalt = "36cd479b6b5"

// aesECBEncrypt AES-128-ECB 加密（PKCS7 填充；纯函数）。
func aesECBEncrypt(plaintext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pad := block.BlockSize() - len(plaintext)%block.BlockSize()
	padded := make([]byte, len(plaintext)+pad)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	out := make([]byte, len(padded))
	for bs := 0; bs < len(padded); bs += block.BlockSize() {
		block.Encrypt(out[bs:bs+block.BlockSize()], padded[bs:bs+block.BlockSize()])
	}
	return out, nil
}

// EapiParams eapi 加密，返回 params（大写 hex；纯函数）。
// 参数：path eapi 路径（如 /eapi/w/login/cellphone）；data 请求体 map。
func EapiParams(path string, data map[string]any) (string, error) {
	text, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	// 路径中 eapi 替换为 api（仅第一处）
	apiPath := strings.Replace(path, "eapi", "api", 1)
	message := fmt.Sprintf("nobody%suse%smd5forencrypt", apiPath, string(text))
	digest := md5.Sum([]byte(message))
	payload := fmt.Sprintf("%s-%s-%s-%s-%x", apiPath, eapiSalt, string(text), eapiSalt, digest)
	cipherText, err := aesECBEncrypt([]byte(payload), []byte(eapiKey))
	if err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(cipherText)), nil
}
