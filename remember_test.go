package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rememberedRequest(a *app, cookie *http.Cookie, path, token string, custom bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if custom {
		r.Header.Set("X-Relay-Session", "1")
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}

func rememberLogin(t *testing.T, a *app) (string, *http.Cookie, string) {
	t.Helper()
	_, id, secret := register(t, a, "owner")
	w := req(a, "POST", "/api/login", "", map[string]any{"name": "owner", "code": totp(secret, now()/30+1), "remember": true})
	result := readJSON(t, w, 200)
	if result["remembered"] != true || result["idle_minutes"].(float64) != 0 {
		t.Fatal("remember preference not applied")
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == rememberCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge < 604795 || cookie.MaxAge > 604800 {
		t.Fatal("invalid persistent cookie policy")
	}
	return result["token"].(string), cookie, id
}

func TestRememberedLoginSurvivesRestartWithoutSlidingExpiry(t *testing.T) {
	a := testApp(t)
	token, cookie, id := rememberLogin(t, a)
	var stored string
	var expires int64
	if err := a.db.QueryRow(`SELECT id,expires FROM remembered_sessions WHERE user_id=?`, id).Scan(&stored, &expires); err != nil {
		t.Fatal(err)
	}
	if stored == cookie.Value || stored != rememberHash(cookie.Value) {
		t.Fatal("raw credential stored")
	}
	ageSession(a, token, 2*24*time.Hour)
	readJSON(t, req(a, "GET", "/api/session", token, nil), 200)
	readJSON(t, req(a, "POST", "/api/session/activity", token, nil), 200)
	readJSON(t, rememberedRequest(a, cookie, "/api/session/restore", "", false), 403)
	a.db.Close()
	restarted, err := openApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.db.Close()
	w := rememberedRequest(restarted, cookie, "/api/session/restore", "", true)
	result := readJSON(t, w, 200)
	if result["remembered"] != true || int64(result["expires_at"].(float64)) != expires*1000 {
		t.Fatal("restoration extended expiry")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("restoration must not refresh cookie")
	}
	readJSON(t, req(restarted, "GET", "/api/messages", result["token"].(string), nil), 200)
}

func TestRememberedLogoutRevokesCookieAndOtherRestoredTokens(t *testing.T) {
	a := testApp(t)
	token, cookie, _ := rememberLogin(t, a)
	other := readJSON(t, rememberedRequest(a, cookie, "/api/session/restore", "", true), 200)["token"].(string)
	readJSON(t, rememberedRequest(a, cookie, "/api/logout", token, false), 403)
	w := rememberedRequest(a, cookie, "/api/logout", token, true)
	readJSON(t, w, 200)
	if len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("cookie not removed")
	}
	readJSON(t, req(a, "GET", "/api/session", other, nil), 401)
	readJSON(t, rememberedRequest(a, cookie, "/api/session/restore", "", true), 401)
}

func TestRememberedExpiryAccountLimitAndRevocation(t *testing.T) {
	a := testApp(t)
	token, cookie, id := rememberLogin(t, a)
	a.db.Exec(`UPDATE remembered_sessions SET expires=?`, now()-1)
	readJSON(t, req(a, "GET", "/api/session", token, nil), 401)
	readJSON(t, rememberedRequest(a, cookie, "/api/session/restore", "", true), 401)
	a.db.Exec(`UPDATE remembered_sessions SET expires=?`, now()+3600)
	a.db.Exec(`UPDATE users SET expires=? WHERE id=?`, now()-1, id)
	readJSON(t, rememberedRequest(a, cookie, "/api/session/restore", "", true), 401)
	a.db.Exec(`UPDATE users SET expires=? WHERE id=?`, now()+3600, id)
	u, _ := a.findUser(id)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "https://example.test/api/login", nil)
	r.Header.Set("Origin", "https://example.test")
	a.finishLogin(w, r, u, true, 200)
	result := readJSON(t, w, 200)
	if int64(result["expires_at"].(float64)) != u.Expires*1000 {
		t.Fatal("temporary user expiry bypassed")
	}
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie not secure")
	}
	s := session{RememberID: "remembered", LastActive: time.Now().Add(-24 * time.Hour), Expires: time.Now().Add(-time.Second)}
	if !sessionExpired(s, 0, time.Now()) {
		t.Fatal("absolute remembered expiry ignored")
	}
}

func TestUncheckingRememberClearsPreviousBrowserGrant(t *testing.T) {
	a := testApp(t)
	_, cookie, id := rememberLogin(t, a)
	u, _ := a.findUser(id)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(""))
	r.AddCookie(cookie)
	a.finishLogin(w, r, u, false, 200)
	result := readJSON(t, w, 200)
	if result["remembered"] != false || result["idle_minutes"].(float64) != 15 {
		t.Fatal("ordinary policy not restored")
	}
	readJSON(t, rememberedRequest(a, cookie, "/api/session/restore", "", true), 401)
}
