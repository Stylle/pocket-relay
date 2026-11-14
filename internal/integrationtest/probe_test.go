package integrationtest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fixtureToken = "123456:abcdefghijklmnopqrstuvwxy0123456789"

func TestDraftAndSecrets(t *testing.T) {
	if Secret("  pass  ", "old") != "  pass  " || Secret("", "old") != "old" {
		t.Fatal("secret whitespace or fallback changed")
	}
	for _, raw := range []string{`[]`, `{"x":1} {}`, `{"x":`} {
		if _, e := Read(strings.NewReader(raw)); e == nil {
			t.Fatal("invalid draft accepted")
		}
	}
	v, e := Read(strings.NewReader(`{"x":""}`))
	if e != nil {
		t.Fatal(e)
	}
	if s, _ := String(v, "x", "old"); s != "" {
		t.Fatal("explicit empty was ignored")
	}
	if s, _ := String(v, "missing", "old"); s != "old" {
		t.Fatal("missing field did not retain saved value")
	}
}
func TestTelegramErrorsAndRedirectsDoNotLeakSecrets(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprintf(w, `{"ok":false,"error_code":403,"description":%q}`, fixtureToken)
			return
		}
		http.Redirect(w, r, "/leak", 302)
	}))
	defer srv.Close()
	c := Telegram{HTTP: srv.Client(), BaseURL: srv.URL}
	_, e := c.Check(context.Background(), fixtureToken)
	if e == nil || strings.Contains(e.Error(), fixtureToken) || !strings.Contains(e.Error(), "/start") {
		t.Fatal("unsafe or unhelpful Telegram error")
	}
	e = c.Send(context.Background(), fixtureToken, "123", "test")
	if e == nil || calls != 2 {
		t.Fatal("redirect followed or reported success")
	}
}
func TestWebhookUsesDraftAndSanitizesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Draft") != "fresh" {
			t.Error("wrong draft header")
		}
		w.WriteHeader(503)
		fmt.Fprint(w, fixtureToken)
	}))
	defer srv.Close()
	status, e := Webhook(context.Background(), srv.Client(), "POST", srv.URL, map[string]string{"X-Draft": "fresh"}, []byte(`{}`))
	if status != 503 || e == nil || strings.Contains(e.Error(), fixtureToken) {
		t.Fatal("Webhook failure was not safely surfaced")
	}
}
