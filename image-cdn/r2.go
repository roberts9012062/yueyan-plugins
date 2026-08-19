// marketplace-repo/image-cdn/r2.go
// Cloudflare Worker 客户端：配对探测（/health）与图片转存（/upload）。
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Worker 请求超时（上传含图片体，放宽到 30s；健康探测 8s 快速失败）。
const (
	workerHealthTimeout = 8 * time.Second
	workerUploadTimeout = 30 * time.Second
)

// workerUploadResult Worker /upload 响应。
type workerUploadResult struct {
	URL  string `json:"url"`  // 公开访问 URL
	Key  string `json:"key"`  // R2 对象键（storage_key）
	Size int64  `json:"size"` // 字节数
	Mime string `json:"mime"` // MIME 类型
}

// storageResult 插件契约响应（/storage/upload 成功载荷）。
type storageResult struct {
	Type       string `json:"type"`
	StorageKey string `json:"storage_key"`
	URL        string `json:"url"`
	Mime       string `json:"mime"`
	Size       int64  `json:"size"`
}

// newWorkerClient 构造指定超时的 HTTP 客户端（纯函数）。
func newWorkerClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// pingWorker 配对探测：GET {url}/health（Bearer key）→ {"ok":true}。
func pingWorker(baseURL string, apiKey string) error {
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := newWorkerClient(workerHealthTimeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil || !body.OK {
		reason := body.Error
		if reason == "" {
			reason = "响应异常"
		}
		return fmt.Errorf("%s", reason)
	}
	return nil
}

// uploadToWorker 图片转存：content_b64 解码 → multipart file → POST {url}/upload
// （Bearer key）→ 契约响应。Type 由扩展名判定（image——白名单校验宿主已前置）。
func uploadToWorker(baseURL string, apiKey string, filename string, mimeType string, content64 string) (*storageResult, error) {
	content, err := base64.StdEncoding.DecodeString(content64)
	if err != nil {
		return nil, fmt.Errorf("内容解码失败：%w", err)
	}
	// multipart 组装（字段名 file，与 Worker 契约一致）
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(content); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(baseURL, "/")+"/upload", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := newWorkerClient(workerUploadTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("Worker 请求失败（检查 URL 可达性）：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var result workerUploadResult
	if err := json.Unmarshal(raw, &result); err != nil || result.URL == "" {
		return nil, fmt.Errorf("Worker 响应异常（缺 url 字段）")
	}
	return &storageResult{
		Type:       "image", // 上传白名单前置校验已限定图片类（宿主 DetectType）
		StorageKey: result.Key,
		URL:        result.URL,
		Mime:       result.Mime,
		Size:       result.Size,
	}, nil
}

// listWorker 对象列表：GET {url}/list?cursor=（Bearer key）→ Worker 响应原样字节。
func listWorker(baseURL string, apiKey string, cursor string) ([]byte, error) {
	target := strings.TrimSuffix(baseURL, "/") + "/list"
	if cursor != "" {
		target += "?cursor=" + url.QueryEscape(cursor)
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := newWorkerClient(workerHealthTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("Worker 请求失败：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// deleteWorkerObject 删除对象：DELETE {url}/f/:key（Bearer key；key 路径转义防注入）。
func deleteWorkerObject(baseURL string, apiKey string, objectKey string) error {
	target := strings.TrimSuffix(baseURL, "/") + "/f/" + url.PathEscape(objectKey)
	req, err := http.NewRequest(http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := newWorkerClient(workerHealthTimeout).Do(req)
	if err != nil {
		return fmt.Errorf("Worker 请求失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// base64Decode 标准 base64 解码薄封装（统一导入处）。
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// base64Encode 标准 base64 编码薄封装。
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
