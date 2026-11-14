package main

import "testing"

func TestRemovedTelegramTestEndpoints(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	for _, path := range []string{"/api/admin/telegram/test", "/api/admin/telegram/send-test"} {
		w := req(a, "POST", path, token, map[string]string{})
		if w.Code == 200 {
			t.Fatal("obsolete test endpoint still enabled")
		}
	}
}
