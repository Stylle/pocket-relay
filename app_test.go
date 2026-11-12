package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testApp(t *testing.T) *app {
	t.Helper()
	a, e := openApp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.db.Close() })
	return a
}
func req(a *app, method, path, token string, body any) *httptest.ResponseRecorder {
	var data io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		data = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, data)
	r.RemoteAddr = "192.168.10.20:12345"
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}
func readJSON(t *testing.T, w *httptest.ResponseRecorder, code int) map[string]any {
	t.Helper()
	if w.Code != code {
		t.Fatalf("HTTP %d, expected %d: %s", w.Code, code, w.Body.String())
	}
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func register(t *testing.T, a *app, name string) (string, string, string) {
	t.Helper()
	b := readJSON(t, req(a, "POST", "/api/enroll", "", map[string]string{"name": name}), 200)
	secret := b["secret"].(string)
	b = readJSON(t, req(a, "POST", "/api/enroll/confirm", "", map[string]string{"enrollment": b["enrollment"].(string), "code": totp(secret, now()/30)}), 201)
	return b["token"].(string), b["user"].(map[string]any)["id"].(string), secret
}
func public(t *testing.T, a *app, token string) {
	s := a.config()
	s.Public = true
	readJSON(t, req(a, "PUT", "/api/admin", token, s), 200)
}
func uploadTest(t *testing.T, a *app, token, name string, data []byte, ttl string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("device", "工作电脑")
	form.WriteField("text", "附言 🌿")
	form.WriteField("ttl", ttl)
	part, e := form.CreateFormFile("file", name)
	if e != nil {
		t.Fatal(e)
	}
	part.Write(data)
	form.Close()
	r := httptest.NewRequest("POST", "/api/messages", &body)
	r.RemoteAddr = "192.168.10.20:12345"
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}

func TestTOTPRFC6238(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	for _, tc := range []struct {
		ts   int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		if got := totp(secret, tc.ts/30); got != tc.code {
			t.Errorf("TOTP mismatch for %d", tc.ts)
		}
	}
}
func TestRegistrationLoginAndExpiry(t *testing.T) {
	a := testApp(t)
	status := readJSON(t, req(a, "GET", "/api/status", "", nil), 200)
	if status["setup"] != true {
		t.Fatal("expected setup")
	}
	admin, id, secret := register(t, a, "管理员")
	u, e := a.findUser(id)
	if e != nil || !u.Admin || u.Expires != 0 {
		t.Fatal("first account is not permanent admin")
	}
	readJSON(t, req(a, "POST", "/api/enroll", "", map[string]string{"name": "closed"}), 403)
	readJSON(t, req(a, "POST", "/api/login", "", map[string]string{"name": "管理员", "code": totp(secret, now()/30)}), 401)
	readJSON(t, req(a, "POST", "/api/login", "", map[string]string{"name": "管理员", "code": totp(secret, now()/30+1)}), 200)
	public(t, a, admin)
	guest, gid, _ := register(t, a, "guest")
	g, e := a.findUser(gid)
	if e != nil || g.Admin || g.Expires < now()+3590 || g.Expires > now()+3601 {
		t.Fatal("guest lifetime mismatch")
	}
	readJSON(t, req(a, "GET", "/api/admin", guest, nil), 403)
	if _, e = a.db.Exec(`UPDATE users SET expires=? WHERE id=?`, now()-1, gid); e != nil {
		t.Fatal(e)
	}
	readJSON(t, req(a, "GET", "/api/messages", guest, nil), 401)
	readJSON(t, req(a, "PUT", "/api/admin/users/"+gid, admin, map[string]int{"hours": 2}), 200)
	readJSON(t, req(a, "GET", "/api/messages", guest, nil), 200)
	var encrypted string
	a.db.QueryRow(`SELECT secret FROM users WHERE id=?`, id).Scan(&encrypted)
	if strings.Contains(encrypted, secret) {
		t.Fatal("secret stored in clear")
	}
}
func TestUserIsolationTicketsAndRetention(t *testing.T) {
	a := testApp(t)
	admin, _, _ := register(t, a, "owner")
	public(t, a, admin)
	guest, _, _ := register(t, a, "visitor")
	payload := []byte("arbitrary file including zero\x00bytes")
	b := readJSON(t, uploadTest(t, a, admin, "测试.bin", payload, "0"), 201)
	mid := b["id"].(string)
	list := readJSON(t, req(a, "GET", "/api/messages", admin, nil), 200)["messages"].([]any)
	m := list[0].(map[string]any)
	fileID := m["file_id"].(string)
	if m["device"] != "工作电脑 · 192.168.*.*" {
		t.Fatal("device/IP not masked")
	}
	other := readJSON(t, req(a, "GET", "/api/messages", guest, nil), 200)["messages"].([]any)
	if len(other) != 0 {
		t.Fatal("cross-user message disclosure")
	}
	readJSON(t, req(a, "DELETE", "/api/messages/"+mid, guest, nil), 404)
	readJSON(t, req(a, "POST", "/api/files/"+fileID+"/ticket", guest, nil), 404)
	url := readJSON(t, req(a, "POST", "/api/files/"+fileID+"/ticket", admin, nil), 200)["url"].(string)
	w := req(a, "GET", url, "", nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatal("download mismatch")
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("unsafe file disposition")
	}
	readJSON(t, req(a, "POST", "/api/logout", admin, nil), 200)
	readJSON(t, req(a, "GET", url, "", nil), 401)
	// A new page has no bearer token, even if account/device names are remembered.
	readJSON(t, req(a, "GET", "/api/messages", "", nil), 401)
	// Expired messages/files are inaccessible even before the janitor runs.
	var ownerID string
	a.db.QueryRow(`SELECT id FROM users WHERE name='owner'`).Scan(&ownerID)
	token := a.issue(user{ID: ownerID, Name: "owner", Admin: true})["token"].(string)
	a.db.Exec(`UPDATE messages SET expires=? WHERE id=?`, now()-1, mid)
	if len(readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)["messages"].([]any)) != 0 {
		t.Fatal("expired message visible")
	}
	readJSON(t, req(a, "POST", "/api/files/"+fileID+"/ticket", token, nil), 404)
	a.cleanup(context.Background())
	if _, e := os.Stat(filepath.Join(a.dir, "blobs", fileID)); !os.IsNotExist(e) {
		t.Fatal("expired blob not deleted")
	}
}
func TestUploadLimitAndCursor(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "owner")
	s := a.config()
	s.MaxMB = 1
	readJSON(t, req(a, "PUT", "/api/admin", token, s), 200)
	readJSON(t, uploadTest(t, a, token, "too-large.bin", make([]byte, (1<<20)+1), "0"), 413)
	entries, _ := os.ReadDir(filepath.Join(a.dir, "blobs"))
	if len(entries) != 0 {
		t.Fatal("failed upload leaked blob")
	}
	for i := 0; i < 105; i++ {
		_, e := a.db.Exec(`INSERT INTO messages(id,user_id,device,text,created,expires) VALUES(?,?,?,?,?,0)`, randomID(), uid, "test", "same timestamp", 12345)
		if e != nil {
			t.Fatal(e)
		}
	}
	first := readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)["messages"].([]any)
	if len(first) != 100 {
		t.Fatal("page size")
	}
	last := first[99].(map[string]any)
	second := readJSON(t, req(a, "GET", "/api/messages?before=12345&before_id="+last["id"].(string), token, nil), 200)["messages"].([]any)
	if len(second) != 5 {
		t.Fatal("pagination skipped same-timestamp messages")
	}
}
func TestWebDAVAndStorageSwitch(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	var mu sync.Mutex
	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "test" || p != "password" {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(201)
		case "PUT":
			objects[r.URL.Path], _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
		case "GET":
			b, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(b)
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	s := store{Kind: "webdav", Name: "test dav", URL: server.URL + "/dav/", Root: "pocket/files", User: "test", Password: "password"}
	b := readJSON(t, req(a, "POST", "/api/admin/storage", token, s), 201)
	cfg := a.config()
	cfg.ActiveStore = b["id"].(string)
	readJSON(t, req(a, "PUT", "/api/admin", token, cfg), 200)
	readJSON(t, uploadTest(t, a, token, "dav.txt", []byte("from remote"), "0"), 201)
	list := readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)["messages"].([]any)
	m := list[0].(map[string]any)
	cfg.ActiveStore = "local"
	readJSON(t, req(a, "PUT", "/api/admin", token, cfg), 200)
	url := readJSON(t, req(a, "POST", "/api/files/"+m["file_id"].(string)+"/ticket", token, nil), 200)["url"].(string)
	w := req(a, "GET", url, "", nil)
	if w.Code != 200 || w.Body.String() != "from remote" {
		t.Fatal("switch lost remote attachment")
	}
	admin := readJSON(t, req(a, "GET", "/api/admin", token, nil), 200)
	raw, _ := json.Marshal(admin)
	if bytes.Contains(raw, []byte("password")) {
		t.Fatal("password disclosed in admin response")
	}
	readJSON(t, req(a, "DELETE", "/api/messages/"+m["id"].(string), token, nil), 200)
	a.cleanup(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(objects) != 0 {
		t.Fatal("remote garbage not removed")
	}
}
func TestBackupEncryptionAndRestore(t *testing.T) {
	a := testApp(t)
	token, uid, secret := register(t, a, "owner")
	readJSON(t, uploadTest(t, a, token, "keep.bin", []byte("keep this local file"), "0"), 201)
	w := req(a, "POST", "/api/admin/backup", token, map[string]string{"password": "test-backup-password"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	encrypted := append([]byte(nil), w.Body.Bytes()...)
	if bytes.Contains(encrypted, []byte(secret)) || bytes.Contains(encrypted, []byte("keep this local file")) {
		t.Fatal("backup not encrypted")
	}
	var plain bytes.Buffer
	if e := decryptBackup(&plain, bytes.NewReader(encrypted), "wrong-password"); e == nil {
		t.Fatal("wrong password accepted")
	}
	plain.Reset()
	if e := decryptBackup(&plain, bytes.NewReader(encrypted[:len(encrypted)-1]), "test-backup-password"); e == nil {
		t.Fatal("truncated backup accepted")
	}
	plain.Reset()
	if e := decryptBackup(&plain, bytes.NewReader(encrypted), "test-backup-password"); e != nil {
		t.Fatal(e)
	}
	readJSON(t, req(a, "POST", "/api/messages", token, map[string]any{"text": "not in backup", "device": "test", "ttl": 0}), 201)
	r := httptest.NewRequest("POST", "/api/admin/restore", bytes.NewReader(encrypted))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Backup-Password", "test-backup-password")
	restored := httptest.NewRecorder()
	a.handler().ServeHTTP(restored, r)
	readJSON(t, restored, 200)
	select {
	case <-a.restart:
	default:
		t.Fatal("restart not requested")
	}
	a.db.Close()
	if e := applyRestore(a.dir); e != nil {
		t.Fatal(e)
	}
	if e := applyRestore(a.dir); e != nil {
		t.Fatal("restore not idempotent", e)
	}
	b, e := openApp(a.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer b.db.Close()
	u, e := b.findUser(uid)
	if e != nil || u.Name != "owner" {
		t.Fatal("user not restored")
	}
	var count int
	b.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count)
	if count != 1 {
		t.Fatal("snapshot not restored")
	}
	var enc string
	b.db.QueryRow(`SELECT secret FROM users WHERE id=?`, uid).Scan(&enc)
	decoded, e := unseal(b.key, enc)
	if e != nil || decoded != secret {
		t.Fatal("authenticator secret not restored")
	}
	if e = validateBackup(b.dir); e != nil {
		t.Fatal(e)
	}
}
func TestRejectZipTraversalAndTamper(t *testing.T) {
	dir := t.TempDir()
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	out, _ := z.Create("../outside")
	out.Write([]byte("bad"))
	z.Close()
	source := filepath.Join(dir, "bad.zip")
	os.WriteFile(source, data.Bytes(), 0600)
	if extractBackup(source, dir) == nil {
		t.Fatal("zip traversal accepted")
	}
	var encrypted bytes.Buffer
	encryptBackup(&encrypted, strings.NewReader("payload"), "test-backup-password")
	raw := encrypted.Bytes()
	raw[len(raw)/2] ^= 1
	var plain bytes.Buffer
	if decryptBackup(&plain, bytes.NewReader(raw), "test-backup-password") == nil {
		t.Fatal("tampering accepted")
	}
}
func TestHeadersAndAuthRateLimit(t *testing.T) {
	a := testApp(t)
	w := req(a, "GET", "/", "", nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing security headers")
	}
	for i := 0; i < 31; i++ {
		w = req(a, "POST", "/api/login", "", map[string]string{"name": "missing", "code": "000000"})
	}
	if w.Code != 429 {
		t.Fatal("no rate limit")
	}
	if maskedIP("[2001:db8::1]:123") != "2001:db8:*" {
		t.Fatal("ipv6 masking")
	}
	if _, ok := verifyTOTP("invalid", "000000", time.Now().Unix()); ok {
		t.Fatal("invalid TOTP")
	}
}
