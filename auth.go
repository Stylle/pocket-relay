package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

func seal(key []byte, plain string) string {
	block, e := aes.NewCipher(key)
	if e != nil {
		panic(e)
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		panic(e)
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		panic(e)
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(n, n, []byte(plain), []byte("pocket-relay:v1")))
}
func unseal(key []byte, encoded string) (string, error) {
	b, e := base64.RawStdEncoding.DecodeString(encoded)
	if e != nil {
		return "", e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	if len(b) < g.NonceSize() {
		return "", fmt.Errorf("invalid ciphertext")
	}
	p, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("pocket-relay:v1"))
	return string(p), e
}
func totp(secret string, step int64) string {
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if e != nil {
		return ""
	}
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, uint64(step))
	h := hmac.New(sha1.New, key)
	h.Write(msg)
	sum := h.Sum(nil)
	o := sum[len(sum)-1] & 15
	n := (binary.BigEndian.Uint32(sum[o:o+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", n)
}
func verifyTOTP(secret, code string, last int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	t := now() / 30
	for _, step := range []int64{t, t - 1, t + 1} {
		if step > last && subtle.ConstantTimeCompare([]byte(totp(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
func (a *app) limited(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.attempts[host]; !exists && len(a.attempts) >= 10000 {
		return true
	}
	b := a.attempts[host]
	if time.Now().After(b.Until) {
		b = bucket{Until: time.Now().Add(10 * time.Minute)}
	}
	b.Count++
	a.attempts[host] = b
	return b.Count > 30
}
func (a *app) status(w http.ResponseWriter, r *http.Request) {
	s := a.config()
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	reply(w, 200, map[string]any{"setup": n == 0, "public_registration": s.Public, "guest_hours": s.GuestHours})
}

var validName = regexp.MustCompile(`^[\p{L}\p{N}_-]{2,40}$`)

func (a *app) enroll(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		fail(w, 429, "操作过于频繁，请十分钟后重试")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validName.MatchString(in.Name) {
		fail(w, 400, "账户名为 2–40 位文字、数字、下划线或短横线")
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if n > 0 && !a.config().Public {
		fail(w, 403, "公开注册已关闭")
		return
	}
	var exists int
	a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE name=?`, in.Name).Scan(&exists)
	if exists > 0 {
		fail(w, 409, "账户名已使用")
		return
	}
	b := make([]byte, 20)
	if _, e := rand.Read(b); e != nil {
		fail(w, 500, "生成密钥失败")
		return
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	id := randomID()
	a.mu.Lock()
	if len(a.pending) >= 64 {
		a.mu.Unlock()
		fail(w, 429, "等待注册人数过多，请稍后重试")
		return
	}
	a.pending[id] = enrollment{in.Name, secret, time.Now().Add(10 * time.Minute)}
	a.mu.Unlock()
	uri := "otpauth://totp/" + url.PathEscape("Pocket Relay:"+in.Name) + "?" + url.Values{"secret": {secret}, "issuer": {"Pocket Relay"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
	png, e := qrcode.Encode(uri, qrcode.Medium, 256)
	if e != nil {
		fail(w, 500, "二维码生成失败")
		return
	}
	reply(w, 200, map[string]any{"enrollment": id, "secret": secret, "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), "uri": uri})
}
func (a *app) issue(u user) map[string]any {
	token := randomID() + randomID()
	expiry := time.Now().Add(12 * time.Hour)
	if u.Expires > 0 && time.Unix(u.Expires, 0).Before(expiry) {
		expiry = time.Unix(u.Expires, 0)
	}
	a.mu.Lock()
	a.sessions[token] = session{u.ID, expiry}
	a.mu.Unlock()
	return map[string]any{"token": token, "user": u}
}
func (a *app) confirm(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		fail(w, 429, "操作过于频繁，请稍后重试")
		return
	}
	var in struct {
		Enrollment string `json:"enrollment"`
		Code       string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	a.mu.Lock()
	p, ok := a.pending[in.Enrollment]
	a.mu.Unlock()
	if !ok || time.Now().After(p.Expires) {
		fail(w, 400, "二维码已过期，请重新添加")
		return
	}
	step, ok := verifyTOTP(p.Secret, in.Code, -1)
	if !ok {
		fail(w, 401, "验证码不正确，请确认设备时间已同步")
		return
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	s := a.configTxSettings(tx)
	if count > 0 && !s.Public {
		fail(w, 403, "公开注册已关闭")
		return
	}
	u := user{ID: randomID(), Name: p.Name, Admin: count == 0}
	if !u.Admin {
		u.Expires = now() + int64(s.GuestHours)*3600
	}
	_, e = tx.Exec(`INSERT INTO users(id,name,secret,admin,expires,last_step) VALUES(?,?,?,?,?,?)`, u.ID, u.Name, seal(a.key, p.Secret), u.Admin, u.Expires, step)
	if e != nil {
		fail(w, 409, "账户名已使用，请重新注册")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	a.mu.Lock()
	delete(a.pending, in.Enrollment)
	a.mu.Unlock()
	reply(w, 201, a.issue(u))
}
func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		fail(w, 429, "尝试过多，请十分钟后再试")
		return
	}
	var in struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	var u user
	var enc string
	var last int64
	e := a.db.QueryRow(`SELECT id,name,admin,expires,secret,last_step FROM users WHERE name=?`, in.Name).Scan(&u.ID, &u.Name, &u.Admin, &u.Expires, &enc, &last)
	if e != nil {
		fail(w, 401, "账户或验证码不正确")
		return
	}
	if u.Expires > 0 && u.Expires <= now() {
		fail(w, 401, "此账户已过期，请联系管理员延长有效期")
		return
	}
	secret, e := unseal(a.key, enc)
	if e != nil {
		fail(w, 500, "账户密钥无法读取，请检查备份是否完整")
		return
	}
	step, ok := verifyTOTP(secret, in.Code, last)
	if !ok {
		fail(w, 401, "验证码不正确或已使用，请等待下一个验证码")
		return
	}
	result, e := a.db.Exec(`UPDATE users SET last_step=? WHERE id=? AND last_step<?`, step, u.ID, step)
	if e != nil {
		fail(w, 500, "登录失败")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		fail(w, 401, "验证码已使用，请等待下一组")
		return
	}
	reply(w, 200, a.issue(u))
}
