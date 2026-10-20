package main

import (
	"os"

	"sync"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessCookie = "lumina_sess"
	sessTTL    = 30 * 24 * time.Hour
	pbkdfIter  = 120000
)

func (s *Server) ensureAuthTables() error {
	_, err := s.st.db.Exec(`
CREATE TABLE IF NOT EXISTS users(
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  name    TEXT UNIQUE NOT NULL,
  pw      TEXT NOT NULL,
  space   TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions(
  token   TEXT PRIMARY KEY,
  user    TEXT NOT NULL,
  created INTEGER NOT NULL,
  expires INTEGER NOT NULL
);`)
	return err
}
func (s *Server) migrateUserSpace() {
	_, _ = s.st.db.Exec(`ALTER TABLE users ADD COLUMN space TEXT NOT NULL DEFAULT ''`)
}

// pbkdf2SHA256 手写实现，避免为一个函数引入 x/crypto 依赖。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hLen := prf.Size()
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	buf := make([]byte, 4)
	for b := 1; b <= blocks; b++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf, uint32(b))
		prf.Write(buf)
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	sum := pbkdf2SHA256([]byte(pw), salt, pbkdfIter, 32)
	return hex.EncodeToString(salt) + "$" + hex.EncodeToString(sum)
}

func checkPassword(stored, pw string) bool {
	parts := strings.SplitN(stored, "$", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(pw), salt, pbkdfIter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Server) userCount() int {
	var n int
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

func (s *Server) hSetupState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"needs_setup": s.userCount() == 0})
}

func (s *Server) hSetup(w http.ResponseWriter, r *http.Request) {
	if s.userCount() > 0 {
		http.Error(w, "already configured", 409)
		return
	}
	var req struct {
		Users []struct {
			Name string `json:"name"`
			Pass string `json:"password"`
		} `json:"users"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	for _, u := range req.Users {
		name := strings.TrimSpace(u.Name)
		if !validUser(name) || len(u.Pass) < 4 {
			http.Error(w, "invalid user or password too short", 400)
			return
		}
		if _, err := s.st.db.Exec(`INSERT INTO users(name,pw,created) VALUES(?,?,?)`,
			name, hashPassword(u.Pass), time.Now().Unix()); err != nil {
			http.Error(w, "insert failed", 500)
			return
		}
	}
	if s.userCount() == 0 {
		http.Error(w, "empty setup", 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) hLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Pass string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	var pw string
	err := s.st.db.QueryRow(`SELECT pw FROM users WHERE name=?`, req.Name).Scan(&pw)
	if err != nil || !checkPassword(pw, req.Pass) {
		time.Sleep(350 * time.Millisecond) // 轻微延迟，压制暴力尝试
		http.Error(w, `{"error":"bad credentials"}`, 401)
		return
	}
	tok := randomHex(24)
	now := time.Now()
	_, _ = s.st.db.Exec(`INSERT INTO sessions(token,user,created,expires) VALUES(?,?,?,?)`,
		tok, req.Name, now.Unix(), now.Add(sessTTL).Unix())
	http.SetCookie(w, &http.Cookie{
		Name: sessCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessTTL.Seconds()),
	})
	writeJSON(w, map[string]any{"ok": true, "user": req.Name})
}

func (s *Server) hLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessCookie); err == nil {
		_, _ = s.st.db.Exec(`DELETE FROM sessions WHERE token=?`, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) hMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"user": r.Header.Get("X-Lumina-User")})
}

func (s *Server) sessionUser(tok string) (string, bool) {
	var user string
	var exp int64
	if err := s.st.db.QueryRow(`SELECT user,expires FROM sessions WHERE token=?`, tok).Scan(&user, &exp); err != nil {
		return "", false
	}
	if time.Now().Unix() > exp {
		_, _ = s.st.db.Exec(`DELETE FROM sessions WHERE token=?`, tok)
		return "", false
	}
	return user, true
}

func (s *Server) isPublic(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/" || p == "/index.html" || p == "/favicon.ico":
		return true
	case p == "/app.js" || p == "/land.json" || p == "/favicon.png":
		return true
		return true
	case strings.HasPrefix(p, "/s/"):
		return true
	case r.Method == "POST" && p == "/api/login":
		return true
	case r.Method == "GET" && p == "/api/health":
		return true
	case r.Method == "GET" && p == "/api/setup-state":
		return true
	case r.Method == "POST" && p == "/api/register":
		return true
	case r.Method == "POST" && p == "/api/setup":
		return true
	}
	return false
}

func (s *Server) authMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isPublic(r) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(sessCookie); err == nil {
			if u, ok := s.sessionUser(c.Value); ok {
				r.Header.Set("X-Lumina-User", u)
				next.ServeHTTP(w, r)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/t/") || strings.HasPrefix(r.URL.Path, "/m/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
	})
}

// 定期清理过期会话
func (s *Server) gcSessions() {
	_, _ = s.st.db.Exec(`DELETE FROM sessions WHERE expires < ?`, time.Now().Unix())
}

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func (s *Server) hPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if len(req.New) < 4 {
		http.Error(w, "密码至少 4 位", 400)
		return
	}
	user := r.Header.Get("X-Lumina-User")
	var pw string
	if err := s.st.db.QueryRow(`SELECT pw FROM users WHERE name=?`, user).Scan(&pw); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if !checkPassword(pw, req.Old) {
		time.Sleep(300 * time.Millisecond)
		http.Error(w, "原密码不正确", 401)
		return
	}
	if _, err := s.st.db.Exec(`UPDATE users SET pw=? WHERE name=?`, hashPassword(req.New), user); err != nil {
		http.Error(w, "update failed", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
func validUser(name string) bool {
	return name == "lwm" || name == "cr" || name == "admin"
}

// ensureAdmin 保证管理员账号存在（可查看全部空间）。
// 首次创建时使用默认密码，登录后可在右上角修改。
func (s *Server) ensureAdmin() {
	var n int
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM users WHERE name='admin'`).Scan(&n)
	if n > 0 {
		return
	}
	_, err := s.st.db.Exec(`INSERT INTO users(name,pw,created) VALUES(?,?,?)`,
		"admin", hashPassword(firstAdminPassword()), time.Now().Unix())
	if err == nil {
		log.Printf("已创建管理员账号 admin（初始密码见 LUMINA_ADMIN_PW 环境变量）")
	}
}
// ---------- 账号空间映射 ----------

var (
	spaceMu sync.RWMutex
	spaceOfUser = map[string]string{}
)

func (s *Server) loadSpaces() {
	rows, err := s.st.db.Query(`SELECT name, space FROM users`)
	if err != nil {
		return
	}
	defer rows.Close()
	spaceMu.Lock()
	defer spaceMu.Unlock()
	for rows.Next() {
		var n, sp string
		if rows.Scan(&n, &sp) == nil {
			spaceOfUser[n] = sp
		}
	}
}

func spaceForUser(name string) string {
	spaceMu.RLock()
	sp := spaceOfUser[name]
	spaceMu.RUnlock()
	if sp != "" {
		return sp
	}
	if name == "lwm" || name == "cr" || name == "family" || name == "admin" {
		return name
	}
	return "family"
}

// hRegister 注册新账号，可选绑定到某个空间。
func (s *Server) hRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Pass  string `json:"password"`
		Space string `json:"space"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) < 2 || len(name) > 24 {
		http.Error(w, `{"error":"账号长度需 2-24 位"}`, 400)
		return
	}
	for _, ch := range name {
		if !(ch == '_' || ch == '-' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
			http.Error(w, `{"error":"账号只能包含字母数字下划线"}`, 400)
			return
		}
	}
	if len(req.Pass) < 4 {
		http.Error(w, `{"error":"密码至少 4 位"}`, 400)
		return
	}
	space := req.Space
	if space != "lwm" && space != "cr" && space != "family" {
		space = "family"
	}
	var n int
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM users WHERE name=?`, name).Scan(&n)
	if n > 0 {
		http.Error(w, `{"error":"这个账号已存在"}`, 409)
		return
	}
	if _, err := s.st.db.Exec(`INSERT INTO users(name,pw,space,created) VALUES(?,?,?,?)`,
		name, hashPassword(req.Pass), space, time.Now().Unix()); err != nil {
		http.Error(w, `{"error":"创建失败"}`, 500)
		return
	}
	spaceMu.Lock()
	spaceOfUser[name] = space
	spaceMu.Unlock()
	log.Printf("register: %s -> space %s", name, space)
	writeJSON(w, map[string]any{"ok": true, "user": name, "space": space})
}

// firstAdminPassword 首次建库时的管理员初始密码。
// 优先读环境变量，没配就随机生成并打印一次（不再写死在代码里）。
func firstAdminPassword() string {
	if v := os.Getenv("LUMINA_ADMIN_PW"); v != "" {
		return v
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "changeme-" + time.Now().Format("150405")
	}
	pw := hex.EncodeToString(buf)
	log.Printf("首次启动，管理员初始密码：%s（请尽快修改）", pw)
	return pw
}
