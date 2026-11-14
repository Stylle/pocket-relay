package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRelayBindingFlow(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	})
	_, rev, _ := a.telegramConfig()
	in := telegramInput{Enabled: true, Receive: true, Send: true, BindFlow: true, Token: "123456:" + strings.Repeat("x", 30), Revision: rev}
	readJSON(t, req(a, "PUT", "/api/admin/telegram", token, in), 200)
	cfg, rev, _ := a.telegramConfig()
	if cfg.ChatID != "" {
		t.Fatal("save implicitly bound")
	}
	pair := readJSON(t, req(a, "POST", "/api/admin/telegram/pair", token, nil), 200)
	command := pair["command"].(string)
	m := &telegramMessage{Text: command, Date: now()}
	m.Chat.ID = 42
	m.Chat.Type = "group"
	m.From.ID = 42
	if a.acceptRelayBinding(context.Background(), cfg, rev, m) {
		t.Fatal("group accepted")
	}
	m.Chat.Type = "private"
	if !a.acceptRelayBinding(context.Background(), cfg, rev, m) {
		t.Fatal("private bind failed")
	}
	bound, next, _ := a.telegramConfig()
	if bound.ChatID != "42" || next == rev {
		t.Fatal("binding not persisted")
	}
	if a.acceptRelayBinding(context.Background(), cfg, rev, m) {
		t.Fatal("proof replayed")
	}
	readJSON(t, req(a, "POST", "/api/admin/telegram/unbind", token, nil), 200)
	bound, _, _ = a.telegramConfig()
	if bound.ChatID != "" || bound.Token != cfg.Token {
		t.Fatal("unbind lost token")
	}
}
