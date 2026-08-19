// marketplace-repo/image-cdn/compress.go
// 服务端图片压缩：按后台设置（开关/质量/最大边长）压缩后转存 R2。
// 前端发帖上传已有 canvas 压缩（≤1MB webp），本层兜底"直传大图"场景
// （后台管理页/媒体库直传、第三方客户端）——两层幂等：已达标的跳过。
package main

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/gif" // 解码注册（gif/webp 原样转发不压缩）
	"image/png"
	"strings"

	"golang.org/x/image/draw"
)

// 压缩设置回退默认（设置缺省/非法时兜底）。
const (
	defaultCompressQuality = 80  // JPEG 质量（1-100）
	defaultMaxDimension    = 1920 // 最大边长（像素；0=不缩放）
)

// compressConfig 压缩设置（从插件配置解析；纯函数）。
type compressConfig struct {
	Enabled bool // 服务端压缩开关
	Quality int  // JPEG 质量（1-100）
	MaxDim  int  // 最大边长像素（≤0=不缩放）
}

// parseCompressConfig 从配置快照解析压缩参数（非法值回退默认）。
func parseCompressConfig(cfg map[string]string) compressConfig {
	return compressConfig{
		Enabled: strings.TrimSpace(cfg["compress_enabled"]) == "on",
		Quality: clampInt(atoiSafe(cfg["compress_quality"]), 30, 95, defaultCompressQuality),
		MaxDim:  clampInt(atoiSafe(cfg["max_dimension"]), 0, 8192, defaultMaxDimension),
	}
}

// atoiSafe 十进制解析（失败返回 0；纯函数）。
func atoiSafe(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// clampInt 区间夹取（越界/零值回退默认；纯函数）。
func clampInt(v int, min int, max int, fallback int) int {
	if v == 0 {
		return fallback
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// imageExt 取小写扩展名（无点返回空串；纯函数）。
func imageExt(filename string) string {
	if idx := strings.LastIndex(filename, "."); idx >= 0 {
		return strings.ToLower(filename[idx+1:])
	}
	return ""
}

// compressImage 压缩入口：不启用/解码失败/越压越大 → 原样返回（nil, nil 标识未变）。
// 支持范围：JPEG（质量+缩放）、PNG（缩放重编码）；GIF/WebP 原样（编码器不支持）。
func compressImage(cc compressConfig, filename string, mimeType string, content []byte) ([]byte, string, error) {
	if !cc.Enabled {
		return nil, "", nil
	}
	ext := imageExt(filename)
	if ext != "jpg" && ext != "jpeg" && ext != "png" {
		return nil, "", nil // gif/webp：无编码器，原样转发
	}
	img, format, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, "", nil // 解码失败不阻断：原样上传
	}
	// 等比缩放（超边长才缩）
	out := scaleIfNeeded(cc.MaxDim, img)
	// 编码（JPEG 质量 / PNG 无损重编码）
	buf := &bytes.Buffer{}
	if format == "png" && ext == "png" {
		if err := png.Encode(buf, out); err != nil {
			return nil, "", nil
		}
	} else {
		if err := jpeg.Encode(buf, out, &jpeg.Options{Quality: cc.Quality}); err != nil {
			return nil, "", nil
		}
	}
	if buf.Len() >= len(content) {
		return nil, "", nil // 压缩无收益：用原图
	}
	mime := "image/png"
	if ext != "png" {
		mime = "image/jpeg"
	}
	return buf.Bytes(), mime, nil
}

// scaleIfNeeded 超过最大边长时等比缩放（x/image/draw 双线性；不超或无效边长返回原图）。
func scaleIfNeeded(maxDim int, img image.Image) image.Image {
	if maxDim <= 0 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || (w <= maxDim && h <= maxDim) {
		return img
	}
	ratio := float64(maxDim) / float64(w)
	if h > w {
		ratio = float64(maxDim) / float64(h)
	}
	nw, nh := int(float64(w)*ratio+0.5), int(float64(h)*ratio+0.5)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}
