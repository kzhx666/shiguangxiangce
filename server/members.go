package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var memberUploadMu sync.Mutex
var memberShared = map[string]bool{}
var memberDefs []ownerDef
var memberName = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,23}$`)

func ownerDefs() []ownerDef {
	spaceMu.RLock()
	defer spaceMu.RUnlock()
	if len(memberDefs) > 0 {
		return append([]ownerDef(nil), memberDefs...)
	}
	return append([]ownerDef(nil), owners...)
}
func allOwnerIDs() []string {
	out := []string{}
	for _, o := range ownerDefs() {
		out = append(out, o.Owner)
	}
	return out
}
func searchDate(v string, end bool) int64 {
	t, e := time.Parse("2006-01-02", v)
	if e != nil {
		return 0
	}
	if end {
		t = t.AddDate(0, 0, 1)
	}
	return t.Unix()
}
func (s *Server) ensureMembers() error {
	_, _ = s.st.db.Exec(`ALTER TABLE users ADD COLUMN shared INTEGER NOT NULL DEFAULT 1`)
	_, err := s.st.db.Exec(`CREATE TABLE IF NOT EXISTS spaces(name TEXT PRIMARY KEY,label TEXT NOT NULL,quota INTEGER NOT NULL DEFAULT 0)`)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if _, err = s.st.db.Exec(`INSERT OR IGNORE INTO spaces(name,label) VALUES(?,?)`, o.Owner, o.Label); err != nil {
			return err
		}
	}
	return s.refreshMembers()
}
func (s *Server) refreshMembers() error {
	rows, e := s.st.db.Query(`SELECT name,label FROM spaces ORDER BY name`)
	if e != nil {
		return e
	}
	defs := []ownerDef{}
	for rows.Next() {
		var n, l string
		if e = rows.Scan(&n, &l); e != nil {
			rows.Close()
			return e
		}
		dir := n
		if n == "family" {
			dir = "家庭共享"
		}
		defs = append(defs, ownerDef{dir, n, l})
	}
	rows.Close()
	rows, e = s.st.db.Query(`SELECT name,shared FROM users`)
	if e != nil {
		return e
	}
	shared := map[string]bool{}
	for rows.Next() {
		var n string
		var b bool
		if e = rows.Scan(&n, &b); e != nil {
			rows.Close()
			return e
		}
		shared[n] = b
	}
	rows.Close()
	spaceMu.Lock()
	memberDefs = defs
	memberShared = shared
	spaceMu.Unlock()
	return nil
}
func (s *Server) quotaAllows(owner string, size int64) bool {
	var quota, used, reserved int64
	if s.st.db.QueryRow(`SELECT quota FROM spaces WHERE name=?`, owner).Scan(&quota) != nil {
		return false
	}
	if quota == 0 {
		return true
	}
	if s.st.db.QueryRow(`SELECT COALESCE(SUM(size),0) FROM assets WHERE owner=?`, owner).Scan(&used) != nil {
		return false
	}
	if s.st.db.QueryRow(`SELECT COALESCE(SUM(size),0) FROM uploads WHERE owner=?`, owner).Scan(&reserved) != nil {
		return false
	}
	return size <= quota-used-reserved
}
func (s *Server) hMembers(w http.ResponseWriter, r *http.Request) {
	if userOf(r) != "admin" {
		http.Error(w, "administrator required", 403)
		return
	}
	rows, e := s.st.db.Query(`SELECT u.name,CASE WHEN u.space='' THEN u.name ELSE u.space END,u.shared,COALESCE(s.quota,0),COALESCE(s.label,u.name) FROM users u LEFT JOIN spaces s ON s.name=CASE WHEN u.space='' THEN u.name ELSE u.space END ORDER BY u.name`)
	if e != nil {
		http.Error(w, "database", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var n, sp, l string
		var shared bool
		var quota int64
		if rows.Scan(&n, &sp, &shared, &quota, &l) == nil {
			out = append(out, map[string]any{"name": n, "space": sp, "shared": shared, "quota": quota, "label": l})
		}
	}
	writeJSON(w, map[string]any{"members": out})
}
func (s *Server) hMemberSave(w http.ResponseWriter, r *http.Request) {
	if userOf(r) != "admin" {
		http.Error(w, "administrator required", 403)
		return
	}
	var req struct {
		Name, Password, Label string
		Shared                bool
		Quota                 int64
	}
	if jsonDecode(r, &req) != nil || !memberName.MatchString(req.Name) || req.Name == "admin" || req.Name == "family" || req.Quota < 0 {
		http.Error(w, "invalid member", 400)
		return
	}
	if req.Label == "" {
		req.Label = req.Name + " 的相册"
	}
	if len(req.Label) > 120 {
		http.Error(w, "label too long", 400)
		return
	}
	if req.Password != "" && len(req.Password) < 8 {
		http.Error(w, "密码至少 8 位", 400)
		return
	}
	memberUploadMu.Lock()
	defer memberUploadMu.Unlock()
	var n int
	var sp string
	if e := s.st.db.QueryRow(`SELECT COUNT(*) FROM users WHERE name=?`, req.Name).Scan(&n); e != nil {
		http.Error(w, "database", 500)
		return
	}
	if n == 0 && req.Password == "" {
		http.Error(w, "password required", 400)
		return
	}
	sp = req.Name
	if n > 0 {
		_ = s.st.db.QueryRow(`SELECT CASE WHEN space='' THEN name ELSE space END FROM users WHERE name=?`, req.Name).Scan(&sp)
	}
	// Existing members keep their old directory mapping; creating a member never grants another private space.
	if e := os.MkdirAll(filepath.Join(s.cfg.Root, baseDirFor(sp)), 0755); e != nil {
		http.Error(w, "无法创建空间目录", 500)
		return
	}
	tx, e := s.st.db.Begin()
	if e != nil {
		http.Error(w, "database", 500)
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO spaces(name,label,quota) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET label=excluded.label,quota=excluded.quota`, sp, req.Label, req.Quota)
	if e == nil {
		if n == 0 {
			_, e = tx.Exec(`INSERT INTO users(name,pw,space,shared,created) VALUES(?,?,?,?,?)`, req.Name, hashPassword(req.Password), sp, req.Shared, time.Now().Unix())
		} else {
			_, e = tx.Exec(`UPDATE users SET shared=? WHERE name=?`, req.Shared, req.Name)
		}
	}
	if e == nil && n > 0 && req.Password != "" {
		_, e = tx.Exec(`UPDATE users SET pw=? WHERE name=?`, hashPassword(req.Password), req.Name)
		if e == nil {
			_, e = tx.Exec(`DELETE FROM sessions WHERE user=?`, req.Name)
		}
	}
	if e != nil {
		http.Error(w, "database", 500)
		return
	}
	if tx.Commit() != nil {
		http.Error(w, "database", 500)
		return
	}
	s.loadSpaces()
	if s.refreshMembers() != nil {
		http.Error(w, "refresh failed", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
