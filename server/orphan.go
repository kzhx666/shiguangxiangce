package main

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// purgeMissingAssets 处理「磁盘上文件已经不存在」的记录。
//
// 规则：
//   - 根目录本身读不到（盘没挂好）-> 整轮跳过，什么都不做；
//   - 第一次发现文件不在 -> 打 missing 标记（时间轴立刻不再显示它）；
//   - 连续 10 分钟都不在 -> 删记录 + 缩略图 + 海报缓存。
//
// 用户直接从硬盘删文件后，最多 10 分钟缓存自动清干净；
// 又能扛住一次临时的挂载抖动。
func (s *Server) purgeMissingAssets() (removed int) {
	if _, err := os.Stat(s.cfg.Root); err != nil {
		log.Printf("purge missing: 根目录不可读(%v)，跳过本轮", err)
		return 0
	}

	type row struct {
		id    int64
		rel   string
		size  int64
		mtime int64
		since int64
		flag  int
	}
	rows, err := s.st.db.Query(
		`SELECT id, rel_path, size, mtime, missing_since, missing FROM assets WHERE status='active'`)
	if err != nil {
		return 0
	}
	var all []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.rel, &r.size, &r.mtime, &r.since, &r.flag) == nil {
			all = append(all, r)
		}
	}
	rows.Close()
	if len(all) == 0 {
		return 0
	}

	now := time.Now().Unix()
	for _, r := range all {
		if _, statErr := os.Stat(filepath.Join(s.cfg.Root, r.rel)); statErr == nil {
			if r.flag != 0 {
				_, _ = s.st.db.Exec(`UPDATE assets SET missing=0, missing_since=0 WHERE id=?`, r.id)
			}
			continue
		}
		if r.flag == 0 {
			_, _ = s.st.db.Exec(`UPDATE assets SET missing=1, missing_since=? WHERE id=?`, now, r.id)
			continue
		}
		if now-r.since < 600 {
			continue
		}
		_, _ = s.st.db.Exec(`DELETE FROM assets WHERE id=?`, r.id)
		for _, size := range []string{"m", "t", "p"} {
			_ = os.Remove(s.thumbKey(r.id, r.size, r.mtime, size))
		}
		sub := pad2(r.id)
		_ = os.Remove(filepath.Join(s.cfg.CacheDir, "poster", sub,
			itoa64(r.id)+"_"+itoa64(r.size)+"_"+itoa64(r.mtime)+".jpg"))
		removed++
	}
	if removed > 0 {
		log.Printf("purge missing: 清理 %d 条（磁盘上已不存在的文件及缓存）", removed)
	}
	return removed
}

func pad2(v int64) string {
	n := ((v % 100) + 100) % 100
	if n < 10 {
		return "0" + itoa64(n)
	}
	return itoa64(n)
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
