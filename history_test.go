package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLegacyMessageMigration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE messages(id TEXT PRIMARY KEY,text TEXT); INSERT INTO messages VALUES('legacy','preserved')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = migrateMessages(db); err != nil {
			t.Fatal(err)
		}
	}
	var text string
	var concealed bool
	if err = db.QueryRow(`SELECT text,concealed FROM messages WHERE id='legacy'`).Scan(&text, &concealed); err != nil {
		t.Fatal(err)
	}
	if text != "preserved" || concealed {
		t.Fatal("migration changed existing messages")
	}
}

func TestHistoryScopeAndSearch(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "history-owner")
	public(t, a, token)
	other, otherID, _ := register(t, a, "other-owner")
	start := time.Now().Add(-time.Minute)
	a.mu.Lock()
	s := a.sessions[token]
	s.Started = start
	a.sessions[token] = s
	a.mu.Unlock()
	insert := func(id, owner, text string, created, expires int64) {
		t.Helper()
		if _, err := a.db.Exec(`INSERT INTO messages(id,user_id,device,text,created,expires) VALUES(?,?,'test',?,?,?)`, id, owner, text, created, expires); err != nil {
			t.Fatal(err)
		}
	}
	insert("old", uid, "历史查找 100%_ 中文", start.Add(-time.Hour).UnixMilli(), 0)
	insert("new", uid, "本次消息", start.Add(time.Second).UnixMilli(), 0)
	insert("other", otherID, "历史查找 100%_ 中文", start.UnixMilli(), 0)
	insert("expired", uid, "历史查找 100%_ 中文", start.UnixMilli(), now()-1)
	for i := 0; i < 105; i++ {
		insert(fmt.Sprintf("filler%03d", i), uid, "filler", start.Add(-time.Second).UnixMilli(), 0)
	}
	list := func(path, tok string) map[string]any { return readJSON(t, req(a, "GET", path, tok, nil), 200) }
	current := list("/api/messages?scope=current", token)["messages"].([]any)
	if len(current) != 1 || current[0].(map[string]any)["id"] != "new" {
		t.Fatal("current session includes history")
	}
	for _, path := range []string{"/api/messages?scope=current", "/api/messages?scope=all", "/api/messages?files=1", "/api/messages?q=not-found", "/api/messages?before=1"} {
		if list(path, token)["current_count"] != float64(1) {
			t.Fatalf("current badge depends on list filters: %s", path)
		}
	}
	full := list("/api/messages?scope=all", token)
	if len(full["messages"].([]any)) != 100 || full["has_more"] != true {
		t.Fatal("missing pagination")
	}
	query := "/api/messages?q=" + url.QueryEscape("100%_ 中文")
	results := list(query, token)["messages"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["id"] != "old" {
		t.Fatal("search did not cover full history or leaked account/expired messages")
	}
	if len(list(query, other)["messages"].([]any)) != 1 {
		t.Fatal("owner isolation failed")
	}
	for _, q := range []string{"from=bad", "from=2&to=1", "scope=bad", "q=" + strings.Repeat("a", 1025)} {
		if req(a, "GET", "/api/messages?"+q, token, nil).Code != 400 {
			t.Fatalf("invalid filter accepted: %s", q[:min(len(q), 30)])
		}
	}
	bounded := list(fmt.Sprintf("/api/messages?from=%d&to=%d", start.UnixMilli(), start.Add(time.Second).UnixMilli()), token)["messages"].([]any)
	if len(bounded) != 0 {
		t.Fatal("end-exclusive time range violated")
	}
	info := list("/api/session", token)
	if info["started_at"] != float64(start.UnixMilli()) {
		t.Fatal("refresh changes login boundary")
	}
}

func TestConcealedTextAndImagePolicy(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "conceal-owner")
	public(t, a, token)
	other, _, _ := register(t, a, "conceal-other")
	b := readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "private full body", "concealed": true}), 201)
	id := b["id"].(string)
	w := req(a, "GET", "/api/messages?q=private", token, nil)
	if strings.Contains(w.Body.String(), "private full body") {
		t.Fatal("list leaks concealed text")
	}
	list := readJSON(t, w, 200)
	m := list["messages"].([]any)[0].(map[string]any)
	if m["concealed"] != true || m["text"] != "" {
		t.Fatal("concealed metadata missing")
	}
	path := "/api/messages/" + id + "/text"
	if req(a, "GET", path, "", nil).Code != 401 || req(a, "GET", path, other, nil).Code != 404 {
		t.Fatal("reveal authorization missing")
	}
	if readJSON(t, req(a, "GET", path, token, nil), 200)["text"] != "private full body" {
		t.Fatal("reveal failed")
	}
	a.db.Exec(`UPDATE messages SET expires=? WHERE id=?`, now()-1, id)
	if req(a, "GET", path, token, nil).Code != 404 {
		t.Fatal("expired reveal accepted")
	}
	s := a.config()
	if !s.AutoLoadImages {
		t.Fatal("images should default to enabled")
	}
	s.AutoLoadImages = false
	readJSON(t, req(a, "PUT", "/api/admin", token, s), 200)
	if a.config().AutoLoadImages || readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)["auto_load_images"] != false {
		t.Fatal("image preference not persisted")
	}
	if err := migrateMessages(a.db); err != nil {
		t.Fatal("migration must be idempotent", err)
	}
}
