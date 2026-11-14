package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const rememberCookie = "pr-remember"

func rememberHash(token string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(token))) }

func rememberCookieValue(r *http.Request) string {
	c, err := r.Cookie(rememberCookie)
	if err != nil || len(c.Value) != 64 {
		return ""
	}
	for _, ch := range c.Value {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return ""
		}
	}
	return c.Value
}

func setRememberCookie(w http.ResponseWriter, r *http.Request, value string, until time.Time) {
	maxAge := -1
	if value != "" {
		maxAge = max(1, int(time.Until(until).Seconds()))
	}
	secure := requestHTTPS(r)
	http.SetCookie(w, &http.Cookie{Name: rememberCookie, Value: value, Path: "/api/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: until})
}

func (a *app) rememberValid(id, userID string) bool {
	var expires int64
	return a.db.QueryRow(`SELECT expires FROM remembered_sessions WHERE id=? AND user_id=?`, id, userID).Scan(&expires) == nil && expires > now()
}

func (a *app) revokeRemembered(id string) error {
	if id == "" {
		return nil
	}
	if _, err := a.db.Exec(`DELETE FROM remembered_sessions WHERE id=?`, id); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, s := range a.sessions {
		if s.RememberID == id {
			delete(a.sessions, token)
		}
	}
	return nil
}

func (a *app) finishLogin(w http.ResponseWriter, r *http.Request, u user, remember bool, status int) {
	if old := rememberCookieValue(r); old != "" {
		if err := a.revokeRemembered(rememberHash(old)); err != nil {
			fail(w, 500, "撤销旧登录失败，请重试")
			return
		}
	}
	if !remember {
		setRememberCookie(w, r, "", time.Unix(1, 0))
		reply(w, status, a.issue(u))
		return
	}
	until := time.Now().Add(7 * 24 * time.Hour).Truncate(time.Second)
	if u.Expires > 0 && time.Unix(u.Expires, 0).Before(until) {
		until = time.Unix(u.Expires, 0)
	}
	credential := randomID() + randomID()
	id := rememberHash(credential)
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "保存登录失败")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM remembered_sessions WHERE expires<=?`, now()); err == nil {
		_, err = tx.Exec(`INSERT INTO remembered_sessions(id,user_id,created,expires) VALUES(?,?,?,?)`, id, u.ID, time.Now().UnixMilli(), until.Unix())
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM remembered_sessions WHERE user_id=? AND id NOT IN(SELECT id FROM remembered_sessions WHERE user_id=? ORDER BY created DESC,rowid DESC LIMIT 32)`, u.ID, u.ID)
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM remembered_sessions WHERE id NOT IN(SELECT id FROM remembered_sessions ORDER BY created DESC,rowid DESC LIMIT 2048)`)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "保存登录失败")
		return
	}
	setRememberCookie(w, r, credential, until)
	reply(w, status, a.issueSession(u, id, until))
}

func (a *app) restoreRemembered(w http.ResponseWriter, r *http.Request) {
	// Custom headers cannot be sent by cross-site forms or simple fetches.
	if r.Header.Get("X-Relay-Session") != "1" {
		fail(w, 403, "请求来源不正确")
		return
	}
	credential := rememberCookieValue(r)
	if credential == "" {
		fail(w, 401, "没有记住的登录")
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	id := rememberHash(credential)
	var userID string
	var expires int64
	if a.db.QueryRow(`SELECT user_id,expires FROM remembered_sessions WHERE id=?`, id).Scan(&userID, &expires) != nil || expires <= now() {
		setRememberCookie(w, r, "", time.Unix(1, 0))
		fail(w, 401, "记住的登录已到期，请重新验证")
		return
	}
	u, err := a.findUser(userID)
	if err != nil {
		setRememberCookie(w, r, "", time.Unix(1, 0))
		fail(w, 401, "账户已过期或不存在")
		return
	}
	// Do not extend either the database expiry or browser cookie on restoration.
	reply(w, 200, a.issueSession(u, id, time.Unix(expires, 0)))
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	credential := rememberCookieValue(r)
	if credential != "" && r.Header.Get("X-Relay-Session") != "1" {
		fail(w, 403, "请求来源不正确")
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	a.mu.Lock()
	s := a.sessions[token]
	delete(a.sessions, token)
	a.mu.Unlock()
	if err := a.revokeRemembered(s.RememberID); err != nil {
		fail(w, 500, "退出失败，请重试")
		return
	}
	if credential != "" {
		if err := a.revokeRemembered(rememberHash(credential)); err != nil {
			fail(w, 500, "退出失败，请重试")
			return
		}
	}
	setRememberCookie(w, r, "", time.Unix(1, 0))
	reply(w, 200, map[string]bool{"ok": true})
}
