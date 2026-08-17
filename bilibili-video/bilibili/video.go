// cmd/bilibili-video-plugin/bilibili/video.go
// B 站视频信息与播放地址解析：
//   - view 接口：bvid → cid/标题/封面/时长/UP 主（发帖编辑器解析用）；
//   - playurl 接口（WBI 签名）：bvid+cid+qn → 真实 mp4 流地址（durl 段列表）。
//
// 清晰度档位（qn）：16=360P / 32=480P / 64=720P / 80=1080P；
// 匿名最高 480P，登录可达 1080P（112+ 需大会员，本插件不涉及）。
// 流地址约 2 小时时效 + http 前缀统一转 https（避免浏览器混合内容拦截）。
package bilibili

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 视频清晰度 qn 常量（公开契约）。
const (
	QN360  = 16 // 360P
	QN480  = 32 // 480P
	QN720  = 64 // 720P
	QN1080 = 80 // 1080P
)

// VideoInfo 视频信息（view 接口脱敏快照，取第一 P）。
type VideoInfo struct {
	Bvid    string `json:"bvid"`    // 视频 BV 号
	Cid     int64  `json:"cid"`     // 第一 P 的 cid（playurl 必需）
	Title   string `json:"title"`   // 标题
	Cover   string `json:"cover"`   // 封面 URL
	Duration int64  `json:"duration"` // 时长（秒）
	Author  string `json:"author"`  // UP 主昵称
}

// DurlSeg durl 播放段（mp4；多段顺序播放）。
type DurlSeg struct {
	URL    string `json:"url"`    // 段地址（https）
	Size   int64  `json:"size"`   // 字节大小
	Length int64  `json:"length"` // 时长（毫秒）
}

// DashStream DASH 单流（音/视频分离的 m4s；1080P 及以上仅有此形态）。
type DashStream struct {
	ID        int    `json:"id"`         // 档位 qn（视频流）或音质 id（音频流）
	BaseURL   string `json:"base_url"`   // 流地址（https；完整 fMP4）
	Codecs    string `json:"codecs"`     // 编码（视频流优先 avc1，浏览器兼容最好）
	Bandwidth int    `json:"bandwidth"`  // 带宽（kbps，同档多编码时取低者）
	BackupURLs []string `json:"backup_urls,omitempty"` // 备用地址
}

// DashGroup DASH 流组（video 按档位列出，audio 通常一条）。
type DashGroup struct {
	Video []DashStream `json:"video"` // 视频流（含多档位/多编码）
	Audio []DashStream `json:"audio"` // 音频流
}

// PlayInfo 播放地址解析结果。
type PlayInfo struct {
	Quality       int       `json:"quality"`         // 实际返回的清晰度 qn
	Durl          []DurlSeg `json:"durl"`            // mp4 播放段列表（fnval=1；上限 720P）
	Dash          *DashGroup `json:"dash,omitempty"` // DASH 流组（fnval=16；1080P 仅有此形态）
	AcceptQuality []int     `json:"accept_quality"`  // 该视频支持的 qn 全表
	AcceptDesc    []string  `json:"accept_desc"`     // 与上表对应的描述
	Timelength    int64     `json:"timelength"`      // 总时长（毫秒）
}

// View 获取视频信息（bvid 解析；匿名可调）。
func (c *Client) View(bvid string) (*VideoInfo, error) {
	raw, err := c.getJSON(apiBase+"/x/web-interface/view?bvid="+url.QueryEscape(bvid), nil)
	if err != nil {
		return nil, err
	}
	var d struct {
		Bvid     string `json:"bvid"`
		Cid      int64  `json:"cid"`
		Title    string `json:"title"`
		Pic      string `json:"pic"`
		Duration int64  `json:"duration"`
		Owner    struct {
			Name string `json:"name"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || d.Cid == 0 {
		return nil, fmt.Errorf("视频信息解析失败")
	}
	return &VideoInfo{
		Bvid: d.Bvid, Cid: d.Cid, Title: d.Title,
		Cover: httpsURL(d.Pic), Duration: d.Duration, Author: d.Owner.Name,
	}, nil
}

// PlayURL 解析播放地址（qn 目标清晰度；extraCookies 为空时用站长会话，非空则优先用之）。
// 走 html5 平台端点 + fnval=16（DASH）：免 WBI 签名；匿名可达 720P，登录可达 1080P
// （实测 mp4/durl 路径上限 720P，1080P 仅有 DASH 形态）；B 站在身份不满足目标
// 清晰度时自动降级（quality 字段为实际档位）。
func (c *Client) PlayURL(bvid string, cid int64, qn int, extraCookies []*http.Cookie) (*PlayInfo, error) {
	cookies := extraCookies
	if len(cookies) == 0 {
		cookies = c.SessionCookies()
	}
	params := url.Values{}
	params.Set("bvid", bvid)
	params.Set("cid", strconv.FormatInt(cid, 10))
	params.Set("qn", strconv.Itoa(qn))
	params.Set("fnval", "16") // DASH（音视频分离 m4s，经前端 MSE 播放）
	params.Set("platform", "html5")
	params.Set("high_quality", "1")
	raw, err := c.getJSON(apiBase+"/x/player/playurl?"+params.Encode(), cookies)
	if err != nil {
		return nil, err
	}
	return parsePlayInfo(raw)
}

// parsePlayInfo 解析 playurl 响应 data（纯函数）。
func parsePlayInfo(raw []byte) (*PlayInfo, error) {
	var d struct {
		Quality       int      `json:"quality"`
		AcceptQuality []int    `json:"accept_quality"`
		AcceptDesc    []string `json:"accept_description"`
		Timelength    int64    `json:"timelength"`
		Durl          []struct {
			URL    string `json:"url"`
			Size   int64  `json:"size"`
			Length int64  `json:"length"`
		} `json:"durl"`
		Dash *struct {
			Video []struct {
				ID       int      `json:"id"`
				BaseURL  string   `json:"baseUrl"`
				Codecs   string   `json:"codecs"`
				Bandwidth int     `json:"bandwidth"`
				BackupURL []string `json:"backupUrl"`
			} `json:"video"`
			Audio []struct {
				ID       int      `json:"id"`
				BaseURL  string   `json:"baseUrl"`
				Codecs   string   `json:"codecs"`
				Bandwidth int     `json:"bandwidth"`
				BackupURL []string `json:"backupUrl"`
			} `json:"audio"`
		} `json:"dash"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("播放地址解析失败")
	}
	info := &PlayInfo{
		Quality:       d.Quality,
		AcceptQuality: d.AcceptQuality,
		AcceptDesc:    d.AcceptDesc,
		Timelength:    d.Timelength,
	}
	for _, seg := range d.Durl {
		info.Durl = append(info.Durl, DurlSeg{
			URL:    httpsURL(seg.URL),
			Size:   seg.Size,
			Length: seg.Length,
		})
	}
	if d.Dash != nil && len(d.Dash.Video) > 0 {
		group := &DashGroup{}
		for _, v := range d.Dash.Video {
			group.Video = append(group.Video, DashStream{
				ID: v.ID, BaseURL: httpsURL(v.BaseURL),
				Codecs: v.Codecs, Bandwidth: v.Bandwidth, BackupURLs: v.BackupURL,
			})
		}
		for _, a := range d.Dash.Audio {
			group.Audio = append(group.Audio, DashStream{
				ID: a.ID, BaseURL: httpsURL(a.BaseURL),
				Codecs: a.Codecs, Bandwidth: a.Bandwidth, BackupURLs: a.BackupURL,
			})
		}
		info.Dash = group
	}
	if len(info.Durl) == 0 && info.Dash == nil {
		return nil, fmt.Errorf("播放地址解析失败（视频可能为付费/地区限制内容）")
	}
	return info, nil
}

// QualityDesc qn → 描述文案（纯函数；未知档位返回原文数字）。
func QualityDesc(qn int) string {
	switch qn {
	case QN360:
		return "360P"
	case QN480:
		return "480P"
	case QN720:
		return "720P"
	case QN1080:
		return "1080P"
	case 74:
		return "720P60"
	case 112:
		return "1080P+"
	case 116:
		return "1080P60"
	case 120:
		return "4K"
	}
	return strconv.Itoa(qn)
}

// NeedLogin qn 是否需要登录 B 站（720P 起；纯函数）。
func NeedLogin(qn int) bool {
	return qn >= QN720
}

// ExpandShortLink 展开 b23.tv 短链为最终 URL（跟随重定向后取最终请求地址）。
func (c *Client) ExpandShortLink(shortURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, shortURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", desktopUA)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // 读完释放连接
	if resp.Request == nil || resp.Request.URL == nil {
		return "", fmt.Errorf("短链展开失败")
	}
	return resp.Request.URL.String(), nil
}

// httpsURL 把 http:// 地址升级为 https://（纯函数；B 站 CDN 双协议支持）。
func httpsURL(raw string) string {
	if strings.HasPrefix(raw, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}
