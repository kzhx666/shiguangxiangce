package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

var peopleMu sync.Mutex

// Only unassigned faces are clustered. Existing names and manual corrections survive each run.
func (s *Server) rebuildPeople() int {
	peopleMu.Lock()
	defer peopleMu.Unlock()
	rows, e := s.st.db.Query(`SELECT f.id,f.person,f.emb,a.owner FROM faces f JOIN assets a ON a.id=f.asset WHERE a.status='active' AND a.secret=0 ORDER BY f.id`)
	if e != nil {
		return 0
	}
	type face struct {
		id, p int64
		v     []float32
		owner string
	}
	var pending []face
	type group struct {
		p     int64
		v     []float32
		owner string
	}
	var groups []group
	seen := map[int64]bool{}
	for rows.Next() {
		var f face
		var blob []byte
		if rows.Scan(&f.id, &f.p, &blob, &f.owner) != nil {
			continue
		}
		f.v = bytesToVec(blob)
		if len(f.v) == 0 {
			continue
		}
		if f.p == 0 {
			pending = append(pending, f)
		} else if f.p > 0 && !seen[f.p] {
			seen[f.p] = true
			groups = append(groups, group{f.p, f.v, f.owner})
		}
	}
	rows.Close()
	tx, e := s.st.db.Begin()
	if e != nil {
		return 0
	}
	defer tx.Rollback()
	for _, f := range pending {
		var pid int64
		best := float32(faceSimThreshold)
		for _, g := range groups {
			if g.owner == f.owner {
				sim := dot(g.v, f.v)
				if sim >= best {
					best = sim
					pid = g.p
				}
			}
		}
		if pid == 0 {
			r, e := tx.Exec(`INSERT INTO people(name,cover,cnt,created) VALUES('',?,0,?)`, f.id, time.Now().Unix())
			if e != nil {
				return 0
			}
			pid, _ = r.LastInsertId()
			groups = append(groups, group{pid, f.v, f.owner})
		}
		if _, e = tx.Exec(`UPDATE faces SET person=? WHERE id=?`, pid, f.id); e != nil {
			return 0
		}
	}
	if _, e = tx.Exec(`UPDATE people SET cnt=(SELECT COUNT(*) FROM faces WHERE person=people.id)`); e != nil {
		return 0
	}
	if tx.Commit() != nil {
		return 0
	}
	return len(groups)
}

func (s *Server) mayEditPerson(r *http.Request, id int64) bool {
	rows, e := s.st.db.Query(`SELECT DISTINCT a.owner FROM faces f JOIN assets a ON a.id=f.asset WHERE f.person=?`, id)
	if e != nil {
		return false
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var o string
		if rows.Scan(&o) != nil || !canSee(userOf(r), o) {
			return false
		}
		found = true
	}
	return found
}
func (s *Server) hPeopleMerge(w http.ResponseWriter, r *http.Request) {
	peopleMu.Lock()
	defer peopleMu.Unlock()
	var req struct{ From, To int64 }
	if jsonDecode(r, &req) != nil || req.From == req.To || req.From <= 0 || req.To <= 0 {
		http.Error(w, "invalid people", 400)
		return
	}
	if !s.mayEditPerson(r, req.From) || !s.mayEditPerson(r, req.To) {
		http.Error(w, "forbidden", 403)
		return
	}
	// Never merge across storage spaces; that would expose private names in shared views.
	var owners int
	if s.st.db.QueryRow(`SELECT COUNT(DISTINCT a.owner) FROM faces f JOIN assets a ON a.id=f.asset WHERE f.person IN (?,?)`, req.From, req.To).Scan(&owners) != nil || owners != 1 {
		http.Error(w, "只能合并同一空间的人物", 400)
		return
	}
	tx, e := s.st.db.Begin()
	if e != nil {
		http.Error(w, "database", 500)
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE faces SET person=? WHERE person=?`, req.To, req.From); e == nil {
		_, e = tx.Exec(`DELETE FROM people WHERE id=?`, req.From)
	}
	if e == nil {
		_, e = tx.Exec(`UPDATE people SET cnt=(SELECT COUNT(*) FROM faces WHERE person=?) WHERE id=?`, req.To, req.To)
	}
	if e != nil || tx.Commit() != nil {
		http.Error(w, "database", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
func (s *Server) hPersonFaces(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !s.mayEditPerson(r, id) {
		http.Error(w, "forbidden", 403)
		return
	}
	rows, e := s.st.db.Query(`SELECT f.id,f.asset FROM faces f JOIN assets a ON a.id=f.asset WHERE person=? AND a.status='active' AND a.secret=0 ORDER BY f.id LIMIT 500`, id)
	if e != nil {
		http.Error(w, "database", 500)
		return
	}
	defer rows.Close()
	out := []map[string]int64{}
	for rows.Next() {
		var f, a int64
		if rows.Scan(&f, &a) == nil {
			out = append(out, map[string]int64{"id": f, "asset": a})
		}
	}
	writeJSON(w, map[string]any{"faces": out})
}
func (s *Server) hPeopleSplit(w http.ResponseWriter, r *http.Request) {
	peopleMu.Lock()
	defer peopleMu.Unlock()
	var req struct {
		Person int64
		Faces  []int64
		Name   string
	}
	if jsonDecode(r, &req) != nil || len(req.Faces) == 0 || len(req.Faces) > 500 || len(req.Name) > 120 {
		http.Error(w, "invalid selection", 400)
		return
	}
	if !s.mayEditPerson(r, req.Person) {
		http.Error(w, "forbidden", 403)
		return
	}
	tx, e := s.st.db.Begin()
	if e != nil {
		http.Error(w, "database", 500)
		return
	}
	defer tx.Rollback()
	for _, id := range req.Faces {
		var p int64
		if tx.QueryRow(`SELECT person FROM faces WHERE id=?`, id).Scan(&p) != nil || p != req.Person {
			http.Error(w, "invalid face", 400)
			return
		}
	}
	res, e := tx.Exec(`INSERT INTO people(name,cover,cnt,created) VALUES(?,?,?,?)`, req.Name, req.Faces[0], len(req.Faces), time.Now().Unix())
	if e != nil {
		http.Error(w, "database", 500)
		return
	}
	pid, _ := res.LastInsertId()
	for _, id := range req.Faces {
		if _, e = tx.Exec(`UPDATE faces SET person=? WHERE id=?`, pid, id); e != nil {
			http.Error(w, "database", 500)
			return
		}
	}
	_, e = tx.Exec(`UPDATE people SET cnt=(SELECT COUNT(*) FROM faces WHERE person=?),cover=COALESCE((SELECT MIN(id) FROM faces WHERE person=?),0) WHERE id=?`, req.Person, req.Person, req.Person)
	if e != nil || tx.Commit() != nil {
		http.Error(w, "database", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "person": pid})
}
