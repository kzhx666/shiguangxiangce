package main

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// saneTime 把明显错误的拍摄时间纠正回来。
//
// 触发场景：微信/QQ 导出的图片、某些相机写坏的 EXIF，会出现 2257 年这种时间。
// 规则：年份早于 1990 或晚于「当前年份+1」就判为无效，退回文件修改时间；
// 文件修改时间也不合理时，用当前时间。
func saneTime(t time.Time) time.Time {
	now := time.Now()
	min := time.Date(1990, 1, 1, 0, 0, 0, 0, time.Local)
	max := now.AddDate(1, 0, 0)
	if !t.IsZero() && t.After(min) && t.Before(max) {
		return t
	}
	return now
}

// fixBadTimes 启动时跑一次：修正数据库里已有的离谱时间。
func (s *Server) fixBadTimes() {
	rows, err := s.st.db.Query(`SELECT id, rel_path, taken_at, year_month FROM assets
		WHERE taken_at > ? OR taken_at < ?`, time.Now().AddDate(1, 0, 0).Unix(), int64(631152000))
	if err != nil {
		return
	}
	type bad struct {
		id   int64
		rel  string
		t    int64
		ym   string
	}
	var list []bad
	for rows.Next() {
		var b bad
		if rows.Scan(&b.id, &b.rel, &b.t, &b.ym) == nil {
			list = append(list, b)
		}
	}
	rows.Close()
	if len(list) == 0 {
		return
	}

	fixed := 0
	for _, b := range list {
		var when time.Time
		// 优先用磁盘上文件的修改时间（通常是对的）
		if fi, err := os.Stat(filepath.Join(s.cfg.Root, b.rel)); err == nil {
			when = saneTime(fi.ModTime())
		} else {
			when = saneTime(time.Unix(b.t, 0))
		}
		ym := when.Format("2006-01")
		if _, err := s.st.db.Exec(`UPDATE assets SET taken_at=?, year_month=? WHERE id=?`,
			when.Unix(), ym, b.id); err == nil {
			fixed++
			log.Printf("fix time: id=%d %s -> %s", b.id, b.ym, ym)
		}
	}
	if fixed > 0 {
		log.Printf("fix time: 修正了 %d 条异常拍摄时间", fixed)
	}
}

// fixVideoDurations 给"时长为 0"的视频补上时长（最多 200 个/次，避免启动太慢）。
func (s *Server) fixVideoDurations() {
	rows, err := s.st.db.Query(`SELECT id, rel_path FROM assets
		WHERE status='active' AND kind='video' AND duration<=0 LIMIT 200`)
	if err != nil {
		return
	}
	type v struct {
		id  int64
		rel string
	}
	var list []v
	for rows.Next() {
		var x v
		if rows.Scan(&x.id, &x.rel) == nil {
			list = append(list, x)
		}
	}
	rows.Close()
	fixed := 0
	for _, x := range list {
		abs := s.cfg.Root + "/" + x.rel
		pr := &Probe{}
		probeDurationByFFmpeg(abs, pr)
		if pr.Duration > 0 {
			if _, err := s.st.db.Exec(`UPDATE assets SET duration=? WHERE id=?`, pr.Duration, x.id); err == nil {
				fixed++
			}
		}
	}
	if fixed > 0 {
		log.Printf("fix duration: 补齐 %d 个视频的时长", fixed)
	}
}