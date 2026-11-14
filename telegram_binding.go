package main

import (
	"context"
	"encoding/json"
	"errors"
	probe "github.com/Stylle/pocket-relay/internal/integrationtest"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *app) relayBindingRoute(w http.ResponseWriter, r *http.Request) {
	owner, ok := a.admin(w, r)
	if !ok {
		return
	}
	cfg, rev, e := a.telegramConfig()
	if e != nil || !cfg.Enabled || cfg.Token == "" {
		fail(w, 400, "请先保存 Token 并启用机器人")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/unbind") {
		a.tgConfigMu.Lock()
		defer a.tgConfigMu.Unlock()
		cfg.ChatID = ""
		if e = a.storeRelayBinding(cfg, rev); e != nil {
			fail(w, 409, "配置已变化，请刷新")
			return
		}
		a.botBinding.Clear()
		a.wakeTelegram()
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	name, e := a.checkTelegram(ctx, cfg.Token, true)
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	a.tgConfigMu.Lock()
	defer a.tgConfigMu.Unlock()
	_, current, e := a.telegramConfig()
	if e != nil || current != rev {
		fail(w, 409, "配置已变化，请刷新")
		return
	}
	command, e := a.botBinding.Create(owner.ID, rev)
	if e != nil {
		fail(w, 500, "生成绑定码失败")
		return
	}
	a.wakeTelegram()
	reply(w, 200, map[string]any{"command": command, "expiresIn": 300, "bot": name})
}
func (a *app) storeRelayBinding(cfg telegramConfig, rev string) error {
	raw, e := json.Marshal(cfg)
	if e != nil {
		return e
	}
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE telegram SET value=?,revision=?,receive_error='',send_error='' WHERE id=1 AND revision=?", seal(a.key, string(raw)), randomID(), rev)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("配置已变化")
	}
	if _, e = tx.Exec("DELETE FROM telegram_outbox"); e != nil {
		return e
	}
	return tx.Commit()
}
func relayPrivateMessage(m *telegramMessage) bool {
	return m != nil && m.Chat.Type == "private" && m.Chat.ID > 0 && !m.From.Bot && (m.From.ID == 0 || m.From.ID == m.Chat.ID) && m.Date >= now()-120 && m.Date <= now()+30
}
func (a *app) acceptRelayBinding(ctx context.Context, cfg telegramConfig, rev string, m *telegramMessage) bool {
	if !relayPrivateMessage(m) || m.From.ID != m.Chat.ID {
		return false
	}
	parts := strings.Fields(m.Text)
	if len(parts) != 2 || strings.ToLower(strings.Split(parts[0], "@")[0]) != "/bind" {
		return false
	}
	a.tgConfigMu.Lock()
	e := a.botBinding.Consume(parts[1], rev, func(owner string) error {
		user, e := a.findUser(owner)
		if e != nil || !user.Admin {
			return errors.New("账户已失效")
		}
		current, currentRev, e := a.telegramConfig()
		if e != nil || currentRev != rev || !current.Enabled || current.Token != cfg.Token {
			return errors.New("配置已变化")
		}
		current.ChatID = strconv.FormatInt(m.Chat.ID, 10)
		current.UserID = owner
		current.Since = now()
		return a.storeRelayBinding(current, rev)
	})
	a.tgConfigMu.Unlock()
	if e != nil {
		return false
	}
	_ = a.telegramCall(ctx, cfg.Token, "sendMessage", map[string]any{"chat_id": m.Chat.ID, "text": "绑定成功。将文字、图片或文件发送到此私聊，即可按保存的同步规则在网站中查看。使用菜单中的 /help 查看说明。"}, nil)
	a.registerRelayMenu(ctx, cfg, m.Chat.ID)
	a.wakeTelegram()
	return true
}
func (a *app) relayBotCommand(ctx context.Context, cfg telegramConfig, m *telegramMessage) bool {
	if !relayPrivateMessage(m) {
		return false
	}
	words := strings.Fields(m.Text)
	if len(words) == 0 {
		return false
	}
	cmd := strings.ToLower(strings.Split(words[0], "@")[0])
	if cmd == "/bind" {
		return true
	}
	if cmd != "/start" && cmd != "/help" {
		return false
	}
	if cfg.ChatID == "" || strconv.FormatInt(m.Chat.ID, 10) != cfg.ChatID {
		return true
	}
	_ = a.telegramCall(ctx, cfg.Token, "sendMessage", map[string]any{"chat_id": m.Chat.ID, "text": "随手传：在此私聊发送文字、图片和文件，按网站中保存的规则同步。更换绑定请在网站生成新的绑定码，再发送 /bind 绑定码。"}, nil)
	return true
}
func (a *app) registerRelayMenu(ctx context.Context, cfg telegramConfig, chat int64) {
	_ = (probe.Telegram{HTTP: a.tgHTTP, BaseURL: a.tgBase}).Menu(ctx, cfg.Token, chat, []probe.Command{{Command: "start", Description: "使用说明"}, {Command: "help", Description: "消息同步帮助"}, {Command: "bind", Description: "绑定网站账户（后接绑定码）"}})
}
