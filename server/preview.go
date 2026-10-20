package main

// 超大图的「补预览」支持：
// 路由器解不了上亿像素的原图（一张要吃 200MB+ 内存），
// 让手机把图缩成 1280px 传回来当封面，服务器以后就用这张小图生成缩略图。

import (
	"net/http"
	"os"
)

// previewNeed 返回需要客户端生成预览的资产：
// 照片，且（原图 > 25MB 或 像素 > 4000 万），且还没有封面。
func (s *Server) previewNeed(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	rows, err := s.st.db.Query(`SELECT id, name, size, w, h FROM assets
		WHERE status='active' AND kind='photo'
		  AND (size > ? OR (w > 0 AND h > 0 AND w*h > ?))
		ORDER BY size DESC LIMIT 2000`,
		25<<20, 40000000)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	seen := map[int64]bool{}
	for rows.Next() {
		var id, size int64
		var name string
		var w4, h4 int
		if rows.Scan(&id, &name, &size, &w4, &h4) != nil {
			continue
		}
		a, gerr := s.st.Get(id)
		if gerr != nil || !canSee(user, a.Owner) || seen[id] {
			continue
		}
		// 已经有封面（小预览）的跳过
		if pp := s.posterPath(a); pp != "" {
			if fi, err := os.Stat(pp); err == nil && fi.Size() > 0 {
				continue
			}
		}
		seen[id] = true
		out = append(out, map[string]any{
			"id": id, "name": name, "size": size, "w": w4, "h": h4,
			"rel_path": a.RelPath,
		})
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

// videosNeedPoster 返回还没有封面的视频（路由器没有视频解码器，只能靠客户端截帧）。
func (s *Server) videosNeedPoster(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	rows, err := s.st.db.Query(`SELECT id, name, size FROM assets
		WHERE status='active' AND kind='video' ORDER BY size ASC LIMIT 2000`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, size int64
		var name string
		if rows.Scan(&id, &name, &size) != nil {
			continue
		}
		a, gerr := s.st.Get(id)
		if gerr != nil || !canSee(user, a.Owner) {
			continue
		}
		pp := s.posterPath(a)
		if pp != "" {
			if fi, err := os.Stat(pp); err == nil && fi.Size() > 0 {
				continue
			}
		}
		out = append(out, map[string]any{"id": id, "name": name, "size": size})
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}