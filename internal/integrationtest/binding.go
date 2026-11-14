package integrationtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Binding codes are one-use, short lived, and fenced to saved credentials.
type Binding struct {
	mu              sync.Mutex
	digest          [32]byte
	owner, identity string
	expires         time.Time
}

func (b *Binding) Create(owner, identity string) (string, error) {
	raw := make([]byte, 20)
	if _, e := rand.Read(raw); e != nil {
		return "", e
	}
	code := hex.EncodeToString(raw)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.digest = sha256.Sum256([]byte(code))
	b.owner = owner
	b.identity = identity
	b.expires = time.Now().Add(5 * time.Minute)
	return "/bind " + code, nil
}
func (b *Binding) Clear() { b.mu.Lock(); defer b.mu.Unlock(); b.owner = ""; b.digest = [32]byte{} }
func (b *Binding) Pending(identity string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.owner != "" && b.identity == identity && time.Now().Before(b.expires)
}
func (b *Binding) Consume(code, identity string, commit func(string) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	hash := sha256.Sum256([]byte(code))
	if b.owner == "" || b.identity != identity || !time.Now().Before(b.expires) || subtle.ConstantTimeCompare(hash[:], b.digest[:]) != 1 {
		return errors.New("绑定码无效或已过期")
	}
	if e := commit(b.owner); e != nil {
		return e
	}
	b.owner = ""
	b.digest = [32]byte{}
	return nil
}

type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func (t Telegram) Menu(ctx context.Context, token string, chat int64, commands []Command) error {
	scope := map[string]any{"type": "default"}
	payload := map[string]any{"menu_button": map[string]string{"type": "commands"}}
	if chat > 0 {
		scope = map[string]any{"type": "chat", "chat_id": chat}
		payload["chat_id"] = chat
	}
	if e := t.Call(ctx, token, "setMyCommands", map[string]any{"commands": commands, "scope": scope}, nil); e != nil {
		return e
	}
	return t.Call(ctx, token, "setChatMenuButton", payload, nil)
}
