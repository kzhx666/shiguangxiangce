package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// organizeMisplacedFiles 把「所在年份目录与拍摄时间不一致」的文件搬到正确目录，
// 并同步更新数据库里的 rel_path。只处理 status='active' 且文件确实存在的记录。
//
// 触发时机：服务启动后一次；用户点「重新扫描」时一次。
func (s *Server) organizeMisplacedFiles() (moved int) {
	rows, err := s.st.db.Query(`SELECT id, owner, rel_path, taken_at, name
		FROM assets WHERE status='active' AND taken_at > 0 AND IFNULL(folder,'')=''`)
	if err != nil {
		return 0
	}
	type item struct {
		id    int64
		owner string
		rel   string
		at    int64
		name  string
	}
	var list []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.owner, &it.rel, &it.at, &it.name) == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	if len(list) == 0 {
		return 0
	}

	for _, it := range list {
		when := time.Unix(it.at, 0)
		wantDir := filepath.Join(baseDirFor(it.owner),
			when.Format("2006年"), when.Format("2006-01月"))
		haveDir := filepath.Dir(it.rel)
		if filepath.Clean(haveDir) == filepath.Clean(wantDir) {
			continue // 已经在正确位置
		}
		src := filepath.Join(s.cfg.Root, it.rel)
		if _, err := os.Stat(src); err != nil {
			continue // 文件不在，交给缺失清理逻辑
		}
		if err := os.MkdirAll(filepath.Join(s.cfg.Root, wantDir), 0o755); err != nil {
			continue
		}
		dstRel := filepath.Join(wantDir, filepath.Base(it.rel))
		dst := filepath.Join(s.cfg.Root, dstRel)
		// 目标已存在就加后缀，绝不覆盖
		if _, err := os.Stat(dst); err == nil {
			base := strings.TrimSuffix(filepath.Base(it.rel), filepath.Ext(it.rel))
			ext := filepath.Ext(it.rel)
			for i := 1; i < 1000; i++ {
				cand := filepath.Join(wantDir, fmt.Sprintf("%s-%d%s", base, i, ext))
				if _, err := os.Stat(filepath.Join(s.cfg.Root, cand)); err != nil {
					dstRel = cand
					dst = filepath.Join(s.cfg.Root, cand)
					break
				}
			}
		}
		if err := os.Rename(src, dst); err != nil {
			log.Printf("organize: %s -> %s 失败: %v", it.rel, dstRel, err)
			continue
		}
		if _, err := s.st.db.Exec(`UPDATE assets SET rel_path=? WHERE id=?`, dstRel, it.id); err != nil {
			// 数据库写失败就把文件搬回去，保持一致性
			_ = os.Rename(dst, src)
			continue
		}
		moved++
		// 旧目录空了就删掉，避免磁盘上留下一堆 2257年 这种空壳
		oldDir := filepath.Join(s.cfg.Root, haveDir)
		_ = os.Remove(oldDir)                        // 目录非空会失败，正常
		_ = os.Remove(filepath.Dir(oldDir))          // 年份目录也试一下
	}
	if moved > 0 {
		log.Printf("organize: 把 %d 个文件整理到正确的年份目录", moved)
	}
	return moved
}

// pruneEmptyDirs 删掉空的年份/月份目录（比如整理完留下的「2257年/2257-05月」）。
func (s *Server) pruneEmptyDirs() (n int) {
	bases := []string{
		filepath.Join(s.cfg.Root, baseDirFor("lwm")),
		filepath.Join(s.cfg.Root, baseDirFor("cr")),
		filepath.Join(s.cfg.Root, "家庭共享"),
	}
	for _, base := range bases {
		years, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, y := range years {
			if !y.IsDir() || !strings.HasSuffix(y.Name(), "年") {
				continue
			}
			yPath := filepath.Join(base, y.Name())
			months, err := os.ReadDir(yPath)
			if err != nil {
				continue
			}
			for _, m := range months {
				if !m.IsDir() {
					continue
				}
				mPath := filepath.Join(yPath, m.Name())
				if entries, err := os.ReadDir(mPath); err == nil && len(entries) == 0 {
					if os.Remove(mPath) == nil {
						n++
					}
				}
			}
			if entries, err := os.ReadDir(yPath); err == nil && len(entries) == 0 {
				if os.Remove(yPath) == nil {
					n++
				}
			}
		}
	}
	if n > 0 {
		log.Printf("organize: 清掉 %d 个空年份目录", n)
	}
	return n
}
