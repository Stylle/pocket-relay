package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (a *app) configTxSettings(tx *sql.Tx) settings {
	var raw string
	if e := tx.QueryRow(`SELECT value FROM config WHERE id=1`).Scan(&raw); e != nil {
		panic(e)
	}
	var s settings
	if e := json.Unmarshal([]byte(raw), &s); e != nil {
		panic(e)
	}
	return s
}
func validateSettings(s settings) bool {
	return s.GuestHours >= 1 && s.GuestHours <= 8760 && s.MaxMB >= 1 && s.MaxMB <= 10240 && s.QuotaMB >= s.MaxMB && s.QuotaMB <= 1048576 && len(s.DeviceTemplate) > 0 && len(s.DeviceTemplate) < 200 && strings.Contains(s.DeviceTemplate, "{device}")
}
func (a *app) adminGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	s := a.config()
	stores := []store{{ID: "local", Kind: "local", Name: "本地 data/blobs"}}
	rows, e := a.db.Query(`SELECT value FROM stores`)
	if e != nil {
		fail(w, 500, "读取配置失败")
		return
	}
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		p, e := unseal(a.key, raw)
		if e != nil {
			continue
		}
		var st store
		if json.Unmarshal([]byte(p), &st) == nil {
			st.Password = ""
			stores = append(stores, st)
		}
	}
	rows.Close()
	users := []user{}
	rows, e = a.db.Query(`SELECT id,name,admin,expires FROM users ORDER BY admin DESC,name`)
	if e != nil {
		fail(w, 500, "读取用户失败")
		return
	}
	for rows.Next() {
		var u user
		if rows.Scan(&u.ID, &u.Name, &u.Admin, &u.Expires) == nil {
			users = append(users, u)
		}
	}
	rows.Close()
	reply(w, 200, map[string]any{"settings": s, "stores": stores, "users": users})
}
func (a *app) adminSave(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	var s settings
	if !decode(w, r, &s) {
		return
	}
	if !validateSettings(s) {
		fail(w, 400, "配置超出范围；访客 1–8760 小时、文件 1–10240 MB、配额不小于文件大小，模板须包含 {device}")
		return
	}
	if _, e := a.getStore(s.ActiveStore); e != nil {
		fail(w, 400, "所选存储不存在")
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	b, _ := json.Marshal(s)
	if _, e := a.db.Exec(`UPDATE config SET value=? WHERE id=1`, string(b)); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *app) addStore(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	var s store
	if !decode(w, r, &s) {
		return
	}
	if e := validateStore(s); e != nil {
		fail(w, 400, e.Error())
		return
	}
	s.ID = randomID()
	if s.Name == "" {
		s.Name = s.Kind
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if e := a.storeProbe(ctx, s); e != nil {
		fail(w, 502, "存储读写测试失败，请检查地址、凭据和权限；SMB 需要镜像内置的 rclone")
		return
	}
	b, _ := json.Marshal(s)
	if _, e := a.db.Exec(`INSERT INTO stores(id,value) VALUES(?,?)`, s.ID, seal(a.key, string(b))); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	reply(w, 201, map[string]string{"id": s.ID})
}
func (a *app) testStore(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	var s store
	if !decode(w, r, &s) {
		return
	}
	if e := validateStore(s); e != nil {
		fail(w, 400, e.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if e := a.storeProbe(ctx, s); e != nil {
		fail(w, 502, "测试失败，请检查连接、账户和目录权限")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *app) updateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	var in struct {
		Hours int `json:"hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Hours < 1 || in.Hours > 8760 {
		fail(w, 400, "有效期应为 1–8760 小时")
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	result, e := a.db.Exec(`UPDATE users SET expires=? WHERE id=? AND admin=0`, now()+int64(in.Hours)*3600, r.PathValue("id"))
	if e != nil {
		fail(w, 500, "修改失败")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		fail(w, 404, "用户不存在，或为永久管理员")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
