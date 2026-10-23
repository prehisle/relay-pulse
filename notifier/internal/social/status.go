// Package social 生成社媒草稿（周报、多家同时异常），私发给站长人工审阅后再转发。
//
// 数据只取主站公开的 /api/status，不消费 /api/events：事件流是给订阅推送用的单游标，
// 且不区分板块，拿来判断「当前热板的整体状况」口径不对。
package social

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// 主站状态码：1 绿 / 2 黄 / 0 红，其余为无数据
const statusRed = 0

// StatusGroup /api/status 返回的一个通道（只取本模块用到的字段）
type StatusGroup struct {
	Provider      string        `json:"provider"`
	ProviderName  string        `json:"provider_name"`
	Service       string        `json:"service"`
	Channel       string        `json:"channel"`
	ChannelName   string        `json:"channel_name"`
	ListedDays    int           `json:"listed_days"`
	IntervalMS    int64         `json:"interval_ms"`
	CurrentStatus int           `json:"current_status"`
	Layers        []StatusLayer `json:"layers"`
}

// StatusLayer 通道下的一个模型层
type StatusLayer struct {
	Model         string `json:"model"`
	CurrentStatus struct {
		Status    int   `json:"status"`
		Timestamp int64 `json:"timestamp"`
	} `json:"current_status"`
	Timeline []StatusBucket `json:"timeline"`
}

// StatusBucket 时间桶
type StatusBucket struct {
	Time         string  `json:"time"`
	Timestamp    int64   `json:"timestamp"`    // 桶起点（Unix 秒）
	Availability float64 `json:"availability"` // 按主站权重算好的可用率，无数据时为 -1
	Latency      int     `json:"latency"`
	StatusCounts struct {
		Available   int `json:"available"`
		Degraded    int `json:"degraded"`
		Unavailable int `json:"unavailable"`
		Missing     int `json:"missing"`
	} `json:"status_counts"`
}

// probes 桶内探测次数；与主站 timeline 聚合的 total 口径一致
func (b StatusBucket) probes() int {
	c := b.StatusCounts
	return c.Available + c.Degraded + c.Unavailable + c.Missing
}

// displayName 给人看的名字：服务商展示名 + 通道展示名
func (g StatusGroup) displayName() string {
	name := g.ProviderName
	if name == "" {
		name = g.Provider
	}
	ch := g.ChannelName
	if ch == "" {
		ch = g.Channel
	}
	if ch == "" {
		return name
	}
	return name + " · " + ch
}

// StatusClient 读取主站公开状态接口
type StatusClient struct {
	siteURL string
	http    *http.Client
}

// NewStatusClient 创建状态客户端
func NewStatusClient(siteURL string) *StatusClient {
	return &StatusClient{siteURL: siteURL, http: &http.Client{Timeout: 60 * time.Second}}
}

// FetchHot 拉取热板通道（period: 90m / 7d 等）
func (c *StatusClient) FetchHot(ctx context.Context, period string) ([]StatusGroup, error) {
	q := url.Values{"period": {period}, "board": {"hot"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.siteURL+"/api/status?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 /api/status 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("/api/status 返回 %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Groups []StatusGroup `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析 /api/status 失败: %w", err)
	}
	if len(out.Groups) == 0 {
		return nil, fmt.Errorf("/api/status 未返回任何热板通道")
	}
	return out.Groups, nil
}

// serviceLabel 服务方向的展示名
func serviceLabel(service string) string {
	switch service {
	case "cc":
		return "Claude Code"
	case "cx":
		return "Codex"
	case "gm":
		return "Gemini"
	default:
		return service
	}
}
