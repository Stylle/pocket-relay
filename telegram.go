package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type telegramConfig struct {
	Enabled          bool   `json:"enabled"`
	Receive          bool   `json:"receive"`
	Send             bool   `json:"send"`
	IncludeConcealed bool   `json:"include_concealed"`
	ReceiveConcealed bool   `json:"receive_concealed"`
	UserID           string `json:"user_id"`
	ChatID           string `json:"chat_id"`
	Token            string `json:"token"`
	Since            int64  `json:"since"`
}

func (a *app) telegramConfig() (telegramConfig, string, error) {
	cfg := telegramConfig{ReceiveConcealed: true}
	var encoded, revision string
	err := a.db.QueryRow(`SELECT value,revision FROM telegram WHERE id=1`).Scan(&encoded, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, "", nil
	}
	if err != nil {
		return cfg, "", err
	}
	raw, err := unseal(a.key, encoded)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &cfg)
	}
	return cfg, revision, err
}

func (a *app) telegramGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	cfg, rev, err := a.telegramConfig()
	if err != nil {
		fail(w, 500, "机器人配置无法解密，请恢复完整 data 备份")
		return
	}
	configured := cfg.Token != ""
	cfg.Token = ""
	var pending, failed int
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox WHERE failed=0`).Scan(&pending)
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox WHERE failed=1`).Scan(&failed)
	var status string
	var receiveError, sendError string
	var received, sent int64
	a.db.QueryRow(`SELECT last_error,receive_error,send_error,received,sent FROM telegram WHERE id=1`).Scan(&status, &receiveError, &sendError, &received, &sent)
	if receiveError != "" {
		status += " 接收：" + receiveError
	}
	if sendError != "" {
		status += " 发送：" + sendError
	}
	reply(w, 200, map[string]any{"config": cfg, "revision": rev, "configured": configured, "pending": pending, "failed": failed, "last_error": status, "received": received, "sent": sent})
}

type telegramInput struct {
	Enabled          bool   `json:"enabled"`
	Receive          bool   `json:"receive"`
	Send             bool   `json:"send"`
	IncludeConcealed bool   `json:"include_concealed"`
	ReceiveConcealed *bool  `json:"receive_concealed"`
	UserID           string `json:"user_id"`
	ChatID           string `json:"chat_id"`
	Token            string `json:"token"`
	Revision         string `json:"revision"`
}

var telegramTokenPattern = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{20,100}$`)

func (a *app) telegramSave(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	var in telegramInput
	if !decode(w, r, &in) {
		return
	}
	previous, revision, err := a.telegramConfig()
	if err != nil {
		fail(w, 500, "机器人配置无法读取")
		return
	}
	if in.Revision != revision {
		fail(w, 409, "设置已改变，请重新打开后台后保存")
		return
	}
	if in.Token == "" {
		in.Token = previous.Token
	}
	if in.Token != "" && !telegramTokenPattern.MatchString(in.Token) {
		fail(w, 400, "Bot Token 格式不正确")
		return
	}
	if in.Enabled {
		id, e := strconv.ParseInt(in.ChatID, 10, 64)
		if e != nil || id <= 0 || strconv.FormatInt(id, 10) != in.ChatID {
			fail(w, 400, "请填写正整数私聊 Chat ID，不支持群聊")
			return
		}
		if _, e = a.findUser(in.UserID); e != nil {
			fail(w, 400, "归属账户不存在或已过期")
			return
		}
		if !in.Receive && !in.Send || in.Token == "" {
			fail(w, 400, "请填写 Token，并至少开启一个同步方向")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if _, e = a.checkTelegram(ctx, in.Token, in.Receive); e != nil {
			fail(w, 400, e.Error())
			return
		}
	}
	cfg := telegramConfig{Enabled: in.Enabled, Receive: in.Receive, Send: in.Send, IncludeConcealed: in.IncludeConcealed, UserID: in.UserID, ChatID: in.ChatID, Token: in.Token, Since: previous.Since}
	cfg.ReceiveConcealed = previous.ReceiveConcealed
	if in.ReceiveConcealed != nil {
		cfg.ReceiveConcealed = *in.ReceiveConcealed
	}
	if in.Enabled && (!previous.Enabled || !previous.Receive && in.Receive || previous.Token != in.Token || previous.ChatID != in.ChatID || previous.UserID != in.UserID) {
		cfg.Since = now()
	}
	raw, _ := json.Marshal(cfg)
	a.tgConfigMu.Lock()
	defer a.tgConfigMu.Unlock()
	// Cancel in-flight network requests before waiting for a storage operation.
	a.wakeTelegram()
	// Share the data gate with uploads; toggling the bot must not wait for a
	// potentially large web upload. Revision checks fence both import/outbox commits.
	a.gate.RLock()
	defer a.gate.RUnlock()
	_, current, err := a.telegramConfig()
	if err != nil || current != revision {
		fail(w, 409, "设置已改变，请重新打开后台后保存")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback()
	next := int64(0)
	if previous.Token == in.Token {
		tx.QueryRow(`SELECT next_update FROM telegram WHERE id=1`).Scan(&next)
	}
	_, err = tx.Exec(`INSERT INTO telegram(id,value,revision,next_update) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value,revision=excluded.revision,next_update=excluded.next_update,last_error='',receive_error='',send_error=''`, seal(a.key, string(raw)), randomID(), next)
	if err == nil {
		_, err = tx.Exec(`DELETE FROM telegram_outbox`)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	a.wakeTelegram()
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *app) telegramTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	cfg, _, err := a.telegramConfig()
	if err != nil {
		fail(w, 500, "配置无法读取")
		return
	}
	if in.Token == "" {
		in.Token = cfg.Token
	}
	if !telegramTokenPattern.MatchString(in.Token) {
		fail(w, 400, "请填写有效的 Bot Token")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	name, err := a.checkTelegram(ctx, in.Token, true)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	chats := []string{}
	if cfg.Enabled && cfg.Token == in.Token {
		if cfg.ChatID != "" {
			chats = append(chats, cfg.ChatID)
		}
	} else {
		var updates []telegramUpdate
		if err = a.telegramCall(ctx, in.Token, "getUpdates", map[string]any{"timeout": 0, "limit": 100, "allowed_updates": []string{"message"}}, &updates); err != nil {
			fail(w, 502, err.Error())
			return
		}
		seen := map[int64]bool{}
		for _, update := range updates {
			m := update.Message
			if m != nil && m.Chat.Type == "private" && m.Chat.ID > 0 && !m.From.Bot && !seen[m.Chat.ID] {
				chats = append(chats, strconv.FormatInt(m.Chat.ID, 10))
				seen[m.Chat.ID] = true
			}
		}
	}
	reply(w, 200, map[string]any{"username": name, "chats": chats})
}

func (a *app) telegramRetry(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	cfg, rev, err := a.telegramConfig()
	if err != nil || !cfg.Enabled || !cfg.Send {
		fail(w, 400, "请先开启机器人发送")
		return
	}
	if _, err = a.db.Exec(`UPDATE telegram_outbox SET failed=0,attempts=0,available=0 WHERE revision=? AND failed=1`, rev); err != nil {
		fail(w, 500, "重试失败")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

// API/network errors must never include a request URL: it contains the token.
type telegramError struct{ code, retry int }

func (e telegramError) Error() string {
	switch e.code {
	case 401, 404:
		return "Telegram Token 无效或已撤销"
	case 409:
		return "机器人被其他实例占用或已设置 Webhook，请使用专用机器人"
	case 429:
		return "Telegram 限流，稍后自动重试"
	case 403:
		return "机器人无权发送，请先在 TG 私聊机器人并取消屏蔽"
	case 400:
		return "Telegram 拒绝请求，请检查 Chat ID、文件与机器人配置"
	}
	return "Telegram 网络或接口不可用，请检查网络连接（未记录 Token）"
}
func telegramResponse(res *http.Response, out any) error {
	defer res.Body.Close()
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&envelope) != nil {
		return telegramError{code: res.StatusCode}
	}
	if !envelope.OK || res.StatusCode != 200 {
		return telegramError{code: max(envelope.Code, res.StatusCode), retry: min(max(envelope.Parameters.Retry, 1), 3600)}
	}
	if out != nil && json.Unmarshal(envelope.Result, out) != nil {
		return telegramError{}
	}
	return nil
}
func (a *app) telegramCall(ctx context.Context, token, method string, input, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return telegramError{}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.tgBase+"/bot"+token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return telegramError{}
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.tgHTTP.Do(req)
	if err != nil {
		return telegramError{}
	}
	return telegramResponse(res, out)
}
func (a *app) checkTelegram(ctx context.Context, token string, receive bool) (string, error) {
	var bot struct {
		Username string `json:"username"`
	}
	if err := a.telegramCall(ctx, token, "getMe", map[string]any{}, &bot); err != nil {
		return "", err
	}
	if receive {
		var hook struct {
			URL string `json:"url"`
		}
		if err := a.telegramCall(ctx, token, "getWebhookInfo", map[string]any{}, &hook); err != nil {
			return "", err
		}
		if hook.URL != "" {
			return "", telegramError{code: 409}
		}
	}
	return bot.Username, nil
}

func (a *app) wakeTelegram() {
	a.tgMu.Lock()
	if a.tgCancel != nil {
		a.tgCancel()
	}
	a.tgMu.Unlock()
	select {
	case a.tgWake <- struct{}{}:
	default:
	}
}
func telegramWait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func telegramReceiveDelay(err error) time.Duration {
	var apiErr telegramError
	if errors.As(err, &apiErr) && apiErr.code == 429 {
		return time.Duration(max(apiErr.retry, 1)) * time.Second
	}
	return 10 * time.Second
}
func (a *app) telegramStatus(rev, direction string, err error) {
	status := ""
	if err != nil {
		status = err.Error()
	}
	column := "receive_error"
	if direction == "send" {
		column = "send_error"
	}
	a.db.Exec(`UPDATE telegram SET `+column+`=? WHERE id=1 AND revision=?`, status, rev)
}
func (a *app) runTelegram(ctx context.Context) {
	for ctx.Err() == nil {
		a.tgConfigMu.Lock()
		cfg, rev, err := a.telegramConfig()
		a.tgConfigMu.Unlock()
		if err != nil || !cfg.Enabled {
			select {
			case <-ctx.Done():
				return
			case <-a.tgWake:
			}
			continue
		}
		child, cancel := context.WithCancel(ctx)
		a.tgMu.Lock()
		a.tgCancel = cancel
		a.tgMu.Unlock()
		var workers sync.WaitGroup
		if cfg.Receive {
			workers.Add(1)
			go func() { defer workers.Done(); a.receiveTelegram(child, cfg, rev) }()
		}
		if cfg.Send {
			workers.Add(1)
			go func() { defer workers.Done(); a.sendTelegram(child, cfg, rev) }()
		}
		select {
		case <-ctx.Done():
		case <-a.tgWake:
		case <-child.Done():
		}
		cancel()
		workers.Wait()
		a.tgMu.Lock()
		a.tgCancel = nil
		a.tgMu.Unlock()
	}
}

type telegramFile struct {
	ID   string `json:"file_id"`
	Size int64  `json:"file_size"`
	Name string `json:"file_name"`
	MIME string `json:"mime_type"`
}
type telegramMessage struct {
	ID      int64  `json:"message_id"`
	Date    int64  `json:"date"`
	Text    string `json:"text"`
	Caption string `json:"caption"`
	Chat    struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From struct {
		Bot bool `json:"is_bot"`
	} `json:"from"`
	Photos    []telegramFile `json:"photo"`
	Document  *telegramFile  `json:"document"`
	Video     *telegramFile  `json:"video"`
	Audio     *telegramFile  `json:"audio"`
	Voice     *telegramFile  `json:"voice"`
	Animation *telegramFile  `json:"animation"`
	Sticker   *telegramFile  `json:"sticker"`
	VideoNote *telegramFile  `json:"video_note"`
}
type telegramUpdate struct {
	ID      int64            `json:"update_id"`
	Message *telegramMessage `json:"message"`
}

func (a *app) receiveTelegram(ctx context.Context, cfg telegramConfig, rev string) {
	for ctx.Err() == nil {
		if _, err := a.findUser(cfg.UserID); err != nil {
			a.telegramStatus(rev, "receive", errors.New("归属账户已过期或不存在，已停止同步"))
			if !telegramWait(ctx, 30*time.Second) {
				return
			}
			continue
		}
		var next int64
		if a.db.QueryRow(`SELECT next_update FROM telegram WHERE revision=?`, rev).Scan(&next) != nil {
			return
		}
		var updates []telegramUpdate
		err := a.telegramCall(ctx, cfg.Token, "getUpdates", map[string]any{"offset": next, "timeout": 25, "limit": 50, "allowed_updates": []string{"message"}}, &updates)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.telegramStatus(rev, "receive", err)
			if !telegramWait(ctx, telegramReceiveDelay(err)) {
				return
			}
			continue
		}
		for _, update := range updates {
			if update.ID < next {
				continue
			}
			m := update.Message
			if m != nil && m.Chat.Type == "private" && strconv.FormatInt(m.Chat.ID, 10) == cfg.ChatID && !m.From.Bot && m.Date >= cfg.Since {
				if err = a.importTelegram(ctx, cfg, rev, update); err != nil {
					break
				}
			}
			if ctx.Err() != nil {
				return
			}
			if _, err = a.db.Exec(`UPDATE telegram SET next_update=? WHERE revision=?`, update.ID+1, rev); err != nil {
				break
			}
			next = update.ID + 1
		}
		if err != nil {
			a.telegramStatus(rev, "receive", err)
			if !telegramWait(ctx, telegramReceiveDelay(err)) {
				return
			}
		} else {
			a.telegramStatus(rev, "receive", nil)
		}
	}
}

func telegramAttachment(m *telegramMessage) *telegramFile {
	for _, f := range []*telegramFile{m.Document, m.Video, m.Audio, m.Voice, m.Animation, m.Sticker, m.VideoNote} {
		if f != nil {
			return f
		}
	}
	if len(m.Photos) > 0 {
		return &m.Photos[len(m.Photos)-1]
	}
	return nil
}

var telegramPathPattern = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
var errTelegramAttachment = errors.New("Telegram 附件超过限制或路径不受支持")

func (a *app) telegramDownload(ctx context.Context, token string, object *telegramFile, limit int64) (string, error) {
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := a.telegramCall(ctx, token, "getFile", map[string]string{"file_id": object.ID}, &file); err != nil {
		return "", err
	}
	if !telegramPathPattern.MatchString(file.Path) || strings.HasPrefix(file.Path, "/") || path.Clean(file.Path) != file.Path || strings.Contains(file.Path, "..") || file.Size > limit {
		return "", errTelegramAttachment
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.tgBase+"/file/bot"+token+"/"+file.Path, nil)
	if err != nil {
		return "", telegramError{}
	}
	res, err := a.tgHTTP.Do(req)
	if err != nil {
		return "", telegramError{}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", telegramError{code: res.StatusCode}
	}
	f, err := os.CreateTemp(filepath.Join(a.dir, "tmp"), "tg-receive-")
	if err != nil {
		return "", errors.New("Telegram 接收临时磁盘不可写")
	}
	n, copyErr := io.Copy(f, io.LimitReader(res.Body, limit+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n > limit {
		os.Remove(f.Name())
		if n > limit {
			return "", errTelegramAttachment
		}
		return "", errors.New("Telegram 附件下载失败或超过限制")
	}
	return f.Name(), nil
}

func (a *app) importTelegram(ctx context.Context, cfg telegramConfig, rev string, update telegramUpdate) error {
	if update.Message == nil || update.Message.Chat.Type != "private" || strconv.FormatInt(update.Message.Chat.ID, 10) != cfg.ChatID || update.Message.From.Bot || update.Message.Date < cfg.Since {
		return nil
	}
	select {
	case a.transfers <- struct{}{}:
		defer func() { <-a.transfers }()
	case <-ctx.Done():
		return ctx.Err()
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	current, currentRev, err := a.telegramConfig()
	if err != nil || currentRev != rev || !current.Enabled || !current.Receive || ctx.Err() != nil {
		return errors.New("机器人设置已改变")
	}
	bot := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.Token)))
	var seen int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM telegram_inbox WHERE bot=? AND update_id=?`, bot, update.ID).Scan(&seen); err != nil {
		return errors.New("接收记录无法读取")
	}
	if seen > 0 {
		return nil
	}
	if _, err = a.findUser(cfg.UserID); err != nil {
		return errors.New("归属账户已过期")
	}
	m := update.Message
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	settings := a.config()
	var fileID, filename, kind, digest, temp string
	var size int64
	object := telegramAttachment(m)
	warning := ""
	if object != nil {
		limit := min(settings.MaxMB<<20, int64(20<<20))
		if object.Size > limit {
			warning = "Telegram 附件未导入：超过 20 MB 或站点单文件上限，请在 TG 查看或改用网页上传。"
		} else {
			temp, err = a.telegramDownload(ctx, cfg.Token, object, limit)
			if err != nil {
				var apiErr telegramError
				if errors.Is(err, errTelegramAttachment) || errors.As(err, &apiErr) && (apiErr.code == 400 || apiErr.code == 403 || apiErr.code == 404) {
					warning = "Telegram 附件未导入：附件超限或不可读取，请在 TG 查看或改用网页上传。"
				} else {
					return err
				}
			}
			if temp != "" {
				defer os.Remove(temp)
				f, e := os.Open(temp)
				if e != nil {
					return errors.New("接收文件无法读取")
				}
				h := sha256.New()
				size, e = io.Copy(h, f)
				f.Seek(0, 0)
				head := make([]byte, 512)
				n, _ := f.Read(head)
				f.Close()
				if e != nil {
					return errors.New("接收文件校验失败")
				}
				kind = http.DetectContentType(head[:n])
				digest = fmt.Sprintf("%x", h.Sum(nil))
				fileID = randomID()
				filename = filepath.Base(strings.ReplaceAll(object.Name, "\\", "/"))
				if filename == "." || filename == "" {
					filename = "telegram-" + strconv.FormatInt(m.ID, 10)
					if strings.HasPrefix(kind, "image/") {
						filename += ".jpg"
					}
				}
				if len(filename) > 255 || strings.ContainsAny(filename, "\x00\r\n") {
					filename = "telegram-attachment"
				}
			}
		}
	}
	if warning != "" {
		if text != "" {
			text += "\n\n"
		}
		text += warning
	}
	if text == "" && fileID == "" {
		text = "[Telegram 消息类型暂不支持，请在 TG 查看]"
	}
	if len(text) > 16384 {
		text = string([]rune(text)[:min(4000, len([]rune(text)))])
	}
	stored := false
	committed := false
	defer func() {
		if stored && !committed {
			a.db.Exec(`UPDATE files SET deleted=1 WHERE id=?`, fileID)
		}
	}()
	if fileID != "" {
		st, e := a.getStore(settings.ActiveStore)
		if e != nil {
			return errors.New("接收存储配置不可用")
		}
		if _, err = a.db.Exec(`INSERT INTO files(id,user_id,store_id,name,mime,size,sha256,deleted) VALUES(?,?,?,?,?,?,?,-1)`, fileID, cfg.UserID, settings.ActiveStore, filename, kind, size, digest); err != nil {
			return errors.New("接收文件登记失败")
		}
		stored = true
		if err = a.put(ctx, st, fileID, temp); err != nil {
			return errors.New("接收存储写入失败，将自动重试")
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err = a.findUser(cfg.UserID); err != nil {
		return errors.New("归属账户已过期")
	}
	tx, err := a.db.Begin()
	if err != nil {
		return errors.New("接收数据库不可用")
	}
	defer tx.Rollback()
	var commitRevision string
	if err = tx.QueryRow(`SELECT revision FROM telegram WHERE id=1`).Scan(&commitRevision); err != nil || commitRevision != rev {
		return errors.New("机器人设置已改变，接收已取消")
	}
	var used int64
	if err = tx.QueryRow(`SELECT COALESCE(SUM(size),0) FROM files WHERE user_id=? AND deleted=0`, cfg.UserID).Scan(&used); err != nil {
		return errors.New("接收配额检查失败")
	}
	if used+size > settings.QuotaMB<<20 {
		return errors.New("归属账户存储空间不足，请清理后等待重试")
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,user_id,device,text,created,expires,file_id,concealed) VALUES(?,?,'Telegram',?,?,0,?,?)`, randomID(), cfg.UserID, text, time.Now().UnixMilli(), fileID, cfg.ReceiveConcealed); err != nil {
		return errors.New("接收消息保存失败")
	}
	if fileID != "" {
		if _, err = tx.Exec(`UPDATE files SET deleted=0 WHERE id=?`, fileID); err != nil {
			return errors.New("接收文件保存失败")
		}
	}
	if _, err = tx.Exec(`INSERT INTO telegram_inbox(bot,update_id) VALUES(?,?)`, bot, update.ID); err != nil {
		return errors.New("接收去重保存失败")
	}
	if _, err = tx.Exec(`UPDATE telegram SET received=?,receive_error=? WHERE revision=?`, now(), warning, rev); err != nil {
		return errors.New("接收状态保存失败")
	}
	if err = tx.Commit(); err != nil {
		return errors.New("接收提交失败")
	}
	committed = true
	return nil
}

// Keep completed chunks in the durable outbox. Telegram offers no idempotency
// key, so a lost HTTP response after acceptance can still cause a retry duplicate.
func telegramChunks(text string) []string {
	parts := []string{}
	runes := []rune(text)
	for len(runes) > 0 {
		units, n := 0, 0
		for n < len(runes) {
			width := 1
			if runes[n] > 0xffff {
				width = 2
			}
			if units+width > 3500 {
				break
			}
			units += width
			n++
		}
		parts = append(parts, string(runes[:n]))
		runes = runes[n:]
	}
	return parts
}
func (a *app) sendTelegram(ctx context.Context, cfg telegramConfig, rev string) {
	for ctx.Err() == nil {
		worked, err := a.deliverTelegram(ctx, cfg, rev)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.telegramStatus(rev, "send", err)
		}
		if !worked || err != nil {
			if !telegramWait(ctx, 2*time.Second) {
				return
			}
		} else {
			if !telegramWait(ctx, time.Second) {
				return
			}
		}
	}
}
func (a *app) deliverTelegram(ctx context.Context, cfg telegramConfig, rev string) (bool, error) {
	var id string
	var step, attempts int
	err := a.db.QueryRow(`SELECT message_id,step,attempts FROM telegram_outbox WHERE revision=? AND failed=0 AND available<=? ORDER BY rowid LIMIT 1`, rev, now()).Scan(&id, &step, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("发送队列无法读取")
	}
	var m message
	err = a.db.QueryRow(`SELECT m.id,m.text,m.device,m.created,m.expires,m.file_id,m.concealed,COALESCE(f.name,''),COALESCE(f.size,0) FROM messages m LEFT JOIN files f ON f.id=m.file_id WHERE m.id=? AND m.user_id=?`, id, cfg.UserID).Scan(&m.ID, &m.Text, &m.Device, &m.Created, &m.Expires, &m.FileID, &m.Concealed, &m.Name, &m.Size)
	if err != nil || m.Expires > 0 && m.Expires <= now() || m.Concealed && !cfg.IncludeConcealed {
		a.db.Exec(`DELETE FROM telegram_outbox WHERE message_id=?`, id)
		return true, nil
	}
	if _, err = a.findUser(cfg.UserID); err != nil {
		a.db.Exec(`DELETE FROM telegram_outbox WHERE message_id=?`, id)
		return true, errors.New("归属账户已过期，发送已停止")
	}
	parts := telegramChunks(m.Text)
	if m.FileID != "" && m.Size > 50<<20 {
		parts = append(parts, "附件 "+m.Name+" 超过 Telegram 50 MB 上限，请在网站下载。")
		m.FileID = ""
	}
	var sendErr error
	if step < len(parts) {
		sendErr = a.telegramCall(ctx, cfg.Token, "sendMessage", map[string]any{"chat_id": cfg.ChatID, "text": parts[step], "link_preview_options": map[string]bool{"is_disabled": true}}, nil)
	} else if m.FileID != "" && step == len(parts) {
		sendErr = a.telegramUpload(ctx, cfg, m)
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if sendErr != nil {
		attempts++
		delay := min(5*(1<<min(attempts, 8)), 900)
		var apiErr telegramError
		if errors.As(sendErr, &apiErr) && apiErr.code == 429 {
			delay = apiErr.retry
		}
		a.db.Exec(`UPDATE telegram_outbox SET attempts=?,available=?,failed=? WHERE message_id=? AND revision=?`, attempts, now()+int64(delay), attempts >= 10, id, rev)
		return true, sendErr
	}
	step++
	if step >= len(parts) && m.FileID == "" || step > len(parts) {
		a.db.Exec(`DELETE FROM telegram_outbox WHERE message_id=? AND revision=?`, id, rev)
		a.db.Exec(`UPDATE telegram SET sent=?,send_error='' WHERE revision=?`, now(), rev)
	} else {
		a.db.Exec(`UPDATE telegram_outbox SET step=?,attempts=0,available=0 WHERE message_id=? AND revision=?`, step, id, rev)
	}
	return true, nil
}

func (a *app) telegramUpload(ctx context.Context, cfg telegramConfig, m message) error {
	select {
	case a.transfers <- struct{}{}:
		defer func() { <-a.transfers }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if !a.fileAllowed(cfg.UserID, m.FileID) {
		return errors.New("待发送附件已删除或过期")
	}
	var storeID string
	if a.db.QueryRow(`SELECT store_id FROM files WHERE id=? AND user_id=? AND deleted=0`, m.FileID, cfg.UserID).Scan(&storeID) != nil {
		return errors.New("待发送附件不存在")
	}
	filename := filepath.Join(a.dir, "blobs", m.FileID)
	if storeID != "local" {
		st, err := a.getStore(storeID)
		if err != nil {
			return errors.New("待发送存储配置不可用")
		}
		f, err := os.CreateTemp(filepath.Join(a.dir, "tmp"), "tg-cache-")
		if err != nil {
			return errors.New("发送缓存不可写")
		}
		filename = f.Name()
		defer os.Remove(filename)
		err = a.fetch(ctx, st, m.FileID, f, m.Size)
		f.Close()
		if err != nil {
			return errors.New("远程附件读取失败，将自动重试")
		}
	}
	src, err := os.Open(filename)
	if err != nil {
		return errors.New("待发送附件无法读取")
	}
	defer src.Close()
	f, err := os.CreateTemp(filepath.Join(a.dir, "tmp"), "tg-send-")
	if err != nil {
		return errors.New("发送临时磁盘不可写")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	writer := multipart.NewWriter(f)
	if err = writer.WriteField("chat_id", cfg.ChatID); err != nil {
		return errors.New("构建 Telegram 附件失败")
	}
	part, err := writer.CreateFormFile("document", m.Name)
	if err != nil {
		return errors.New("构建 Telegram 附件失败")
	}
	n, err := io.Copy(part, io.LimitReader(src, m.Size+1))
	if err != nil || n != m.Size {
		return errors.New("待发送附件大小不一致")
	}
	if writer.Close() != nil {
		return errors.New("构建 Telegram 附件失败")
	}
	length, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return errors.New("发送文件无法读取")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return errors.New("发送文件无法读取")
	}
	if !a.fileAllowed(cfg.UserID, m.FileID) {
		return errors.New("待发送附件已删除或过期")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.tgBase+"/bot"+cfg.Token+"/sendDocument", f)
	if err != nil {
		return telegramError{}
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = length
	res, err := a.tgHTTP.Do(req)
	if err != nil {
		return telegramError{}
	}
	return telegramResponse(res, nil)
}
