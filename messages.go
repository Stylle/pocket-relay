package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type message struct {
	Concealed bool   `json:"concealed"`
	ID        string `json:"id"`
	Device    string `json:"device"`
	Text      string `json:"text"`
	Created   int64  `json:"created"`
	Expires   int64  `json:"expires"`
	FileID    string `json:"file_id"`
	Name      string `json:"name"`
	MIME      string `json:"mime"`
	Size      int64  `json:"size"`
}

func (a *app) listMessages(w http.ResponseWriter, r *http.Request) {
	u, ok := a.auth(w, r)
	if !ok {
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before == 0 {
		before = 1 << 62
	}
	beforeID := r.URL.Query().Get("before_id")
	if beforeID == "" {
		beforeID = "ffffffffffffffffffffffffffffffff"
	}
	q := r.URL.Query()
	text := strings.TrimSpace(q.Get("q"))
	if len(text) > 1024 {
		fail(w, 400, "搜索文字不能超过 1024 字节")
		return
	}
	from, to := int64(0), int64(1<<62)
	for name, target := range map[string]*int64{"from": &from, "to": &to} {
		if raw := q.Get(name); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 {
				fail(w, 400, "搜索时间不正确")
				return
			}
			*target = value
		}
	}
	if from >= to {
		fail(w, 400, "开始时间必须早于结束时间")
		return
	}
	switch q.Get("scope") {
	case "", "all":
	case "current":
		a.mu.Lock()
		s := a.sessions[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		a.mu.Unlock()
		if s.Started.IsZero() {
			fail(w, 401, "请重新验证")
			return
		}
		from = max(from, s.Started.UnixMilli())
	default:
		fail(w, 400, "消息范围不正确")
		return
	}
	filesOnly := q.Get("files") == "1"
	// instr is a literal substring match: %, _ and SQL syntax are not wildcards.
	// Search runs against the database, not just the most recent page in memory.
	rows, e := a.db.Query(`SELECT m.id,m.device,CASE WHEN m.concealed=1 THEN '' ELSE m.text END,m.created,m.expires,m.file_id,COALESCE(f.name,''),COALESCE(f.mime,''),COALESCE(f.size,0),m.concealed FROM messages m LEFT JOIN files f ON f.id=m.file_id WHERE m.user_id=? AND (m.expires=0 OR m.expires>?) AND m.created>=? AND m.created<? AND instr(lower(m.text),lower(?))>0 AND (?=0 OR m.file_id!='') AND (m.created<? OR (m.created=? AND m.id<?)) ORDER BY m.created DESC,m.id DESC LIMIT 101`, u.ID, now(), from, to, text, filesOnly, before, before, beforeID)
	if e != nil {
		fail(w, 500, "读取消息失败")
		return
	}
	out := []message{}
	for rows.Next() {
		var m message
		if e = rows.Scan(&m.ID, &m.Device, &m.Text, &m.Created, &m.Expires, &m.FileID, &m.Name, &m.MIME, &m.Size, &m.Concealed); e != nil {
			rows.Close()
			fail(w, 500, "读取消息失败")
			return
		}
		out = append(out, m)
	}
	if rows.Err() != nil {
		rows.Close()
		fail(w, 500, "读取消息失败")
		return
	}
	rows.Close()
	hasMore := len(out) > 100
	if hasMore {
		out = out[:100]
	}
	config := a.config()
	payload, e := json.Marshal(map[string]any{"messages": out, "has_more": hasMore, "auto_load_images": config.AutoLoadImages, "user": u, "device_template": config.DeviceTemplate, "max_file_mb": config.MaxMB})
	if e != nil {
		fail(w, 500, "读取消息失败")
		return
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(payload))
	w.Header().Set("ETag", etag)
	w.Header().Set("Vary", "Authorization")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(payload)
}

// Concealed text is fetched only on an explicit reveal; it is never included in
// list responses. This is display privacy, not end-to-end or at-rest encryption.
func (a *app) messageText(w http.ResponseWriter, r *http.Request) {
	u, ok := a.auth(w, r)
	if !ok {
		return
	}
	var text string
	if err := a.db.QueryRow(`SELECT text FROM messages WHERE id=? AND user_id=? AND (expires=0 OR expires>?)`, r.PathValue("id"), u.ID, now()).Scan(&text); err != nil {
		fail(w, 404, "消息不存在或已过期")
		return
	}
	reply(w, 200, map[string]string{"text": text})
}
func expiry(ttl int64) (int64, error) {
	if ttl == 0 {
		return 0, nil
	}
	if ttl < 60 || ttl > 365*86400 {
		return 0, fmt.Errorf("invalid ttl")
	}
	return now() + ttl, nil
}
func (a *app) sendMessage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.auth(w, r)
	if !ok {
		return
	}
	select {
	case a.transfers <- struct{}{}:
		defer func() { <-a.transfers }()
	default:
		fail(w, 429, "当前有多个文件正在传输，请稍后重试")
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	s := a.config()
	tg, tgRevision, tgError := a.telegramConfig()
	if tgError != nil {
		fail(w, 500, "机器人配置无法读取，请检查完整 data 备份")
		return
	}
	var text, device, fileID, name, kind, storeID, digest string
	var expires, size int64
	var concealed bool
	temp := ""
	stored := false
	committed := false
	defer func() {
		if temp != "" {
			os.Remove(temp)
		}
		if stored && !committed {
			a.db.Exec(`UPDATE files SET deleted=1 WHERE id=?`, fileID)
			if st, e := a.getStore(storeID); e == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if a.remove(ctx, st, fileID) == nil {
					a.db.Exec(`DELETE FROM files WHERE id=? AND deleted=1`, fileID)
				}
			}
		}
	}()
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, s.MaxMB<<20+128<<10)
		reader, e := r.MultipartReader()
		if e != nil {
			fail(w, 400, "上传格式不正确")
			return
		}
		var ttl int64
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				fail(w, 413, "上传中断或文件超过大小限制")
				return
			}
			if part.FormName() == "file" {
				if fileID != "" {
					part.Close()
					fail(w, 400, "每次仅上传一个文件，可批量选择自动排队")
					return
				}
				fileID = randomID()
				name = filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
				if len(name) > 255 || strings.ContainsAny(name, "\x00\r\n") || name == "." {
					part.Close()
					fail(w, 400, "文件名不正确")
					return
				}
				f, e := os.CreateTemp(filepath.Join(a.dir, "tmp"), "upload-")
				if e != nil {
					part.Close()
					fail(w, 507, "磁盘无法写入")
					return
				}
				temp = f.Name()
				h := sha256.New()
				head := make([]byte, 512)
				n, _ := io.ReadFull(part, head)
				head = head[:n]
				kind = http.DetectContentType(head)
				_, e = f.Write(head)
				if e == nil {
					h.Write(head)
					var rest int64
					rest, e = io.Copy(io.MultiWriter(f, h), io.LimitReader(part, (s.MaxMB<<20)-int64(n)+1))
					size = int64(n) + rest
				}
				closeErr := f.Close()
				if e != nil || closeErr != nil || size > s.MaxMB<<20 {
					part.Close()
					fail(w, 413, "上传失败或超过单文件限制")
					return
				}
				digest = hex.EncodeToString(h.Sum(nil))
			} else {
				b, e := io.ReadAll(io.LimitReader(part, 16385))
				if e != nil || len(b) > 16384 {
					part.Close()
					fail(w, 400, "文字内容过长")
					return
				}
				switch part.FormName() {
				case "concealed":
					concealed, e = strconv.ParseBool(string(b))
					if e != nil {
						part.Close()
						fail(w, 400, "正文遮挡选项不正确")
						return
					}
				case "text":
					text = string(b)
				case "device":
					device = string(b)
				case "ttl":
					ttl, e = strconv.ParseInt(string(b), 10, 64)
					if e != nil {
						part.Close()
						fail(w, 400, "保存时间不正确")
						return
					}
				}
			}
			part.Close()
		}
		var e2 error
		expires, e2 = expiry(ttl)
		if e2 != nil {
			fail(w, 400, "保存时间应为永久或 1 分钟至 365 天")
			return
		}
	} else {
		var in struct {
			Concealed bool   `json:"concealed"`
			Text      string `json:"text"`
			Device    string `json:"device"`
			TTL       int64  `json:"ttl"`
		}
		if !decode(w, r, &in) {
			return
		}
		text = in.Text
		concealed = in.Concealed
		device = in.Device
		var e error
		expires, e = expiry(in.TTL)
		if e != nil {
			fail(w, 400, "保存时间不正确")
			return
		}
	}
	if len(text) > 16384 || strings.TrimSpace(text) == "" && fileID == "" {
		fail(w, 400, "请填写文字或选择文件")
		return
	}
	if fileID != "" {
		storeID = s.ActiveStore
		st, e := a.getStore(storeID)
		if e != nil {
			fail(w, 503, "存储配置不可用")
			return
		}
		if _, e = a.db.Exec(`INSERT INTO files(id,user_id,store_id,name,mime,size,sha256,deleted) VALUES(?,?,?,?,?,?,?,-1)`, fileID, u.ID, storeID, name, kind, size, digest); e != nil {
			fail(w, 500, "无法登记文件传输，请重试")
			return
		}
		stored = true
		if e = a.put(r.Context(), st, fileID, temp); e != nil {
			fail(w, 502, "存储写入失败，请检查存储连接及权限")
			return
		}
	}
	// Recheck expiry after a long upload; a guest cannot publish after expiry.
	if _, e := a.findUser(u.ID); e != nil {
		fail(w, 401, "账户已过期")
		return
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback()
	var used int64
	if e = tx.QueryRow(`SELECT COALESCE(SUM(size),0) FROM files WHERE user_id=? AND deleted=0`, u.ID).Scan(&used); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	if used+size > s.QuotaMB<<20 {
		fail(w, 413, "账户存储空间不足，请删除部分文件或联系管理员")
		return
	}
	if fileID != "" {
		_, e = tx.Exec(`UPDATE files SET deleted=0 WHERE id=? AND user_id=? AND deleted=-1`, fileID, u.ID)
		if e != nil {
			fail(w, 500, "保存失败")
			return
		}
	}
	id := randomID()
	_, e = tx.Exec(`INSERT INTO messages(id,user_id,device,text,created,expires,file_id,concealed) VALUES(?,?,?,?,?,?,?,?)`, id, u.ID, a.deviceTemplate(s, r, device), text, time.Now().UnixMilli(), expires, fileID, concealed && text != "")
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	if tg.Enabled && tg.Send && tg.UserID == u.ID && (!concealed || tg.IncludeConcealed) {
		if _, e = tx.Exec(`INSERT INTO telegram_outbox(message_id,revision) SELECT ?,? WHERE EXISTS(SELECT 1 FROM telegram WHERE id=1 AND revision=?)`, id, tgRevision, tgRevision); e != nil {
			fail(w, 500, "保存发送队列失败")
			return
		}
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	committed = true
	reply(w, 201, map[string]string{"id": id})
}
func (a *app) deviceTemplate(s settings, r *http.Request, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "未命名设备"
	}
	runes := []rune(name)
	if len(runes) > 40 {
		name = string(runes[:40])
	}
	return strings.NewReplacer("{device}", name, "{ip}", maskedIP(r.RemoteAddr)).Replace(s.DeviceTemplate)
}
func (a *app) deleteMessage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.auth(w, r)
	if !ok {
		return
	}
	a.gate.RLock()
	defer a.gate.RUnlock()
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "删除失败")
		return
	}
	defer tx.Rollback()
	var id string
	if e = tx.QueryRow(`SELECT file_id FROM messages WHERE id=? AND user_id=?`, r.PathValue("id"), u.ID).Scan(&id); e != nil {
		fail(w, 404, "消息不存在")
		return
	}
	if id != "" {
		if _, e = tx.Exec(`UPDATE files SET deleted=1 WHERE id=?`, id); e != nil {
			fail(w, 500, "删除失败")
			return
		}
	}
	if _, e = tx.Exec(`DELETE FROM messages WHERE id=? AND user_id=?`, r.PathValue("id"), u.ID); e != nil {
		fail(w, 500, "删除失败")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "删除失败")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *app) fileAllowed(userID, fileID string) bool {
	var n int
	e := a.db.QueryRow(`SELECT COUNT(*) FROM files f JOIN messages m ON m.file_id=f.id WHERE f.id=? AND f.user_id=? AND f.deleted=0 AND (m.expires=0 OR m.expires>?)`, fileID, userID, now()).Scan(&n)
	return e == nil && n > 0
}
func (a *app) fileTicket(w http.ResponseWriter, r *http.Request) {
	u, ok := a.auth(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !a.fileAllowed(u.ID, id) {
		fail(w, 404, "文件不存在或已过期")
		return
	}
	token := randomID() + randomID()
	a.mu.Lock()
	if len(a.tickets) >= 10000 {
		a.mu.Unlock()
		fail(w, 429, "下载请求过多，请稍后重试")
		return
	}
	a.tickets[token] = ticket{u.ID, id, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), time.Now().Add(5 * time.Minute)}
	a.mu.Unlock()
	reply(w, 200, map[string]string{"url": "/file/" + id + "?ticket=" + token})
}
func (a *app) download(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("ticket")
	a.mu.Lock()
	t, ok := a.tickets[token]
	s, sessionOK := a.sessions[t.Session]
	a.mu.Unlock()
	id := r.PathValue("id")
	if !ok || !sessionOK || sessionExpired(s, a.config().SessionIdleMinutes, time.Now()) || (s.RememberID != "" && !a.rememberValid(s.RememberID, s.User)) || t.File != id || time.Now().After(t.Expires) {
		fail(w, 401, "下载链接已过期，请重新点击下载")
		return
	}
	if _, e := a.findUser(t.User); e != nil || !a.fileAllowed(t.User, id) {
		fail(w, 404, "文件不存在或已过期")
		return
	}
	var storeID, name, kind string
	var size int64
	if e := a.db.QueryRow(`SELECT store_id,name,mime,size FROM files WHERE id=? AND user_id=?`, id, t.User).Scan(&storeID, &name, &kind, &size); e != nil {
		fail(w, 404, "文件不存在")
		return
	}
	st, e := a.getStore(storeID)
	if e != nil {
		fail(w, 503, "存储不可用")
		return
	}
	filename := filepath.Join(a.dir, "blobs", id)
	if st.Kind != "local" {
		select {
		case a.transfers <- struct{}{}:
			defer func() { <-a.transfers }()
		default:
			fail(w, 429, "文件传输繁忙，请稍后重试")
			return
		}
		f, e := os.CreateTemp(filepath.Join(a.dir, "tmp"), "download-")
		if e != nil {
			fail(w, 507, "临时磁盘不可用")
			return
		}
		filename = f.Name()
		defer os.Remove(filename)
		e = a.fetch(r.Context(), st, id, f, size)
		f.Close()
		if e != nil {
			fail(w, 502, "远程文件读取失败")
			return
		}
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" && (safeImage(kind) || strings.HasPrefix(kind, "video/") || strings.HasPrefix(kind, "audio/")) {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeFile(w, r, filename)
}
func safeImage(kind string) bool {
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif":
		return true
	}
	return false
}
func (a *app) janitor(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.cleanup(ctx)
		}
	}
}
func (a *app) cleanup(ctx context.Context) {
	a.db.ExecContext(ctx, `DELETE FROM remembered_sessions WHERE expires<=?`, now())
	idleMinutes := a.config().SessionIdleMinutes
	a.mu.Lock()
	for k, v := range a.sessions {
		if sessionExpired(v, idleMinutes, time.Now()) {
			delete(a.sessions, k)
		}
	}
	for k, v := range a.pending {
		if time.Now().After(v.Expires) {
			delete(a.pending, k)
		}
	}
	for k, v := range a.tickets {
		if time.Now().After(v.Expires) {
			delete(a.tickets, k)
		}
	}
	for k, v := range a.attempts {
		if time.Now().After(v.Until) {
			delete(a.attempts, k)
		}
	}
	a.mu.Unlock()
	a.gate.RLock()
	defer a.gate.RUnlock()
	tx, e := a.db.Begin()
	if e != nil {
		return
	}
	if _, e = tx.Exec(`UPDATE files SET deleted=1 WHERE id IN (SELECT file_id FROM messages WHERE expires>0 AND expires<=?)`, now()); e != nil {
		tx.Rollback()
		return
	}
	if _, e = tx.Exec(`DELETE FROM messages WHERE expires>0 AND expires<=?`, now()); e != nil {
		tx.Rollback()
		return
	}
	if e = tx.Commit(); e != nil {
		return
	}
	rows, e := a.db.Query(`SELECT id,store_id FROM files WHERE deleted=1 LIMIT 100`)
	if e != nil {
		return
	}
	type item struct{ id, store string }
	items := []item{}
	for rows.Next() {
		var x item
		if rows.Scan(&x.id, &x.store) == nil {
			items = append(items, x)
		}
	}
	rows.Close()
	for _, x := range items {
		if ctx.Err() != nil {
			return
		}
		s, e := a.getStore(x.store)
		if e != nil {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		e = a.remove(c, s, x.id)
		cancel()
		if e == nil {
			a.db.Exec(`DELETE FROM files WHERE id=? AND deleted=1`, x.id)
		}
	}
}
