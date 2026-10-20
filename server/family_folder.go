package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// hFamilyFolder 把某个文件夹里的全部照片一次性推送到家庭共享空间。
// 和单张推送走同一套逻辑（硬链接 + 复制兜底），只是范围换成了整个文件夹。
func (s *Server) hFamilyFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, `{"error":"empty name"}`, 400)
		return
	}
	clause, args := ownerIn(s.visibleOwners(r, ""))
	q := `SELECT id FROM assets WHERE status='active' AND folder=? AND secret=0` + clause
	rows, err := s.st.db.Query(q, append([]any{name}, args...)...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	pushed := 0
	for _, id := range ids {
		if s.pushOne(id, name) {
			pushed++
		}
	}
	writeJSON(w, map[string]any{"ok": true, "pushed": pushed, "total": len(ids)})
}
