package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed web/*
var assets embed.FS

type user struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Admin   bool   `json:"admin"`
	Expires int64  `json:"expires"`
}
type session struct {
	User       string
	Started    time.Time
	Expires    time.Time
	LastActive time.Time
}
type enrollment struct {
	Name, Secret string
	Expires      time.Time
}
type ticket struct {
	User, File, Session string
	Expires             time.Time
}
type bucket struct {
	Count int
	Until time.Time
}
type app struct {
	db       *sql.DB
	dir      string
	key      []byte
	mu       sync.Mutex
	sessions map[string]session
	pending  map[string]enrollment
	tickets  map[string]ticket
	attempts map[string]bucket
	// Serialize backup/restore and writes, not normal reads or streamed downloads.
	gate        sync.RWMutex
	restart     chan struct{}
	maintenance bool
	transfers   chan struct{}
	tgMu        sync.Mutex
	tgConfigMu  sync.Mutex
	tgWake      chan struct{}
	tgCancel    context.CancelFunc
	tgHTTP      *http.Client
	tgBase      string // Only overridden by isolated tests; never configurable through the API.
}
type settings struct {
	Public             bool   `json:"public_registration"`
	GuestHours         int    `json:"guest_hours"`
	MaxMB              int64  `json:"max_file_mb"`
	QuotaMB            int64  `json:"quota_mb"`
	DeviceTemplate     string `json:"device_template"`
	ActiveStore        string `json:"active_store"`
	SessionIdleMinutes int    `json:"session_idle_minutes"`
	AutoLoadImages     bool   `json:"auto_load_images"`
}

func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func now() int64 { return time.Now().Unix() }
func openApp(dir string) (*app, error) {
	for _, p := range []string{dir, filepath.Join(dir, "blobs"), filepath.Join(dir, "tmp")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			return nil, e
		}
	}
	keyPath := filepath.Join(dir, "master.key")
	key, e := os.ReadFile(keyPath)
	if errors.Is(e, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		if e = os.WriteFile(keyPath, key, 0600); e != nil {
			return nil, e
		}
	} else if e != nil {
		return nil, e
	}
	if len(key) != 32 {
		return nil, errors.New("master.key 无效，请恢复完整 data 备份")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "app.db"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	a := &app{db: db, dir: dir, key: key, sessions: map[string]session{}, pending: map[string]enrollment{}, tickets: map[string]ticket{}, attempts: map[string]bucket{}, restart: make(chan struct{}), transfers: make(chan struct{}, 4)}
	a.tgWake = make(chan struct{}, 1)
	a.tgHTTP = &http.Client{Timeout: 70 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	a.tgBase = "https://api.telegram.org"
	schema := `PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,secret TEXT NOT NULL,admin INTEGER NOT NULL,expires INTEGER NOT NULL,last_step INTEGER NOT NULL DEFAULT -1);
 CREATE TABLE IF NOT EXISTS config(id INTEGER PRIMARY KEY CHECK(id=1),value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS stores(id TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),device TEXT NOT NULL,text TEXT NOT NULL,created INTEGER NOT NULL,expires INTEGER NOT NULL,file_id TEXT NOT NULL DEFAULT '');
 CREATE INDEX IF NOT EXISTS messages_user_created ON messages(user_id,created);
 CREATE TABLE IF NOT EXISTS files(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),store_id TEXT NOT NULL,name TEXT NOT NULL,mime TEXT NOT NULL,size INTEGER NOT NULL,sha256 TEXT NOT NULL,deleted INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS files_user ON files(user_id);`
	schema += `CREATE TABLE IF NOT EXISTS telegram(id INTEGER PRIMARY KEY CHECK(id=1),value TEXT NOT NULL,revision TEXT NOT NULL,next_update INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',receive_error TEXT NOT NULL DEFAULT '',send_error TEXT NOT NULL DEFAULT '',received INTEGER NOT NULL DEFAULT 0,sent INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS telegram_inbox(bot TEXT NOT NULL,update_id INTEGER NOT NULL,PRIMARY KEY(bot,update_id));
 CREATE TABLE IF NOT EXISTS telegram_outbox(message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,revision TEXT NOT NULL,step INTEGER NOT NULL DEFAULT 0,attempts INTEGER NOT NULL DEFAULT 0,available INTEGER NOT NULL DEFAULT 0,failed INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS telegram_ready ON telegram_outbox(failed,available);`
	if _, e = db.Exec(schema); e != nil {
		db.Close()
		return nil, e
	}
	if e = migrateMessages(db); e != nil {
		db.Close()
		return nil, e
	}
	// A process may have stopped between reserving an object and committing its
	// message. Reclaim it through the same retryable deletion queue on startup.
	if _, e = db.Exec(`UPDATE files SET deleted=1 WHERE deleted=-1`); e != nil {
		db.Close()
		return nil, e
	}
	defaults, _ := json.Marshal(settings{GuestHours: 1, MaxMB: 256, QuotaMB: 5120, DeviceTemplate: "{device} · {ip}", ActiveStore: "local", SessionIdleMinutes: 15, AutoLoadImages: true})
	if _, e = db.Exec(`INSERT OR IGNORE INTO config(id,value) VALUES(1,?)`, string(defaults)); e != nil {
		db.Close()
		return nil, e
	}
	// Interrupted upload staging is never indexed. It is safe to remove on startup.
	entries, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	for _, f := range entries {
		if !f.IsDir() {
			os.Remove(filepath.Join(dir, "tmp", f.Name()))
		}
	}
	return a, nil
}
func (a *app) config() settings {
	var b string
	if e := a.db.QueryRow(`SELECT value FROM config WHERE id=1`).Scan(&b); e != nil {
		panic(e)
	}
	s := settings{SessionIdleMinutes: 15, AutoLoadImages: true}
	if e := json.Unmarshal([]byte(b), &s); e != nil {
		panic(e)
	}
	return s
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "请求格式不正确")
		return false
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		fail(w, 400, "请求格式不正确")
		return false
	}
	return true
}
func (a *app) findUser(id string) (user, error) {
	var u user
	e := a.db.QueryRow(`SELECT id,name,admin,expires FROM users WHERE id=?`, id).Scan(&u.ID, &u.Name, &u.Admin, &u.Expires)
	if e == nil && u.Expires > 0 && u.Expires <= now() {
		e = errors.New("expired")
	}
	return u, e
}
func (a *app) auth(w http.ResponseWriter, r *http.Request) (user, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	idleMinutes := a.config().SessionIdleMinutes
	a.mu.Lock()
	s, ok := a.sessions[token]
	if !ok || sessionExpired(s, idleMinutes, time.Now()) {
		delete(a.sessions, token)
		a.mu.Unlock()
		fail(w, 401, "请重新输入验证器验证码")
		return user{}, false
	}
	a.mu.Unlock()
	u, e := a.findUser(s.User)
	if e != nil {
		fail(w, 401, "账户已过期或不存在")
		return user{}, false
	}
	return u, true
}
func (a *app) admin(w http.ResponseWriter, r *http.Request) (user, bool) {
	u, ok := a.auth(w, r)
	if ok && !u.Admin {
		fail(w, 403, "仅管理员可操作")
		return u, false
	}
	return u, ok
}
func maskedIP(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if ip == nil {
		return "未知地址"
	}
	if v := ip.To4(); v != nil {
		return fmt.Sprintf("%d.%d.*.*", v[0], v[1])
	}
	parts := strings.Split(ip.String(), ":")
	return strings.Join(parts[:2], ":") + ":*"
}
func (a *app) device(r *http.Request, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "未命名设备"
	}
	if len(name) > 80 {
		name = string([]rune(name)[:min(20, len([]rune(name)))])
	}
	t := a.config().DeviceTemplate
	return strings.NewReplacer("{device}", name, "{ip}", maskedIP(r.RemoteAddr)).Replace(t)
}
func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("POST /api/enroll", a.enroll)
	mux.HandleFunc("POST /api/enroll/confirm", a.confirm)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("GET /api/session", a.sessionInfo)
	mux.HandleFunc("POST /api/session/activity", a.sessionActivity)
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		delete(a.sessions, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		a.mu.Unlock()
		reply(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/messages", a.listMessages)
	mux.HandleFunc("POST /api/messages", a.sendMessage)
	mux.HandleFunc("GET /api/messages/{id}/text", a.messageText)
	mux.HandleFunc("DELETE /api/messages/{id}", a.deleteMessage)
	mux.HandleFunc("POST /api/files/{id}/ticket", a.fileTicket)
	mux.HandleFunc("GET /file/{id}", a.download)
	mux.HandleFunc("GET /api/admin", a.adminGet)
	mux.HandleFunc("PUT /api/admin", a.adminSave)
	mux.HandleFunc("GET /api/admin/telegram", a.telegramGet)
	mux.HandleFunc("PUT /api/admin/telegram", a.telegramSave)
	mux.HandleFunc("POST /api/admin/telegram/test", a.telegramTest)
	mux.HandleFunc("POST /api/admin/telegram/retry", a.telegramRetry)
	mux.HandleFunc("POST /api/admin/storage", a.addStore)
	mux.HandleFunc("POST /api/admin/storage/test", a.testStore)
	mux.HandleFunc("PUT /api/admin/users/{id}", a.updateUser)
	mux.HandleFunc("POST /api/admin/backup", a.exportBackup)
	mux.HandleFunc("POST /api/admin/restore", a.restoreBackup)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	sub, _ := fs.Sub(assets, "web")
	static := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		static.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		a.mu.Lock()
		busy := a.maintenance
		a.mu.Unlock()
		if busy {
			fail(w, 503, "正在恢复备份，服务即将重启")
			return
		}
		defer func() {
			if recover() != nil {
				log.Print("请求处理失败（未记录请求内容）")
				fail(w, 500, "服务暂时不可用，请重试")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}
func main() {
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "data"
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = ":8080"
	}
	if e := applyRestore(dir); e != nil {
		log.Fatal("恢复失败，原数据保留，请检查 data：", e)
	}
	a, e := openApp(dir)
	if e != nil {
		log.Fatal("无法打开 data：", e)
	}
	defer a.db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.janitor(ctx)
	var telegramWorkers sync.WaitGroup
	telegramWorkers.Add(1)
	go func() { defer telegramWorkers.Done(); a.runTelegram(ctx) }()
	defer func() { stop(); telegramWorkers.Wait() }()
	server := &http.Server{Addr: address, Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		select {
		case <-ctx.Done():
		case <-a.restart:
			stop()
		}
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	urlAddress := address
	if strings.HasPrefix(urlAddress, ":") {
		urlAddress = "localhost" + urlAddress
	}
	log.Printf("Pocket Relay 已启动：http://%s；首次初始化请在可信网络完成。", urlAddress)
	if e = server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		log.Fatal(e)
	}
}
