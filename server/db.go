package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Asset 是索引中的一条媒体记录。
type Asset struct {
	ID        int64  `json:"id"`
	Owner     string `json:"owner"`
	Kind      string `json:"kind"`
	RelPath   string `json:"rel_path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MTime     int64  `json:"mtime"`
	Mime      string `json:"mime"`
	Sha256    string `json:"sha256"`
	W         int    `json:"w"`
	H         int    `json:"h"`
	Orient    int    `json:"orient"`
	TakenAt   int64  `json:"taken_at"`
	YearMonth string `json:"year_month"`
	Camera    string `json:"camera"`
	Lens      string `json:"lens"`
	Aperture  string `json:"aperture"`
	ISO       int    `json:"iso"`
	Focal     string `json:"focal"`
	Duration  float64 `json:"duration"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	HasGPS    bool   `json:"has_gps"`
	Fav       int    `json:"fav"`
	Status    string `json:"status"`
	Missing   bool   `json:"missing"`
	Folder    string `json:"folder"`
	Sub       string `json:"sub"`
}

type Bucket struct {
	YearMonth string `json:"ym"`
	Count     int    `json:"count"`
	Videos    int    `json:"videos"`
	Cover     int64  `json:"cover"`
	Label     string `json:"label"`
}

type Store struct {
	db *sql.DB
	mu sync.Mutex // 串行化写操作，WAL 下读写不互相阻塞
}

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA busy_timeout=8000;
PRAGMA temp_store=MEMORY;

CREATE TABLE IF NOT EXISTS assets(
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  owner       TEXT NOT NULL,
  kind        TEXT NOT NULL,
  rel_path    TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  size        INTEGER NOT NULL,
  mtime       INTEGER NOT NULL,
  mime        TEXT NOT NULL DEFAULT '',
  sha256      TEXT NOT NULL DEFAULT '',
  w           INTEGER NOT NULL DEFAULT 0,
  h           INTEGER NOT NULL DEFAULT 0,
  orient      INTEGER NOT NULL DEFAULT 1,
  taken_at    INTEGER NOT NULL DEFAULT 0,
  year_month  TEXT NOT NULL DEFAULT '',
  camera      TEXT NOT NULL DEFAULT '',
  lens        TEXT NOT NULL DEFAULT '',
  aperture    TEXT NOT NULL DEFAULT '',
  iso         INTEGER NOT NULL DEFAULT 0,
  focal       TEXT NOT NULL DEFAULT '',
  duration    REAL NOT NULL DEFAULT 0,
  lat         REAL NOT NULL DEFAULT 0,
  lon         REAL NOT NULL DEFAULT 0,
  has_gps     INTEGER NOT NULL DEFAULT 0,
  fav         INTEGER NOT NULL DEFAULT 0,
  status      TEXT NOT NULL DEFAULT 'active',
  created_at  INTEGER NOT NULL DEFAULT 0,
  folder      TEXT NOT NULL DEFAULT '',
  sub         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tl     ON assets(owner,status,kind,taken_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_ym     ON assets(year_month);
CREATE INDEX IF NOT EXISTS idx_status ON assets(status);
CREATE INDEX IF NOT EXISTS idx_folder ON assets(folder);
CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT);
`

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// cache_size=-32000 表示 32MB 页缓存；照片多了以后查询全在这个缓存里跑
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-2000)&_pragma=temp_store(MEMORY)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)
	// 老库补列（SQLite 没有 ADD COLUMN IF NOT EXISTS，重复执行会报错，忽略）
	_, _ = db.Exec(`ALTER TABLE assets ADD COLUMN folder TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE assets ADD COLUMN sub TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE assets ADD COLUMN secret INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE assets ADD COLUMN missing INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE assets ADD COLUMN missing_since INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE assets ADD COLUMN nothumb INTEGER NOT NULL DEFAULT 0`)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// 索引：照片多了以后，翻页/分组都靠这几个索引，否则每次全表扫描
	// 同一份内容可能以不同文件名上传（比如相册封面/重命名后的文件），
	// 去重后只保留一个资产，这里记住所有出现过的「文件名+大小」，
	// 否则备份对账会永远认为这些文件没备份，反复重传。
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS asset_names(
		asset_id INTEGER NOT NULL,
		name     TEXT NOT NULL,
		size     INTEGER NOT NULL,
		PRIMARY KEY(asset_id, name, size))`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_asset_names ON asset_names(name, size)`)

	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_assets_timeline ON assets(status, taken_at DESC, id DESC)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_assets_folder ON assets(folder)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_assets_owner ON assets(owner, status)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_assets_ym ON assets(year_month)`)

	migrate(db)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---- 扫描期查询 ----

func (s *Store) Existing(relPath string) (id int64, size, mtime int64, err error) {
	row := s.db.QueryRow(`SELECT id,size,mtime FROM assets WHERE rel_path=?`, relPath)
	err = row.Scan(&id, &size, &mtime)
	if err == sql.ErrNoRows {
		return 0, 0, 0, nil
	}
	return
}

func (s *Store) Upsert(a *Asset) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`
INSERT INTO assets(owner,kind,rel_path,name,size,mtime,mime,sha256,w,h,orient,taken_at,year_month,
                   camera,lens,aperture,iso,focal,duration,lat,lon,has_gps,status,created_at,folder,sub)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(rel_path) DO UPDATE SET
  size=excluded.size, mtime=excluded.mtime, mime=excluded.mime,
  sha256=CASE WHEN excluded.sha256<>'' THEN excluded.sha256 ELSE assets.sha256 END,
  w=excluded.w, h=excluded.h, orient=excluded.orient, taken_at=excluded.taken_at, year_month=excluded.year_month,
  camera=excluded.camera, lens=excluded.lens, aperture=excluded.aperture, iso=excluded.iso,
  focal=excluded.focal, duration=excluded.duration, lat=excluded.lat, lon=excluded.lon,
  has_gps=excluded.has_gps, status='active',
  folder=CASE WHEN excluded.folder<>'' THEN excluded.folder ELSE assets.folder END,
  sub=CASE WHEN excluded.sub<>'' THEN excluded.sub ELSE assets.sub END`,
		a.Owner, a.Kind, a.RelPath, a.Name, a.Size, a.MTime, a.Mime, a.Sha256, a.W, a.H, a.Orient,
		a.TakenAt, a.YearMonth, a.Camera, a.Lens, a.Aperture, a.ISO, a.Focal,
		a.Duration, a.Lat, a.Lon, boolInt(a.HasGPS), "active", time.Now().Unix(),
		a.Folder, a.Sub)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = s.db.QueryRow(`SELECT id FROM assets WHERE rel_path=?`, a.RelPath).Scan(&id)
	}
	return id, nil
}

func (s *Store) Count() (n int) {
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE status='active' AND missing=0`).Scan(&n)
	return
}

// ---- 时间轴查询（键集分页）----

type TimelineFilter struct {
	Folder   string
	Owner    string
	Owners   []string // 非空时按集合过滤，优先于 Owner
	Kind     string
	BeforeAt int64
	BeforeID int64
	Limit    int
	Person   int64
	Album    int64
	Secret   int    // 1 = 只看秘密相册；0 = 只看普通相册
	ExcludeFamily bool // true = 排除共享空间（普通/秘密分区用）
}

func (s *Store) Timeline(owner, kind string, beforeAt int64, beforeID int64, limit int) ([]Asset, error) {
	return s.TimelineFiltered(TimelineFilter{Owner: owner, Kind: kind,
		BeforeAt: beforeAt, BeforeID: beforeID, Limit: limit})
}

func (s *Store) TimelineFiltered(f TimelineFilter) ([]Asset, error) {
	q := `SELECT id,owner,kind,rel_path,name,size,mtime,mime,w,h,orient,taken_at,year_month,
	             camera,lens,aperture,iso,focal,duration,lat,lon,has_gps,fav,status
	      FROM assets WHERE status='active' AND missing=0`
	args := []any{}
	if f.Secret == 1 {
		q += ` AND secret=1`
	} else {
		q += ` AND secret=0`
	}
	if f.ExcludeFamily {
		q += ` AND owner<>'family'`
	}
	if len(f.Owners) > 0 {
		q += ` AND owner IN (?` + strings.Repeat(",?", len(f.Owners)-1) + `)`
		for _, o := range f.Owners {
			args = append(args, o)
		}
	} else if f.Owner != "" && f.Owner != "all" {
		q += ` AND owner=?`
		args = append(args, f.Owner)
	}
	if f.Folder != "" {
		q += ` AND folder=?`
		args = append(args, f.Folder)
	}
	if f.Kind == "photo" || f.Kind == "video" {
		q += ` AND kind=?`
		args = append(args, f.Kind)
	}
	if f.Person > 0 {
		q += ` AND id IN (SELECT asset FROM faces WHERE person=?)`
		args = append(args, f.Person)
	}
	if f.Album > 0 {
		q += ` AND id IN (SELECT asset FROM album_items WHERE album=?)`
		args = append(args, f.Album)
	}
	if f.BeforeAt > 0 {
		q += ` AND (taken_at < ? OR (taken_at = ? AND id < ?))`
		args = append(args, f.BeforeAt, f.BeforeAt, f.BeforeID)
	}
	q += ` ORDER BY taken_at DESC, id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.Owner, &a.Kind, &a.RelPath, &a.Name, &a.Size, &a.MTime,
			&a.Mime, &a.W, &a.H, &a.Orient, &a.TakenAt, &a.YearMonth, &a.Camera, &a.Lens, &a.Aperture,
			&a.ISO, &a.Focal, &a.Duration, &a.Lat, &a.Lon, &a.HasGPS, &a.Fav, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) BucketsScoped(owners []string, kind string) ([]Bucket, error) {
	q := `SELECT year_month, COUNT(*) AS c,
	             SUM(CASE WHEN kind='video' THEN 1 ELSE 0 END) AS v,
	             MIN(id)
	      FROM assets WHERE status='active' AND year_month<>''`
	args := []any{}
	if len(owners) > 0 {
		q += ` AND owner IN (?` + strings.Repeat(",?", len(owners)-1) + `)`
		for _, o := range owners {
			args = append(args, o)
		}
	}
	if kind == "photo" || kind == "video" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	q += ` GROUP BY year_month ORDER BY year_month DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.YearMonth, &b.Count, &b.Videos, &b.Cover); err != nil {
			return nil, err
		}
		b.Label = labelOf(b.YearMonth)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) Get(id int64) (*Asset, error) {
	var a Asset
	err := s.db.QueryRow(`SELECT id,owner,kind,rel_path,name,size,mtime,mime,w,h,orient,taken_at,year_month,
		camera,lens,aperture,iso,focal,duration,lat,lon,has_gps,fav,status
		FROM assets WHERE id=?`, id).Scan(&a.ID, &a.Owner, &a.Kind, &a.RelPath, &a.Name, &a.Size,
		&a.MTime, &a.Mime, &a.W, &a.H, &a.Orient, &a.TakenAt, &a.YearMonth, &a.Camera, &a.Lens,
		&a.Aperture, &a.ISO, &a.Focal, &a.Duration, &a.Lat, &a.Lon, &a.HasGPS, &a.Fav, &a.Status)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) SetMeta(k, v string) {
	_, _ = s.db.Exec(`INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
}

func (s *Store) GetMeta(k string) string {
	var v string
	_ = s.db.QueryRow(`SELECT v FROM meta WHERE k=?`, k).Scan(&v)
	return v
}

func (s *Store) Stats() map[string]any {
	m := map[string]any{}
	var n, photos, videos, bytes int64
	_ = s.db.QueryRow(`SELECT COUNT(*), SUM(size) FROM assets WHERE status='active' AND missing=0`).Scan(&n, &bytes)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE status='active' AND missing=0 AND kind='photo'`).Scan(&photos)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE status='active' AND missing=0 AND kind='video'`).Scan(&videos)
	m["total"] = n
	m["photos"] = photos
	m["videos"] = videos
	m["bytes"] = bytes
	return m
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func labelOf(ym string) string {
	if len(ym) != 7 {
		return ym
	}
	return ym[0:4] + " 年 " + trim0(ym[5:7]) + " 月"
}

func trim0(s string) string {
	for len(s) > 1 && s[0] == '0' {
		s = s[1:]
	}
	return s
}

// migrate 兼容老库：CREATE TABLE IF NOT EXISTS 不会补列，这里逐条尝试。
func migrate(db *sql.DB) {
	stmts := []string{
		`ALTER TABLE assets ADD COLUMN orient INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE assets ADD COLUMN sha256 TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_sha ON assets(sha256)`,
	}
	for _, q := range stmts {
		_, _ = db.Exec(q) // 已存在则报错，忽略
	}
}
