package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMessagePrivacyToggleIsolationAndTickets(t *testing.T) {
	a := testApp(t)
	owner, _, _ := register(t, a, "privacy-owner")
	public(t, a, owner)
	other, _, _ := register(t, a, "privacy-other")
	created := readJSON(t, req(a, "POST", "/api/messages", owner, map[string]any{"text": "mail alice@example.com 13800138000"}), 201)
	id := created["id"].(string)
	readJSON(t, req(a, "PUT", "/api/messages/"+id+"/privacy", other, map[string]bool{"concealed": true}), 404)
	readJSON(t, req(a, "PUT", "/api/messages/"+id+"/privacy", owner, map[string]any{}), 400)
	result := readJSON(t, req(a, "PUT", "/api/messages/"+id+"/privacy", owner, map[string]bool{"concealed": true}), 200)
	if result["privacy_version"].(float64) != 1 {
		t.Fatal("privacy version not advanced")
	}
	page := readJSON(t, req(a, "GET", "/api/messages", owner, nil), 200)
	m := page["messages"].([]any)[0].(map[string]any)
	if m["text"] != "" || m["concealed"] != true {
		t.Fatal("concealed list exposes text")
	}
	readJSON(t, req(a, "GET", "/api/messages/"+id+"/text", other, nil), 404)
	readJSON(t, req(a, "PUT", "/api/messages/"+id+"/privacy", owner, map[string]bool{"concealed": false}), 200)
	page = readJSON(t, req(a, "GET", "/api/messages", owner, nil), 200)
	if page["privacy_revision"].(float64) != 2 {
		t.Fatal("other devices cannot detect privacy edits")
	}
	readJSON(t, uploadTest(t, a, owner, "sample.png", []byte("image"), "0"), 201)
	var mid, file string
	a.db.QueryRow("SELECT id,file_id FROM messages WHERE file_id!=''").Scan(&mid, &file)
	url := readJSON(t, req(a, "POST", "/api/files/"+file+"/ticket", owner, nil), 200)["url"].(string)
	readJSON(t, req(a, "PUT", "/api/messages/"+mid+"/privacy", owner, map[string]bool{"concealed": true}), 200)
	readJSON(t, req(a, "GET", url, "", nil), 401)
}

func TestFileOnlyPrivateUploadAndSharedFont(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "private-image")
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("concealed", "true")
	part, _ := form.CreateFormFile("file", "image.png")
	part.Write([]byte("image"))
	form.Close()
	r := httptest.NewRequest("POST", "/api/messages", &body)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	readJSON(t, w, 201)
	page := readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)
	if page["messages"].([]any)[0].(map[string]any)["concealed"] != true {
		t.Fatal("file-only privacy ignored")
	}
	for _, size := range []string{"small", "medium", "large"} {
		s := a.config()
		s.MessageSize = size
		readJSON(t, req(a, "PUT", "/api/admin", token, s), 200)
		page = readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)
		if page["message_size"] != size {
			t.Fatal("font setting not shared")
		}
	}
	s := a.config()
	s.MessageSize = "invalid"
	readJSON(t, req(a, "PUT", "/api/admin", token, s), 400)
	a.db.Close()
	restarted, err := openApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.db.Close()
	if restarted.config().MessageSize != "large" {
		t.Fatal("font preference not persisted")
	}
}

func TestTelegramReceivePrivacyDefaultAndOverride(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "tg-private")
	mockTelegram(t, a, func(w http.ResponseWriter, r *http.Request, method string) { fmt.Fprint(w, `{"ok":true,"result":{}}`) })
	cfg, rev := configureTelegram(t, a, token, uid)
	if !cfg.ReceiveConcealed {
		t.Fatal("new Telegram messages should default private")
	}
	for i := 1; i <= 2; i++ {
		var update telegramUpdate
		json.Unmarshal([]byte(fmt.Sprintf(`{"update_id":%d,"message":{"date":%d,"text":"incoming","from":{"id":42},"chat":{"id":42,"type":"private"}}}`, i, cfg.Since)), &update)
		if err := a.importTelegram(context.Background(), cfg, rev, update); err != nil {
			t.Fatal(err)
		}
		var concealed bool
		a.db.QueryRow("SELECT concealed FROM messages ORDER BY created DESC,rowid DESC LIMIT 1").Scan(&concealed)
		if concealed != (i == 1) {
			t.Fatal("receive privacy preference ignored")
		}
		if i == 1 {
			off := false
			in := telegramInput{Enabled: true, Receive: true, Send: true, UserID: uid, ChatID: "42", Revision: rev, ReceiveConcealed: &off}
			readJSON(t, req(a, "PUT", "/api/admin/telegram", token, in), 200)
			cfg, rev, _ = a.telegramConfig()
		}
	}
	var raw string
	a.db.QueryRow("SELECT value FROM telegram").Scan(&raw)
	decoded, _ := unseal(a.key, raw)
	var legacy map[string]any
	json.Unmarshal([]byte(decoded), &legacy)
	delete(legacy, "receive_concealed")
	encoded, _ := json.Marshal(legacy)
	a.db.Exec("UPDATE telegram SET value=?", seal(a.key, string(encoded)))
	old, _, _ := a.telegramConfig()
	if !old.ReceiveConcealed {
		t.Fatal("legacy configurations should default private for future incoming messages")
	}
}

func TestOldMessagePrivacyChangeInvalidatesRecentPageETag(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "privacy-pages")
	a.db.Exec(`INSERT INTO messages(id,user_id,device,text,created,expires) VALUES('old',?,'test','old text',1,0)`, uid)
	for i := 0; i < 101; i++ {
		a.db.Exec(`INSERT INTO messages(id,user_id,device,text,created,expires) VALUES(?,?,'test','recent',?,0)`, fmt.Sprintf("recent-%03d", i), uid, i+2)
	}
	before := req(a, "GET", "/api/messages", token, nil)
	readJSON(t, before, 200)
	readJSON(t, req(a, "PUT", "/api/messages/old/privacy", token, map[string]bool{"concealed": true}), 200)
	r := httptest.NewRequest("GET", "/api/messages", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("If-None-Match", before.Header().Get("ETag"))
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("ETag") == before.Header().Get("ETag") {
		t.Fatal("privacy edit outside the recent page was invisible to other devices")
	}
}
