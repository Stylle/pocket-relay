package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func ageSession(a *app, token string, age time.Duration) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[token]
	s.LastActive = time.Now().Add(-age)
	a.sessions[token] = s
	return s.LastActive
}

func setIdle(t *testing.T, a *app, minutes int) {
	t.Helper()
	s := a.config()
	s.SessionIdleMinutes = minutes
	b, _ := json.Marshal(s)
	if _, err := a.db.Exec(`UPDATE config SET value=? WHERE id=1`, string(b)); err != nil {
		t.Fatal(err)
	}
}

func TestIdleSessionPollingAndExpiry(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	before := ageSession(a, token, 14*time.Minute)
	readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)
	readJSON(t, req(a, "GET", "/api/session", token, nil), 200)
	a.mu.Lock()
	after := a.sessions[token].LastActive
	a.mu.Unlock()
	if !before.Equal(after) {
		t.Fatal("passive requests renewed session")
	}
	ageSession(a, token, 16*time.Minute)
	readJSON(t, req(a, "POST", "/api/session/activity", token, nil), 401)
	readJSON(t, req(a, "GET", "/api/messages", token, nil), 401)
	a.mu.Lock()
	_, exists := a.sessions[token]
	a.mu.Unlock()
	if exists {
		t.Fatal("expired token not revoked")
	}
}

func TestSessionActivityAndNoIdleExpiry(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	ageSession(a, token, 14*time.Minute)
	result := readJSON(t, req(a, "POST", "/api/session/activity", token, nil), 200)
	if result["idle_minutes"].(float64) != 15 {
		t.Fatal(result)
	}
	a.mu.Lock()
	active := a.sessions[token].LastActive
	a.mu.Unlock()
	if time.Since(active) > time.Second {
		t.Fatal("activity not applied")
	}
	setIdle(t, a, 0)
	ageSession(a, token, 40*24*time.Hour)
	a.cleanup(context.Background())
	readJSON(t, req(a, "GET", "/api/session", token, nil), 200)
	readJSON(t, req(a, "POST", "/api/logout", token, nil), 200)
	readJSON(t, req(a, "GET", "/api/session", token, nil), 401)
}

func TestIdleDefaultsAndSettingsBounds(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	if a.config().SessionIdleMinutes != 15 {
		t.Fatal("new install default")
	}
	var legacy map[string]any
	b, _ := json.Marshal(a.config())
	json.Unmarshal(b, &legacy)
	delete(legacy, "session_idle_minutes")
	b, _ = json.Marshal(legacy)
	a.db.Exec(`UPDATE config SET value=? WHERE id=1`, string(b))
	if a.config().SessionIdleMinutes != 15 {
		t.Fatal("legacy config default")
	}
	for _, minutes := range []int{0, 1, 1440, 5256000, -1, 5256001} {
		s := a.config()
		s.SessionIdleMinutes = minutes
		expected := 200
		if minutes < 0 || minutes > 5256000 {
			expected = 400
		}
		readJSON(t, req(a, "PUT", "/api/admin", token, s), expected)
	}
}

func TestAccountExpiryStillAppliesWithoutIdleExpiry(t *testing.T) {
	a := testApp(t)
	token, id, _ := register(t, a, "owner")
	setIdle(t, a, 0)
	a.db.Exec(`UPDATE users SET expires=? WHERE id=?`, now()-1, id)
	readJSON(t, req(a, "GET", "/api/session", token, nil), 401)
}

func TestRelaxedPolicyDoesNotReviveExpiredSession(t *testing.T) {
	a := testApp(t)
	token, id, _ := register(t, a, "owner")
	u, err := a.findUser(id)
	if err != nil {
		t.Fatal(err)
	}
	other := a.issue(u)["token"].(string)
	ageSession(a, token, 16*time.Minute)
	s := a.config()
	s.SessionIdleMinutes = 0
	readJSON(t, req(a, "PUT", "/api/admin", other, s), 200)
	readJSON(t, req(a, "GET", "/api/session", token, nil), 401)
	readJSON(t, req(a, "GET", "/api/session", other, nil), 200)
}

func TestUnlimitedSessionsRemainMemoryBounded(t *testing.T) {
	a := testApp(t)
	_, id, _ := register(t, a, "owner")
	setIdle(t, a, 0)
	u, _ := a.findUser(id)
	for range 70 {
		a.issue(u)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) != 64 {
		t.Fatalf("unbounded sessions: %d", len(a.sessions))
	}
}

func TestIdleExpiryAlsoRejectsPreviouslyIssuedDownload(t *testing.T) {
	a := testApp(t)
	token, _, _ := register(t, a, "owner")
	readJSON(t, uploadTest(t, a, token, "sample.txt", []byte("test"), "0"), 201)
	messages := readJSON(t, req(a, "GET", "/api/messages", token, nil), 200)["messages"].([]any)
	file := messages[0].(map[string]any)["file_id"].(string)
	url := readJSON(t, req(a, "POST", "/api/files/"+file+"/ticket", token, nil), 200)["url"].(string)
	ageSession(a, token, 16*time.Minute)
	readJSON(t, req(a, "GET", url, "", nil), 401)
	// Pure expiry predicate is shared by auth, download and cleanup, including
	// exact-boundary and disabled-idle cases (zero absolute expiry is unlimited).
	at := time.Now()
	s := session{LastActive: at.Add(-15 * time.Minute)}
	if !sessionExpired(s, 15, at) || sessionExpired(s, 0, at) {
		t.Fatal("idle boundary")
	}
	s.Expires = at
	if !sessionExpired(s, 0, at) {
		t.Fatal("absolute account expiry")
	}
}
