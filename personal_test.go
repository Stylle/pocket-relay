package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestPersonalMigrationPreservesContentAndDisablesLegacyManagement(t *testing.T) {
	a := testApp(t)
	ownerToken, owner, _ := register(t, a, "owner")
	public(t, a, ownerToken)
	guestToken, guest, _ := register(t, a, "guest")
	readJSON(t, req(a, "POST", "/api/messages", guestToken, map[string]any{"text": "legacy fixture"}), 201)
	if e := a.enablePersonalMode(); e != nil {
		t.Fatal(e)
	}
	if a.config().Public || a.config().ActiveStore != "local" {
		t.Fatal("personal defaults")
	}
	var count int
	a.db.QueryRow("SELECT COUNT(*) FROM messages WHERE user_id=?", owner).Scan(&count)
	if count != 1 {
		t.Fatal("legacy content missing")
	}
	if _, e := a.findUser(guest); e == nil {
		t.Fatal("old account still authorized")
	}
	if req(a, "GET", "/api/messages", guestToken, nil).Code != 401 {
		t.Fatal("old session accepted")
	}
	for _, p := range []string{"/api/admin/storage", "/api/admin/storage/test"} {
		if req(a, http.MethodPost, p, ownerToken, map[string]string{}).Code != 404 {
			t.Fatal("remote store API retained")
		}
	}
	if req(a, http.MethodPut, "/api/admin/users/"+guest, ownerToken, map[string]int{"hours": 1}).Code != 404 {
		t.Fatal("user management retained")
	}
	got := req(a, "GET", "/api/admin", ownerToken, nil)
	if got.Code != 200 || strings.Contains(got.Body.String(), `"users"`) || strings.Contains(got.Body.String(), `"stores"`) {
		t.Fatal("legacy management exposed")
	}
	if req(a, "POST", "/api/enroll", "", map[string]string{"name": "another"}).Code != 403 {
		t.Fatal("registration open")
	}
	if e := a.enablePersonalMode(); e != nil {
		t.Fatal(e)
	}
	a.db.QueryRow("SELECT COUNT(*) FROM messages WHERE user_id=?", owner).Scan(&count)
	if count != 1 {
		t.Fatal("migration not idempotent")
	}
}
func TestPersonalFirstEnrollmentRemainsAvailable(t *testing.T) {
	a := testApp(t)
	if e := a.enablePersonalMode(); e != nil {
		t.Fatal(e)
	}
	register(t, a, "owner")
	if a.personalOwner() == "" {
		t.Fatal("missing owner")
	}
	if req(a, "POST", "/api/enroll", "", map[string]string{"name": "second"}).Code != 403 {
		t.Fatal("second enrollment allowed")
	}
}
