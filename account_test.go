package main

import "testing"

func TestAuthenticatorRotationRevokesOldCredentials(t *testing.T) {
	a := testApp(t)
	token, uid, old := register(t, a, "owner")
	// Isolate the proof step from the enrollment code's replay fence.
	a.db.Exec("UPDATE users SET last_step=0 WHERE id=?", uid)
	begin := readJSON(t, req(a, "POST", "/api/account/authenticator", token, map[string]string{"action": "begin", "code": totp(old, now()/30)}), 200)
	if w := req(a, "GET", "/api/session", token, nil); w.Code != 200 {
		t.Fatal("begin revoked old session")
	}
	if w := req(a, "POST", "/api/account/authenticator", token, map[string]string{"action": "confirm", "enrollment": begin["enrollment"].(string), "code": "bad"}); w.Code != 400 {
		t.Fatal("bad new code accepted")
	}
	// A remembered session must not survive the same commit.
	a.db.Exec("INSERT INTO remembered_sessions(id,user_id,created,expires) VALUES('fixture',?,?,?)", uid, now()*1000, now()+3600)
	fresh := begin["secret"].(string)
	readJSON(t, req(a, "POST", "/api/account/authenticator", token, map[string]string{"action": "confirm", "enrollment": begin["enrollment"].(string), "code": totp(fresh, now()/30)}), 200)
	if w := req(a, "GET", "/api/session", token, nil); w.Code != 401 {
		t.Fatal("old session survived")
	}
	var n int
	a.db.QueryRow("SELECT COUNT(*) FROM remembered_sessions WHERE user_id=?", uid).Scan(&n)
	if n != 0 {
		t.Fatal("remembered login survived")
	}
	if w := req(a, "POST", "/api/login", "", map[string]string{"name": "owner", "code": totp(old, now()/30+1)}); w.Code != 401 {
		t.Fatal("old authenticator survived")
	}
	readJSON(t, req(a, "POST", "/api/login", "", map[string]string{"name": "owner", "code": totp(fresh, now()/30+1)}), 200)
}
