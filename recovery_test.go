package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestInterruptedRemoteUploadIsQueuedForCleanup(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	var mu sync.Mutex
	objects := map[string][]byte{}
	broken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "PUT":
			objects[r.URL.Path], _ = io.ReadAll(r.Body)
			if broken {
				w.WriteHeader(500)
			} else {
				w.WriteHeader(201)
			}
		case "GET":
			w.Write(objects[r.URL.Path])
		case "DELETE":
			if broken {
				w.WriteHeader(503)
			} else {
				delete(objects, r.URL.Path)
				w.WriteHeader(204)
			}
		}
	}))
	defer server.Close()
	st := store{Kind: "webdav", Name: "flaky", URL: server.URL}
	id := readJSON(t, req(a, "POST", "/api/admin/storage", token, st), 201)["id"].(string)
	s := a.config()
	s.ActiveStore = id
	readJSON(t, req(a, "PUT", "/api/admin", token, s), 200)
	mu.Lock()
	broken = true
	mu.Unlock()
	readJSON(t, uploadTest(t, a, token, "partial.bin", []byte("partial sensitive content"), "0"), 502)
	var queued int
	if e := a.db.QueryRow(`SELECT COUNT(*) FROM files WHERE deleted=1`).Scan(&queued); e != nil || queued != 1 {
		t.Fatal("failed object not durably queued")
	}
	var messages int
	a.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&messages)
	if messages != 0 {
		t.Fatal("failed upload appeared in inbox")
	}
	mu.Lock()
	broken = false
	mu.Unlock()
	a.cleanup(context.Background())
	mu.Lock()
	left := len(objects)
	mu.Unlock()
	if left != 0 {
		t.Fatal("remote partial not reclaimed")
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&queued)
	if queued != 0 {
		t.Fatal("cleanup queue not drained")
	}
}
func TestStartupRecoversUncommittedLocalObject(t *testing.T) {
	a := testApp(t)
	_, uid, _ := register(t, a, "owner")
	id := randomID()
	if _, e := a.db.Exec(`INSERT INTO files(id,user_id,store_id,name,mime,size,sha256,deleted) VALUES(?,?,'local','interrupted','application/octet-stream',7,'unused',-1)`, id, uid); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(a.dir, "blobs", id), []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	a.db.Close()
	b, e := openApp(a.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer b.db.Close()
	b.cleanup(context.Background())
	if _, e = os.Stat(filepath.Join(a.dir, "blobs", id)); !os.IsNotExist(e) {
		t.Fatal("uncommitted local object remained")
	}
}
func TestConditionalInboxRead(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	w := req(a, "GET", "/api/messages", token, nil)
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing etag")
	}
	r := httptest.NewRequest("GET", "/api/messages", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("If-None-Match", etag)
	cached := httptest.NewRecorder()
	a.handler().ServeHTTP(cached, r)
	if cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatal("unchanged data retransmitted")
	}
	readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "changed", "device": "test", "ttl": 0}), 201)
	next := httptest.NewRecorder()
	a.handler().ServeHTTP(next, r)
	if next.Code != 200 || next.Header().Get("ETag") == etag {
		t.Fatal("changed data incorrectly cached")
	}
}
