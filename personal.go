package main

import (
	"database/sql"
	"encoding/json"
)

// Legacy user/store rows remain readable for old backup and attachment formats.
// Production only exposes a single owner's login and local uploads.
func (a *app) personalOwner() string {
	var id string
	a.db.QueryRow("SELECT id FROM users WHERE admin=1 ORDER BY rowid LIMIT 1").Scan(&id)
	return id
}
func (a *app) enablePersonalMode() error {
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var owner string
	e = tx.QueryRow("SELECT id FROM users WHERE admin=1 ORDER BY rowid LIMIT 1").Scan(&owner)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if owner != "" {
		for _, table := range []string{"messages", "files"} {
			if _, e = tx.Exec("UPDATE "+table+" SET user_id=? WHERE user_id!=?", owner, owner); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("DELETE FROM remembered_sessions WHERE user_id!=?", owner); e != nil {
			return e
		}
		var raw string
		if e = tx.QueryRow("SELECT value FROM telegram WHERE id=1").Scan(&raw); e == nil {
			plain, err := unseal(a.key, raw)
			if err != nil {
				return err
			}
			var cfg telegramConfig
			if err = json.Unmarshal([]byte(plain), &cfg); err != nil {
				return err
			}
			cfg.UserID = owner
			b, _ := json.Marshal(cfg)
			if _, err = tx.Exec("UPDATE telegram SET value=? WHERE id=1", seal(a.key, string(b))); err != nil {
				return err
			}
		} else if e != sql.ErrNoRows {
			return e
		}
	}
	s := a.configTxSettings(tx)
	s.Public = false
	s.GuestHours = 1
	s.ActiveStore = "local"
	b, _ := json.Marshal(s)
	if _, e = tx.Exec("UPDATE config SET value=? WHERE id=1", string(b)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.personal = true
	return nil
}
