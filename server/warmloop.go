package main

// 后台缩略图预热：服务启动后自动、持续地把缺的缩略图补齐，
// 用户翻到哪里都是现成的，不用等着现场生成。
//
// 关键优化：用一个游标顺着 id 往前走，每个文件每一轮只 stat 一次。
// 之前每找一张都要 stat 最多 400 行（USB 盘上 stat 很慢），
// 导致一分钟只能生成七八张。

import (
	"log"
	"os"
	"sync"
	"time"
)

var warmCursor struct {
	sync.Mutex
	id int64
}

// nextMissingThumb 用游标往后找一张缺缩略图的资产。
func (s *Server) nextMissingThumb() (id int64, rel string, size, mtime int64, kind string, orient int, ok bool, wrapped bool) {
	warmCursor.Lock()
	cur := warmCursor.id
	warmCursor.Unlock()

	rows, err := s.st.db.Query(`SELECT id, rel_path, size, mtime, kind, orient
		FROM assets WHERE status='active' AND missing=0 AND nothumb=0 AND id > ?
		ORDER BY id LIMIT 300`, cur)
	if err != nil {
		return
	}
	type wrow struct {
		id                  int64
		rel                 string
		size, mtime         int64
		kind                string
		orient              int
	}
	var list []wrow
	for rows.Next() {
		var r wrow
		if rows.Scan(&r.id, &r.rel, &r.size, &r.mtime, &r.kind, &r.orient) == nil {
			list = append(list, r)
		}
	}
	rows.Close()

	if len(list) == 0 {
		// 走到头了：游标归零，从头再来
		warmCursor.Lock()
		warmCursor.id = 0
		warmCursor.Unlock()
		return 0, "", 0, 0, "", 0, false, true
	}

	for _, r := range list {
		warmCursor.Lock()
		warmCursor.id = r.id
		warmCursor.Unlock()
		need := false
		// 两个尺寸都要：m 是网页的模糊底图，缺了会显示成一块黑
		for _, sz := range []string{"m", "t"} {
			if _, err := os.Stat(s.thumbKey(r.id, r.size, r.mtime, sz)); err != nil {
				need = true
				break
			}
		}
		if need {
			return r.id, r.rel, r.size, r.mtime, r.kind, r.orient, true, false
		}
	}
	return 0, "", 0, 0, "", 0, false, false
}

// warmLoop 起若干 worker 并行补缩略图。
func (s *Server) warmLoop() {
	time.Sleep(15 * time.Second) // 等启动扫描先跑完
	workers := 1
	if s.cfg.ThumbConc > 0 && s.cfg.ThumbConc < workers {
		workers = s.cfg.ThumbConc
	}
	for i := 0; i < workers; i++ {
		go s.warmWorker()
	}
}

// warmWorker 单个预热工作线程。
func (s *Server) warmWorker() {
	done := 0
	pass := 0
	for {
		if load1() > 2.5 {
			time.Sleep(15 * time.Second)
			continue
		}
		id, rel, size, mtime, kind, orient, ok, wrapped := s.nextMissingThumb()
		if wrapped {
			pass++
			if done > 0 {
				log.Printf("warm: 第 %d 轮完成，本轮补了 %d 张缩略图", pass, done)
				done = 0
				time.Sleep(30 * time.Second)
			} else {
				// 缩略图全库已补齐，长休眠 10 分钟，不空转磁盘
				time.Sleep(10 * time.Minute)
			}
			continue
		}
		if !ok {
			continue // 这一批都已有缓存，继续往下走
		}
		a := &Asset{ID: id, RelPath: rel, Size: size, MTime: mtime, Kind: kind, Orient: orient}
		for _, sz := range []string{"m", "t"} {
			if _, err := s.ensureThumb(a, sz); err == nil {
				done++
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}
