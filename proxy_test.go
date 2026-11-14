package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRememberedCookieProxyHTTPS(t *testing.T) {
	r := httptest.NewRequest("POST", "http://relay.test/api/login", nil)
	r.RemoteAddr = "192.0.2.1:123"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	setRememberCookie(w, r, "fixture", time.Now().Add(time.Hour))
	if w.Result().Cookies()[0].Secure {
		t.Fatal("untrusted proxy changed secure flag")
	}
	t.Setenv("PUBLIC_HTTPS", "1")
	w = httptest.NewRecorder()
	setRememberCookie(w, r, "fixture", time.Now().Add(time.Hour))
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("explicit HTTPS ignored")
	}
	t.Setenv("PUBLIC_HTTPS", "")
	t.Setenv("TRUSTED_PROXIES", "192.0.2.0/24")
	if !requestHTTPS(r) {
		t.Fatal("trusted HTTPS ignored")
	}
	t.Setenv("TRUSTED_PROXIES", "")
	r.Header.Set("Origin", "https://evil.test")
	if requestHTTPS(r) {
		t.Fatal("foreign origin classified HTTPS")
	}
	r.Header.Set("Origin", "https://relay.test")
	if !requestHTTPS(r) {
		t.Fatal("same-host TLS termination regressed")
	}
}
