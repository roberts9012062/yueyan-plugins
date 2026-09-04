// marketplace-repo/tg-image-bed/telegram_test.go
// 发送响应解析单测：四种 Message 形态（photo 数组 / document / sticker / animation）。
// sticker 与 animation 为 TG 服务器自动转码的隐性行为（webp→贴纸、gif→mp4），v0.4.1 实测踩坑。
package main

import (
	"encoding/json"
	"testing"
)

func TestParseSendResult_Document(t *testing.T) {
	raw := json.RawMessage(`{"message_id":101,"document":{"file_id":"DOC1","file_size":329805}}`)
	out, err := parseSendResult(raw)
	if err != nil || out.FileID != "DOC1" || out.Size != 329805 || out.MessageID != 101 {
		t.Fatalf("document 解析错误：%+v err=%v", out, err)
	}
}

func TestParseSendResult_PhotoPicksLargest(t *testing.T) {
	raw := json.RawMessage(`{"message_id":1,"photo":[{"file_id":"S","file_size":100},{"file_id":"L","file_size":900}]}`)
	out, err := parseSendResult(raw)
	if err != nil || out.FileID != "L" {
		t.Fatalf("photo 应取最大尺寸：%+v err=%v", out, err)
	}
}

func TestParseSendResult_Sticker(t *testing.T) {
	// webp 被 TG 转为贴纸（无 document 字段；file_id 经 getFile 可下载回 webp）
	raw := json.RawMessage(`{"message_id":2,"sticker":{"file_id":"STK1","file_size":1234,"width":512,"height":512}}`)
	out, err := parseSendResult(raw)
	if err != nil || out.FileID != "STK1" || out.Size != 1234 {
		t.Fatalf("sticker 解析错误：%+v err=%v", out, err)
	}
}

func TestParseSendResult_Animation(t *testing.T) {
	// gif 被 TG 转码为 mp4 动画（无 document 字段）
	raw := json.RawMessage(`{"message_id":3,"animation":{"file_id":"ANI1","file_size":8888,"duration":2}}`)
	out, err := parseSendResult(raw)
	if err != nil || out.FileID != "ANI1" || out.Size != 8888 {
		t.Fatalf("animation 解析错误：%+v err=%v", out, err)
	}
}

func TestParseSendResult_EmptyStillFails(t *testing.T) {
	// 四种形态全缺：维持原报错
	raw := json.RawMessage(`{"message_id":4,"text":"纯文本"}`)
	if _, err := parseSendResult(raw); err == nil || err.Error() != "发送响应缺少 photo/document 字段" {
		t.Fatalf("空形态应报错，实际 err=%v", err)
	}
}
