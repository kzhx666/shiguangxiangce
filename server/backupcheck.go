package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// hBackupCheck 批量检查"这些文件服务器上还有没有"。
// 手机端备份前先问一次，避免本地凭据过期导致「明明删了却说已备份」。
type backupCheckReq struct {
	Owner string `json:"owner"`
	Items []struct {
		N string `json:"n"` // 文件名
		S int64  `json:"s"` // 字节数
	} `json:"items"`
}

func (s *Server) hBackupCheck(w http.ResponseWriter, r *http.Request) {
	var req backupCheckReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	owner := req.Owner
	if owner == "" {
		owner = userOf(r)
	}
	if !canSee(userOf(r), owner) {
		owner = userOf(r)
	}

	// 服务器上现存的「文件名|大小」集合（active 才算）
	exist := map[string]bool{}
	rows, err := s.st.db.Query(`SELECT name, size FROM assets WHERE owner=? AND status='active'`, owner)
	if err == nil {
		for rows.Next() {
			var n string
			var sz int64
			if rows.Scan(&n, &sz) == nil {
				exist[n+"|"+strconv.FormatInt(sz, 10)] = true
			}
		}
		rows.Close()
	}

	// 同一份内容历史上传时用过别的文件名，也要算“已备份”
	if nrows, nerr := s.st.db.Query(`SELECT n.name, n.size FROM asset_names n
		JOIN assets a ON a.id = n.asset_id
		WHERE a.owner=?`, owner); nerr == nil {
		for nrows.Next() {
			var n string
			var sz int64
			if nrows.Scan(&n, &sz) == nil {
				exist[n+"|"+strconv.FormatInt(sz, 10)] = true
			}
		}
		nrows.Close()
	}

	missing := []int{}
	have := []string{}
	for i, it := range req.Items {
		key := it.N + "|" + strconv.FormatInt(it.S, 10)
		if !exist[key] {
			missing = append(missing, i)
		} else {
			have = append(have, key)
		}
	}
	writeJSON(w, map[string]any{
		"missing": missing,
		"have":    have,
		"total":   len(req.Items),
		"owner":   owner,
		"exist":   len(exist),
	})
}
