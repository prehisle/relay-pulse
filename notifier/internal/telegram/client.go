package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Client Telegram API 客户端
type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

// NewClient 创建 Telegram 客户端
func NewClient(token string) *Client {
	return &Client{
		token:   token,
		baseURL: "https://api.telegram.org/bot" + token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// User Telegram 用户
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Chat Telegram 聊天
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// Message Telegram 消息
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      *Chat  `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text,omitempty"`
}

// Update Telegram 更新
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// APIResponse Telegram API 响应
type APIResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	Description string          `json:"description,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
}

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// GetMe 获取 Bot 信息
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	resp, err := c.doRequest(ctx, "getMe", nil)
	if err != nil {
		return nil, err
	}

	var user User
	if err := json.Unmarshal(resp.Result, &user); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &user, nil
}

// GetUpdates 获取更新（Long Polling）
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	params := map[string]interface{}{
		"offset":  offset,
		"timeout": timeout,
	}

	// 使用更长的超时时间
	client := &http.Client{
		Timeout: time.Duration(timeout+10) * time.Second,
	}

	resp, err := c.doRequestWithClient(ctx, client, "getUpdates", params)
	if err != nil {
		return nil, err
	}

	var updates []Update
	if err := json.Unmarshal(resp.Result, &updates); err != nil {
		return nil, fmt.Errorf("解析更新失败: %w", err)
	}

	return updates, nil
}

// SendMessage 发送消息
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, parseMode string) (*Message, error) {
	params := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if parseMode != "" {
		params["parse_mode"] = parseMode
	}

	resp, err := c.doRequest(ctx, "sendMessage", params)
	if err != nil {
		return nil, err
	}

	var msg Message
	if err := json.Unmarshal(resp.Result, &msg); err != nil {
		return nil, fmt.Errorf("解析消息失败: %w", err)
	}

	return &msg, nil
}

// SendText 发送纯文本消息（不解析格式，原样显示）
func (c *Client) SendText(ctx context.Context, chatID int64, text string) error {
	_, err := c.SendMessage(ctx, chatID, text, "")
	return err
}

// SendMessageHTML 发送 HTML 格式消息
func (c *Client) SendMessageHTML(ctx context.Context, chatID int64, text string) (*Message, error) {
	return c.SendMessage(ctx, chatID, text, "HTML")
}

// SendPhoto 发送图片消息（上传图片数据）
func (c *Client) SendPhoto(ctx context.Context, chatID int64, photoData []byte, caption string) (*Message, error) {
	url := c.baseURL + "/sendPhoto"

	// 使用 multipart/form-data 上传图片
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// 添加 chat_id 字段
	if err := w.WriteField("chat_id", fmt.Sprintf("%d", chatID)); err != nil {
		return nil, fmt.Errorf("写入 chat_id 失败: %w", err)
	}

	// 添加 caption 字段（可选）
	if caption != "" {
		if err := w.WriteField("caption", caption); err != nil {
			return nil, fmt.Errorf("写入 caption 失败: %w", err)
		}
	}

	// 添加图片文件
	fw, err := w.CreateFormFile("photo", "screenshot.png")
	if err != nil {
		return nil, fmt.Errorf("创建 form file 失败: %w", err)
	}
	if _, err := fw.Write(photoData); err != nil {
		return nil, fmt.Errorf("写入图片数据失败: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("关闭 multipart writer 失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if !apiResp.OK {
		return nil, fmt.Errorf("API 错误 [%d]: %s", apiResp.ErrorCode, apiResp.Description)
	}

	var msg Message
	if err := json.Unmarshal(apiResp.Result, &msg); err != nil {
		return nil, fmt.Errorf("解析消息失败: %w", err)
	}

	return &msg, nil
}

// doRequest 执行 API 请求
func (c *Client) doRequest(ctx context.Context, method string, params map[string]interface{}) (*APIResponse, error) {
	return c.doRequestWithClient(ctx, c.httpClient, method, params)
}

// doRequestWithClient 使用指定客户端执行 API 请求
func (c *Client) doRequestWithClient(ctx context.Context, client *http.Client, method string, params map[string]interface{}) (*APIResponse, error) {
	url := c.baseURL + "/" + method

	var body io.Reader
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("序列化参数失败: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if !apiResp.OK {
		return &apiResp, fmt.Errorf("API 错误 [%d]: %s", apiResp.ErrorCode, apiResp.Description)
	}

	return &apiResp, nil
}

// IsForbiddenError 检查是否是用户封禁 Bot 的错误
func IsForbiddenError(err error) bool {
	if err == nil {
		return false
	}
	// Telegram 返回 403 表示用户封禁了 Bot
	return fmt.Sprintf("%v", err) == "API 错误 [403]: Forbidden: bot was blocked by the user"
}
