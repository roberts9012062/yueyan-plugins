// marketplace-repo/tg-image-bed/transfer_test.go
// 外链转存纯函数单测：MIME 规范化与文件名推导（安全校验恒过、扩展名规范化）。
package main

import (
	"strings"
	"testing"
)

func TestNormalizeMime(t *testing.T) {
	cases := map[string]string{
		"image/png":                "image/png",  // 标准形态
		"Image/PNG":                "image/png",  // 大小写归一
		"image/jpeg; charset=utf8": "image/jpeg", // 去参数
		"text/html":                "",           // 非图片
		"image/bmp":                "",           // 不在白名单
		"":                         "",           // 空
	}
	for in, want := range cases {
		if got := normalizeMime(in); got != want {
			t.Fatalf("normalizeMime(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestFilenameForURL(t *testing.T) {
	// 常规：路径尾段 + 扩展名规范化（jpeg→jpg）
	name, err := filenameForURL("https://img.example.com/a/photo.JPEG?w=100", "image/jpeg")
	if err != nil || name != "photo.jpg" {
		t.Fatalf("期望 photo.jpg，实际 %q err=%v", name, err)
	}
	// 无扩展名尾段：按 MIME 补
	name, _ = filenameForURL("https://img.example.com/pic123", "image/png")
	if !strings.HasSuffix(name, ".png") {
		t.Fatalf("期望按 MIME 补 .png，实际 %q", name)
	}
	// 无尾段（纯 query）：哈希兜底命名 + 扩展名
	name, _ = filenameForURL("https://img.example.com/?id=abc", "image/webp")
	if !strings.HasPrefix(name, "transfer-") || !strings.HasSuffix(name, ".webp") {
		t.Fatalf("期望 transfer-*.webp，实际 %q", name)
	}
	// 非法 MIME：拒绝
	if _, err := filenameForURL("https://x.example/a.png", "text/html"); err == nil {
		t.Fatal("非图片 MIME 应报错")
	}
}

func TestFilenameForURL_Safety(t *testing.T) {
	// 安全校验恒过：不含路径分隔符与 ..（与直传 handleUpload 同款校验）
	for _, raw := range []string{
		"https://x.example/a/../b.png", "https://x.example/a%2Fb%3F.png", "https://x.example/../../etc/passwd.png",
	} {
		name, err := filenameForURL(raw, "image/png")
		if err != nil {
			t.Fatalf("意外失败：%v", err)
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "..") || strings.ContainsAny(lower, `/\`) {
			t.Fatalf("文件名不合法（含路径成分）：%q", name)
		}
	}
}
