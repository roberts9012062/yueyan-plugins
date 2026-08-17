// cmd/netease-music-plugin/netease/weapi.go
// 网易云音乐 weapi 加密（进程外插件内部使用；仅用于登录与取播放地址）。
//
// 算法（公开已知，NeteaseCloudMusicApi 同源）：
//   1. 参数 JSON 用 AES-128-CBC 加密（固定 key=0CoJUm6Qyw8W8jud，iv=0102030405060708）
//   2. 结果 base64 后，再用 AES-128-CBC 加密一次（随机 16 字节 key，iv 同上）
//   3. 随机 key 反转后用 RSA 公钥加密 → encSecKey（hex）
//   4. params（二次加密结果 base64）与 encSecKey 一起作为表单提交
package netease

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
)

// 网易云 weapi 固定参数。
const (
	nonceKey     = "0CoJUm6Qyw8W8jud" // 第一次 AES 固定密钥
	nonceIV      = "0102030405060708" // AES 固定 IV
	rsaPublicPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDgtQn2JZ34ZC28NWYpAUd98iZ37BUrX/aKzmFbt7clFSs6sXqHauqKWqdtLkF2KexO40H1YTX8z2lSgBBOAxLsvaklV8k4cBFK9snQXE9/DDaFt6Rr7iVZMldczhC0JNgTz+SHXT6CBHuX3e9SdB1Ua44oncaTWz7OBGLbCiK45wIDAQAB
-----END PUBLIC KEY-----`
)

// secretKeyChars 随机 key 字符集（字母数字）。
const secretKeyChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// pkcs7Pad PKCS7 填充（纯函数）。
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

// aesCBCEncrypt AES-128-CBC 加密（PKCS7 填充；纯函数）。
func aesCBCEncrypt(plaintext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, []byte(nonceIV))
	mode.CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}

// randomSecretKey 生成随机 16 字节字母数字 key（纯函数）。
func randomSecretKey() []byte {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	key := make([]byte, 16)
	for i, b := range buf {
		key[i] = secretKeyChars[int(b)%len(secretKeyChars)]
	}
	return key
}

// reverseBytes 反转字节序（weapi RSA 明文要求逆序；纯函数）。
func reverseBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
}

// rsaEncryptHex RSA 无填充公钥加密并返回 hex（纯函数）。
// 说明：weapi 的 RSA 是「无填充」——明文反转后左补零至公钥长度，再做模幂运算
//       （实测 EncryptPKCS1v15 会失败，必须 NoPadding）。
func rsaEncryptHex(plaintext []byte) (string, error) {
	block, _ := pem.Decode([]byte(rsaPublicPEM))
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", err
	}
	// 明文左补零至公钥长度（128 字节）
	padded := make([]byte, rsaPub.Size())
	copy(padded[rsaPub.Size()-len(plaintext):], plaintext)
	// 无填充模幂：c = m^e mod n
	c := new(big.Int).SetBytes(padded)
	m := new(big.Int).Exp(c, big.NewInt(int64(rsaPub.E)), rsaPub.N)
	out := m.Bytes()
	if len(out) < rsaPub.Size() {
		buf := make([]byte, rsaPub.Size())
		copy(buf[rsaPub.Size()-len(out):], out)
		out = buf
	}
	return hex.EncodeToString(out), nil
}

// WeapiParams 构造 weapi 表单参数（params + encSecKey；纯函数）。
// 参数：data 待加密的请求体（map）。
func WeapiParams(data map[string]any) (string, string, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", "", err
	}
	// 第一次加密（固定 key）
	first, err := aesCBCEncrypt(raw, []byte(nonceKey))
	if err != nil {
		return "", "", err
	}
	firstB64 := base64.StdEncoding.EncodeToString(first)
	// 第二次加密（随机 key）
	secKey := randomSecretKey()
	second, err := aesCBCEncrypt([]byte(firstB64), secKey)
	if err != nil {
		return "", "", err
	}
	params := base64.StdEncoding.EncodeToString(second)
	// RSA 加密随机 key（逆序）
	encSecKey, err := rsaEncryptHex(reverseBytes(secKey))
	if err != nil {
		return "", "", err
	}
	return params, encSecKey, nil
}
