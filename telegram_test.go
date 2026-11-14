package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mockTelegram(t *testing.T, a *app, handler func(http.ResponseWriter, *http.Request, string)) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "getMe" {
			fmt.Fprint(w, `{"ok":true,"result":{"username":"test_only_bot"}}`)
			return
		}
		if method == "getWebhookInfo" {
			fmt.Fprint(w, `{"ok":true,"result":{"url":""}}`)
			return
		}
		handler(w, r, method)
	}))
	t.Cleanup(s.Close)
	a.tgBase = s.URL
}

func configureTelegram(t *testing.T, a *app, token, owner string) (telegramConfig, string) {
	t.Helper()
	_, rev, err := a.telegramConfig()
	if err != nil {
		t.Fatal(err)
	}
	in := telegramInput{Enabled: true, Receive: true, Send: true, Token: "123456:" + strings.Repeat("x", 30), UserID: owner, ChatID: "42", Revision: rev}
	readJSON(t, req(a, "PUT", "/api/admin/telegram", token, in), 200)
	cfg, rev, err := a.telegramConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg, rev
}

func TestTelegramConfigurationAndSecrets(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-owner")
	public(t, a, token)
	guest, _, _ := register(t, a, "tg-guest")
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) { fmt.Fprint(w, `{"ok":true,"result":[]}`) })
	if req(a, "GET", "/api/admin/telegram", guest, nil).Code != 403 || req(a, "GET", "/api/admin/telegram", "", nil).Code != 401 {
		t.Fatal("non-admin can access robot settings")
	}
	cfg, rev := configureTelegram(t, a, token, uid)
	var raw string
	a.db.QueryRow(`SELECT value FROM telegram`).Scan(&raw)
	if strings.Contains(raw, cfg.Token) {
		t.Fatal("token is not encrypted")
	}
	body := readJSON(t, req(a, "GET", "/api/admin/telegram", token, nil), 200)
	if body["config"].(map[string]any)["token"] != "" || body["configured"] != true {
		t.Fatal("token leaked or missing saved indicator")
	}
	invalid := telegramInput{Enabled: true, Receive: true, Token: cfg.Token, ChatID: "-42", UserID: uid, Revision: rev}
	if req(a, "PUT", "/api/admin/telegram", token, invalid).Code != 400 {
		t.Fatal("accepted group chat")
	}
	invalid.ChatID = "42"
	invalid.UserID = "missing"
	if req(a, "PUT", "/api/admin/telegram", token, invalid).Code != 400 {
		t.Fatal("accepted missing owner")
	}
	invalid.UserID = uid
	invalid.Revision = "stale"
	if req(a, "PUT", "/api/admin/telegram", token, invalid).Code != 409 {
		t.Fatal("accepted stale config")
	}
	readJSON(t, req(a, "PUT", "/api/admin/telegram", token, telegramInput{Revision: rev, UserID: uid}), 200)
	disabled, _, _ := a.telegramConfig()
	if disabled.Enabled || disabled.Token != cfg.Token {
		t.Fatal("disable or preserve-token failed")
	}
}

func TestTelegramReceiveIsolationAttachmentsAndDeduplication(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-receiver")
	var cfg telegramConfig
	var since atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	image := append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, make([]byte, 64)...)
	makeMessage := func(id int64, chat int64, kind, text string, date int64) map[string]any {
		return map[string]any{"update_id": id, "message": map[string]any{"message_id": id, "date": date, "chat": map[string]any{"id": chat, "type": kind}, "from": map[string]any{"is_bot": false}, "text": text}}
	}
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		switch method {
		case "getUpdates":
			date := since.Load()
			var in struct {
				Offset int64 `json:"offset"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if in.Offset > 0 {
				cancel()
				fmt.Fprint(w, `{"ok":true,"result":[]}`)
				return
			}
			updates := []map[string]any{makeMessage(1, 99, "private", "unbound", date), makeMessage(2, 42, "group", "group", date), makeMessage(3, 42, "private", "stale", date-1), makeMessage(4, 42, "private", "accepted", date)}
			photo := makeMessage(5, 42, "private", "", date)
			photo["message"].(map[string]any)["photo"] = []any{map[string]any{"file_id": "test-file", "file_size": len(image)}}
			updates = append(updates, photo)
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates})
		case "getFile":
			fmt.Fprintf(w, `{"ok":true,"result":{"file_path":"photos/test.png","file_size":%d}}`, len(image))
		case "test.png":
			w.Write(image)
		default:
			t.Errorf("unexpected method %s", method)
			w.WriteHeader(500)
		}
	})
	var rev string
	cfg, rev = configureTelegram(t, a, token, uid)
	since.Store(cfg.Since)
	done := make(chan struct{})
	go func() { defer close(done); a.receiveTelegram(ctx, cfg, rev) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("receiver did not finish")
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count)
	if count != 2 {
		t.Fatalf("wrong import count %d", count)
	}
	var text, fileID string
	a.db.QueryRow(`SELECT text,file_id FROM messages WHERE file_id!=''`).Scan(&text, &fileID)
	data, err := os.ReadFile(filepath.Join(a.dir, "blobs", fileID))
	if err != nil || string(data) != string(image) {
		t.Fatal("received attachment not saved")
	}
	var next int64
	a.db.QueryRow(`SELECT next_update FROM telegram`).Scan(&next)
	if next != 6 {
		t.Fatal("offset not persisted")
	}
	var duplicate telegramUpdate
	json.Unmarshal([]byte(fmt.Sprintf(`{"update_id":4,"message":{"date":%d,"text":"accepted","chat":{"id":42,"type":"private"}}}`, cfg.Since)), &duplicate)
	if err = a.importTelegram(context.Background(), cfg, rev, duplicate); err != nil {
		t.Fatal(err)
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count)
	if count != 2 {
		t.Fatal("duplicate update imported")
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox`).Scan(&count)
	if count != 0 {
		t.Fatal("received messages looped back to Telegram")
	}
}

func TestTelegramOutboxPrivacyRetryAndFileUpload(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-sender")
	public(t, a, token)
	other, _, _ := register(t, a, "tg-other")
	var mu sync.Mutex
	var texts []string
	var uploaded string
	limitOnce := true
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "sendMessage":
			if limitOnce {
				limitOnce = false
				fmt.Fprint(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`)
				return
			}
			var in struct {
				Text string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			texts = append(texts, in.Text)
			fmt.Fprint(w, `{"ok":true,"result":{}}`)
		case "sendDocument":
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, _, err := r.FormFile("document")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			b, _ := io.ReadAll(file)
			uploaded = string(b)
			fmt.Fprint(w, `{"ok":true,"result":{}}`)
		default:
			w.WriteHeader(500)
		}
	})
	cfg, rev := configureTelegram(t, a, token, uid)
	readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "private", "concealed": true}), 201)
	readJSON(t, req(a, "POST", "/api/messages", other, map[string]any{"text": "other account"}), 201)
	readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "outgoing"}), 201)
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox`).Scan(&count)
	if count != 1 {
		t.Fatal("wrong account/private message queued")
	}
	if _, err := a.deliverTelegram(context.Background(), cfg, rev); err == nil {
		t.Fatal("expected throttling error")
	}
	var step, attempts int
	var available int64
	a.db.QueryRow(`SELECT step,attempts,available FROM telegram_outbox`).Scan(&step, &attempts, &available)
	if step != 0 || attempts != 1 || available <= now() {
		t.Fatal("failed request not durably scheduled")
	}
	a.db.Exec(`UPDATE telegram_outbox SET available=0`)
	if _, err := a.deliverTelegram(context.Background(), cfg, rev); err != nil {
		t.Fatal(err)
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox`).Scan(&count)
	if count != 0 {
		t.Fatal("successful delivery retained in queue")
	}
	readJSON(t, uploadTest(t, a, token, "sample.txt", []byte("test attachment"), "0"), 201)
	if _, err := a.deliverTelegram(context.Background(), cfg, rev); err != nil {
		t.Fatal(err)
	}
	base := a.tgBase
	a.db.Close()
	reopened, err := openApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	a = reopened
	a.tgBase = base
	cfg, rev, err = a.telegramConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.deliverTelegram(context.Background(), cfg, rev); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if uploaded != "test attachment" || len(texts) != 2 || texts[0] != "outgoing" {
		t.Fatal("text or file delivery failed")
	}
}

func TestTelegramShutdownAndDisable(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-cancel")
	started := make(chan struct{}, 1)
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		if method == "getUpdates" {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			select {
			case started <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})
	_, rev := configureTelegram(t, a, token, uid)
	readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "queued"}), 201)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runTelegram(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("polling did not start")
	}
	readJSON(t, req(a, "PUT", "/api/admin/telegram", token, telegramInput{Revision: rev}), 200)
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox`).Scan(&count)
	if count != 0 {
		t.Fatal("disable did not clear old queue")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not stop")
	}
}

func TestTelegramChunkingAndPathValidation(t *testing.T) {
	text := strings.Repeat("😀", 4000) + "tail"
	parts := telegramChunks(text)
	if strings.Join(parts, "") != text || len(parts) != 3 {
		t.Fatal("unicode chunking corrupts text")
	}
	for _, part := range parts {
		units := 0
		for _, r := range part {
			units++
			if r > 0xffff {
				units++
			}
		}
		if units > 3500 {
			t.Fatal("oversized UTF-16 chunk")
		}
	}
	a := testApp(t)
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		fmt.Fprint(w, `{"ok":true,"result":{"file_path":"../secret","file_size":1}}`)
	})
	if _, err := a.telegramDownload(context.Background(), "123456:"+strings.Repeat("x", 30), &telegramFile{ID: "test"}, 1024); err == nil {
		t.Fatal("accepted unsafe file path")
	}
}

func TestTelegramOversizedUnknownAttachmentDoesNotBlockUpdates(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-limit")
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		if method != "getFile" {
			t.Errorf("unexpected download %s", method)
		}
		fmt.Fprint(w, `{"ok":true,"result":{"file_path":"documents/large.bin","file_size":22020096}}`)
	})
	cfg, rev := configureTelegram(t, a, token, uid)
	var update telegramUpdate
	json.Unmarshal([]byte(fmt.Sprintf(`{"update_id":1,"message":{"message_id":1,"date":%d,"document":{"file_id":"test","file_name":"large.bin"},"chat":{"id":42,"type":"private"}}}`, cfg.Since)), &update)
	if err := a.importTelegram(context.Background(), cfg, rev, update); err != nil {
		t.Fatal(err)
	}
	var text, file string
	a.db.QueryRow(`SELECT text,file_id FROM messages`).Scan(&text, &file)
	if file != "" || !strings.Contains(text, "附件未导入") {
		t.Fatal("oversized attachment not represented safely")
	}
}

func TestTelegramExpiredMessagesAndManualRetry(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-retry")
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		t.Errorf("unexpected delivery %s", method)
		fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})
	cfg, rev := configureTelegram(t, a, token, uid)
	b := readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "expired"}), 201)
	a.db.Exec(`UPDATE messages SET expires=? WHERE id=?`, now()-1, b["id"])
	if _, err := a.deliverTelegram(context.Background(), cfg, rev); err != nil {
		t.Fatal(err)
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox`).Scan(&count)
	if count != 0 {
		t.Fatal("expired message retained")
	}
	readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "retry me"}), 201)
	a.db.Exec(`UPDATE telegram_outbox SET failed=1,attempts=10`)
	readJSON(t, req(a, "POST", "/api/admin/telegram/retry", token, nil), 200)
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox WHERE failed=0 AND attempts=0`).Scan(&count)
	if count != 1 {
		t.Fatal("manual retry did not reset queue")
	}
	readJSON(t, req(a, "PUT", "/api/admin/telegram", token, telegramInput{Revision: rev}), 200)
	readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "disabled"}), 201)
	a.db.QueryRow(`SELECT COUNT(*) FROM telegram_outbox`).Scan(&count)
	if count != 0 {
		t.Fatal("disabled integration still queues messages")
	}
}

func TestTelegramRedirectAndErrorRedaction(t *testing.T) {
	a := testApp(t)
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}))
	defer destination.Close()
	token := "123456:" + strings.Repeat("x", 30)
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) {
		if method == "sendMessage" {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 401, "description": "sensitive URL /bot" + token})
	})
	for _, method := range []string{"sendMessage", "sendDocument"} {
		err := a.telegramCall(context.Background(), token, method, map[string]any{}, nil)
		if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), a.tgBase) {
			t.Fatal("unsafe API error")
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("Telegram redirects must not be followed")
	}
}

func TestTelegramSettingsDoNotWaitForWebUploadGate(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-upload-settings")
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) { fmt.Fprint(w, `{"ok":true,"result":{}}`) })
	_, rev := configureTelegram(t, a, token, uid)
	a.gate.RLock()
	defer a.gate.RUnlock()
	done := make(chan int, 1)
	go func() { done <- req(a, "PUT", "/api/admin/telegram", token, telegramInput{Revision: rev}).Code }()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("save returned %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("settings blocked behind web upload")
	}
}
