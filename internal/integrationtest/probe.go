// Package integrationtest implements non-persisting integration probes.
// Application adapters retain their own permissions, address policies and bindings.
package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var tokenPattern = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{20,100}$`)

func Secret(draft, saved string) string {
	if draft == "" {
		return saved
	}
	return draft
}
func Read(body io.Reader) (map[string]json.RawMessage, error) {
	v := map[string]json.RawMessage{}
	d := json.NewDecoder(io.LimitReader(body, 65537))
	if e := d.Decode(&v); e != nil {
		if errors.Is(e, io.EOF) {
			return v, nil
		}
		return nil, errors.New("测试配置必须是 JSON 对象")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return nil, errors.New("测试配置格式无效")
	}
	if v == nil {
		return map[string]json.RawMessage{}, nil
	}
	return v, nil
}
func String(v map[string]json.RawMessage, key, fallback string) (string, error) {
	raw, ok := v[key]
	if !ok {
		return fallback, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", fmt.Errorf("%s 必须为文本", key)
	}
	return value, nil
}

type Telegram struct {
	HTTP    *http.Client
	BaseURL string
}
type Bot struct {
	Username string `json:"username"`
	ID       int64  `json:"id"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Update struct {
	Message *struct {
		Chat Chat `json:"chat"`
		From struct {
			Bot bool `json:"is_bot"`
		} `json:"from"`
	} `json:"message"`
}

func (t Telegram) Call(ctx context.Context, token, method string, payload any, out any) error {
	if !tokenPattern.MatchString(token) {
		return errors.New("Bot Token 格式不正确")
	}
	raw, e := json.Marshal(payload)
	if e != nil {
		return errors.New("Telegram 测试参数无效")
	}
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/bot"+token+"/"+method, bytes.NewReader(raw))
	if e != nil {
		return errors.New("Telegram 测试请求无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if t.HTTP == nil {
		return errors.New("Telegram 测试客户端未配置")
	}
	client := *t.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return errors.New("Telegram 测试超时或已取消，请检查服务器网络和代理")
		}
		return errors.New("Telegram 连接失败，请检查服务器网络和代理")
	}
	defer res.Body.Close()
	var result struct {
		OK          bool            `json:"ok"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result) != nil {
		return fmt.Errorf("Telegram 返回无效响应（HTTP %d）", res.StatusCode)
	}
	if !result.OK || res.StatusCode < 200 || res.StatusCode >= 300 {
		code := result.Code
		if code == 0 {
			code = res.StatusCode
		}
		switch code {
		case 401, 404:
			return errors.New("Telegram Token 无效或已撤销")
		case 403:
			return errors.New("机器人无法向此会话发送消息，请先发送 /start 并解除屏蔽")
		case 409:
			return errors.New("机器人已设置 Webhook 或被其他实例轮询，请使用独立机器人")
		case 429:
			return fmt.Errorf("Telegram 限流，请在 %d 秒后重试", max(result.Parameters.Retry, 1))
		case 400:
			return errors.New("Telegram 会话或消息参数无效，请核对 Chat ID 并先发送 /start")
		}
		return fmt.Errorf("Telegram API 请求失败（HTTP %d）", code)
	}
	if out != nil && json.Unmarshal(result.Result, out) != nil {
		return errors.New("Telegram 返回格式无效")
	}
	return nil
}
func (t Telegram) Check(ctx context.Context, token string) (Bot, error) {
	var b Bot
	e := t.Call(ctx, token, "getMe", map[string]any{}, &b)
	return b, e
}
func (t Telegram) Discover(ctx context.Context, token string) ([]string, error) {
	var updates []Update
	if e := t.Call(ctx, token, "getUpdates", map[string]any{"timeout": 0, "limit": 100, "allowed_updates": []string{"message", "callback_query"}}, &updates); e != nil {
		return nil, e
	}
	chats := []string{}
	seen := map[int64]bool{}
	for _, u := range updates {
		if u.Message != nil && u.Message.Chat.Type == "private" && u.Message.Chat.ID > 0 && !u.Message.From.Bot && !seen[u.Message.Chat.ID] {
			id := u.Message.Chat.ID
			seen[id] = true
			chats = append(chats, strconv.FormatInt(id, 10))
		}
	}
	return chats, nil
}
func (t Telegram) Send(ctx context.Context, token, chat, text string) error {
	id, e := strconv.ParseInt(chat, 10, 64)
	if e != nil || id == 0 {
		return errors.New("请填写有效的 Chat ID")
	}
	return t.Call(ctx, token, "sendMessage", map[string]any{"chat_id": chat, "text": text, "disable_web_page_preview": true}, nil)
}
func Webhook(ctx context.Context, c *http.Client, method, target string, headers map[string]string, payload []byte) (int, error) {
	var body io.Reader
	if method != "GET" {
		body = bytes.NewReader(payload)
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return 0, errors.New("Webhook 测试地址或请求方法无效")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, e := c.Do(req)
	if e != nil {
		return 0, errors.New("Webhook 连接失败或超时，请检查地址、证书、网络和代理")
	}
	defer res.Body.Close()
	_, e = io.Copy(io.Discard, io.LimitReader(res.Body, 8192))
	if e != nil {
		return res.StatusCode, errors.New("Webhook 响应读取失败")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return res.StatusCode, fmt.Errorf("Webhook 返回 HTTP %d", res.StatusCode)
	}
	return res.StatusCode, nil
}
