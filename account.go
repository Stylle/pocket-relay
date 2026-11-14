package main

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	qrcode "github.com/skip2/go-qrcode"
	"net/http"
	"net/url"
	"time"
)

type authenticatorRotation struct {
	ID, Old, Secret string
	Expires         time.Time
}

func (a *app) rotateAuthenticator(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		fail(w, 429, "操作过于频繁，请稍后重试")
		return
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	u, ok := a.auth(w, r)
	if !ok {
		return
	}
	var in struct {
		Action     string `json:"action"`
		Code       string `json:"code"`
		Enrollment string `json:"enrollment"`
	}
	if !decode(w, r, &in) {
		return
	}
	if a.rotations == nil {
		a.rotations = map[string]authenticatorRotation{}
	}
	for id, p := range a.rotations {
		if time.Now().After(p.Expires) {
			delete(a.rotations, id)
		}
	}
	if in.Action == "cancel" {
		delete(a.rotations, u.ID)
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if in.Action == "begin" {
		var enc string
		var last int64
		if a.db.QueryRow("SELECT secret,last_step FROM users WHERE id=?", u.ID).Scan(&enc, &last) != nil {
			fail(w, 500, "账户读取失败")
			return
		}
		old, err := unseal(a.key, enc)
		if err != nil {
			fail(w, 500, "账户密钥无法读取")
			return
		}
		step, valid := verifyTOTP(old, in.Code, last)
		if !valid {
			fail(w, 403, "当前验证码无效或已使用，请等待下一组")
			return
		}
		b := make([]byte, 20)
		if _, err = rand.Read(b); err != nil {
			fail(w, 500, "生成失败")
			return
		}
		secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
		uri := "otpauth://totp/" + url.PathEscape("Pocket Relay:"+u.Name) + "?" + url.Values{"secret": {secret}, "issuer": {"Pocket Relay"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
		png, err := qrcode.Encode(uri, qrcode.Medium, 256)
		if err != nil {
			fail(w, 500, "二维码生成失败")
			return
		}
		if len(a.rotations) >= 64 {
			if _, exists := a.rotations[u.ID]; !exists {
				fail(w, 429, "请稍后重试")
				return
			}
		}
		result, err := a.db.Exec("UPDATE users SET last_step=? WHERE id=? AND secret=? AND last_step<?", step, u.ID, enc, step)
		if err != nil {
			fail(w, 500, "验证失败")
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			fail(w, 409, "账户已变化，请重试")
			return
		}
		p := authenticatorRotation{randomID(), enc, secret, time.Now().Add(10 * time.Minute)}
		a.rotations[u.ID] = p
		reply(w, 200, map[string]string{"enrollment": p.ID, "secret": secret, "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
		return
	}
	p, exists := a.rotations[u.ID]
	if in.Action != "confirm" || !exists || p.ID != in.Enrollment || time.Now().After(p.Expires) {
		fail(w, 400, "绑定已过期，请重新开始")
		return
	}
	step, valid := verifyTOTP(p.Secret, in.Code, -1)
	if !valid {
		fail(w, 400, "新验证器验证码不正确")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE users SET secret=?,last_step=? WHERE id=? AND secret=?", seal(a.key, p.Secret), step, u.ID, p.Old)
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		fail(w, 409, "账户已变化，请重新开始")
		return
	}
	if _, err = tx.Exec("DELETE FROM remembered_sessions WHERE user_id=?", u.ID); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "保存失败，原验证器未改变")
		return
	}
	a.mu.Lock()
	for token, s := range a.sessions {
		if s.User == u.ID {
			delete(a.sessions, token)
		}
	}
	for id, t := range a.tickets {
		if t.User == u.ID {
			delete(a.tickets, id)
		}
	}
	a.mu.Unlock()
	delete(a.rotations, u.ID)
	setRememberCookie(w, r, "", time.Unix(1, 0))
	reply(w, 200, map[string]bool{"ok": true, "reauthenticate": true})
}
