package main

import (
	"runtime/debug"
	"io"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed all:web
var webAssets embed.FS

type Config struct {
	Root      string
	DataDir   string
	CacheDir  string
	Addr      string
	ThumbConc int
	UploadChunk int64
}

type Server struct {
	forceScan bool
	cfg     Config
	st      *Store
	locks   [64]sync.Mutex
	flocks  [64]sync.Mutex // 视频取帧独立锁，避免和缩略图抢同一把锁
	queue   chan int64
	started time.Time
}

type ownerDef struct{ Dir, Owner, Label string }

var owners = []ownerDef{
	{"lwm", "lwm", "lwm 的相册"},
	{"cr", "cr", "cr 的相册"},
	{"家庭共享", "family", "家庭共享空间"},
}

func main() {
	// 路由器内存紧张：给 Go 一个软上限，超过就积极 GC，避免被内核 OOM 杀掉
	debug.SetMemoryLimit(48 << 20)
	debug.SetGCPercent(40)
	var cfg Config
	flag.StringVar(&cfg.Root, "root", "/mnt/sda1", "媒体根目录")
	flag.StringVar(&cfg.DataDir, "data", "", "数据目录（默认 <root>/.lumina）")
	flag.StringVar(&cfg.CacheDir, "cache", "", "缩略图目录（默认 <root>/.lumina_cache）")
	flag.StringVar(&cfg.Addr, "addr", ":8088", "监听地址")
	flag.IntVar(&cfg.ThumbConc, "thumb-conc", 1, "缩略图生成并发数")
	flag.Int64Var(&cfg.UploadChunk, "upload-chunk", 4<<20, "上传分片大小（字节）")
	thumbOneRel := flag.String("thumb-one", "", "仅生成这一张缩略图后退出")
	thumbOneID := flag.Int64("thumb-id", 0, "配合 -thumb-one：资产 id")
	thumbOneSize := flag.Int64("thumb-fsize", 0, "配合 -thumb-one：源文件大小")
	thumbOneMtime := flag.Int64("thumb-fmtime", 0, "配合 -thumb-one：源文件 mtime")
	thumbOneOrient := flag.Int("thumb-orient", 1, "配合 -thumb-one：EXIF 方向")
	scanOnly := flag.Bool("scan", false, "仅扫描一次后退出")
	rescan := flag.Bool("rescan", false, "启动时强制全量重扫（默认增量）")
	probePath := flag.String("probe", "", "打印单个文件的解析结果后退出（诊断用）")
	doctor := flag.Bool("doctor", false, "运行自检后退出")
	flag.Parse()

	if *thumbOneRel != "" {
		if cfg.DataDir == "" {
			cfg.DataDir = filepath.Join(cfg.Root, ".lumina")
		}
		if cfg.CacheDir == "" {
			cfg.CacheDir = filepath.Join(cfg.Root, ".lumina_cache")
		}
		os.Exit(runThumbOne(cfg, *thumbOneID, *thumbOneRel, *thumbOneSize, *thumbOneMtime, *thumbOneOrient))
	}

	if *probePath != "" {
		pr := Open(*probePath)
		b, _ := json.MarshalIndent(pr, "", "  ")
		fmt.Println(string(b))
		return
	}

	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(cfg.Root, ".lumina")
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = filepath.Join(cfg.Root, ".lumina_cache")
	}
	for _, d := range []string{cfg.DataDir, cfg.CacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatalf("mkdir %s: %v", d, err)
		}
	}

	st, err := OpenStore(filepath.Join(cfg.DataDir, "lumina.db"))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	srv := &Server{cfg: cfg, st: st, queue: make(chan int64, 512), started: time.Now()}
	if err := srv.ensureUploadTable(); err != nil {
		log.Fatalf("upload table: %v", err)
	}
	if err := srv.ensureAuthTables(); err != nil {
		log.Fatalf("auth tables: %v", err)
	}
	srv.migrateUserSpace()
	srv.ensureAdmin()
	srv.loadSpaces()
	if err := srv.ensureFamilyTables(); err != nil {
		log.Fatalf("family tables: %v", err)
	}
	srv.ensureSmartColumns()
	srv.ensureFaceTables()
	if err := srv.ensureAlbumTables(); err != nil {
		log.Fatalf("album tables: %v", err)
	}
	if *doctor {
		os.Exit(srv.printDoctor())
	}
	srv.StartWorkers(cfg.ThumbConc)
	srv.startDBBackup()   // 数据库每日备份（只备份索引，几 MB）
	go srv.backupLoop()
	go srv.warmLoop()   // 后台慢慢补缩略图，翻到哪都是现成的
	go func() {
		time.Sleep(2 * time.Second)
		srv.fixBadTimes()
		srv.fixVideoDurations()
		srv.organizeMisplacedFiles()
		srv.pruneEmptyDirs()
	}()

	go func() {
		lastGC := time.Now()
		for {
			srv.sweepUploads()
			srv.gcUploads()
			srv.checkpointWAL()
			if time.Since(lastGC) > 2*time.Minute {
				srv.purgeMissingAssets()
				lastGC = time.Now()
			}
			_ = lastGC
			// 缩略图改成按需生成：滑到哪张才生成哪张，不再后台预生成
			srv.fixDurationsIfIdle(15)
			srv.gcPurged(7 * 24 * time.Hour)
			srv.gcSessions()
			srv.gcTrash()
			debug.FreeOSMemory() // 定期向系统交还物理内存
			time.Sleep(2 * time.Minute)
		}
	}()

	if *scanOnly || *rescan {
		// -rescan 也是"扫一遍就退出"：以前它会继续起服务，导致端口被占、
		// 脚本卡死。需要常驻请直接不带参数启动。
		srv.forceScan = *rescan
		r := srv.Scan()
		log.Printf("scan exit: +%d ~%d =%d !%d", r.Added, r.Updated, r.Skipped, r.Errors)
		return
	} else {
		go func() { time.Sleep(400 * time.Millisecond); srv.Scan() }()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", srv.hHealth)
	mux.HandleFunc("GET /api/stats", srv.hStats)
	mux.HandleFunc("GET /api/config", srv.hConfig)
	mux.HandleFunc("GET /api/buckets", srv.hBuckets)
	mux.HandleFunc("GET /api/timeline", srv.hTimeline)
	mux.HandleFunc("GET /api/asset/{id}", srv.hAsset)
	mux.HandleFunc("POST /api/scan", srv.hScan)
	mux.HandleFunc("POST /api/thumbs/warm", srv.hWarmThumbs)
	mux.HandleFunc("POST /api/upload/init", srv.hUploadInit)
	mux.HandleFunc("PUT /api/upload/{sid}/{idx}", srv.hUploadChunk)
	mux.HandleFunc("GET /api/upload/{sid}", srv.hUploadStatus)
	mux.HandleFunc("POST /api/upload/{sid}/complete", srv.hUploadComplete)
	mux.HandleFunc("DELETE /api/upload/{sid}", srv.hUploadAbort)
	mux.HandleFunc("GET /api/setup-state", srv.hSetupState)
	mux.HandleFunc("POST /api/setup", srv.hSetup)
	mux.HandleFunc("POST /api/login", srv.hLogin)
	mux.HandleFunc("POST /api/register", srv.hRegister)
	mux.HandleFunc("POST /api/backup/check", srv.hBackupCheck)
	mux.HandleFunc("POST /api/logout", srv.hLogout)
	mux.HandleFunc("GET /api/me", srv.hMe)
	mux.HandleFunc("POST /api/password", srv.hPassword)
	mux.HandleFunc("GET /api/trash", srv.hTrashList)
	mux.HandleFunc("POST /api/trash", srv.hTrash)
	mux.HandleFunc("POST /api/restore", srv.hRestore)
	mux.HandleFunc("DELETE /api/trash/{id}", srv.hPurge)
	mux.HandleFunc("POST /api/purge", srv.hPurgeMany)
	mux.HandleFunc("POST /api/batch/fav", srv.hFav)
	mux.HandleFunc("POST /api/batch/push", srv.hPush)
	mux.HandleFunc("POST /api/family/folder", srv.hFamilyFolder)
	mux.HandleFunc("POST /api/zip", srv.hZip)
	mux.HandleFunc("POST /api/share", srv.hShare)
	mux.HandleFunc("GET /s/{token}", srv.hShared)
	mux.HandleFunc("GET /api/dupes", srv.hDupes)
	mux.HandleFunc("GET /api/events", srv.hEvents)
	mux.HandleFunc("GET /api/places", srv.hPlaces)
	mux.HandleFunc("POST /api/edit", srv.hEdit)
	mux.HandleFunc("POST /api/smart/rebuild", srv.hSmartRebuild)
	mux.HandleFunc("GET /api/smart/stats", srv.hSmartStats)
	mux.HandleFunc("GET /api/faces/pending", srv.hFacesPending)
	mux.HandleFunc("POST /api/faces/{id}", srv.hFacesSubmit)
	mux.HandleFunc("GET /api/people", srv.hPeople)
	mux.HandleFunc("POST /api/people/rebuild", srv.hPeopleRebuild)
	mux.HandleFunc("POST /api/people/{id}/name", srv.hPeopleName)
	mux.HandleFunc("GET /api/people/{id}/assets", srv.hPersonAssets)
	mux.HandleFunc("GET /f/{id}", srv.hFaceImage)
	mux.HandleFunc("GET /api/doctor", srv.hDoctor)
	mux.HandleFunc("POST /api/backup", srv.hBackup)
	mux.HandleFunc("POST /api/prune", srv.hPrune)
	mux.HandleFunc("GET /t/{id}/{name}", srv.hThumb)
	mux.HandleFunc("GET /m/{id}", srv.hMedia)
	mux.HandleFunc("GET /v/{id}/{n}", srv.hVideoFrame)
	mux.HandleFunc("POST /api/poster/{id}", srv.hPoster)
	mux.HandleFunc("POST /api/alias/{id}", srv.hAlias)
	mux.HandleFunc("POST /api/alias/clear", srv.hAliasClear)
	mux.HandleFunc("POST /api/rotate/{id}", srv.hRotate)
	mux.HandleFunc("GET /api/preview/needed", srv.previewNeed)
	mux.HandleFunc("GET /api/videos/noposter", srv.videosNeedPoster)
	mux.HandleFunc("GET /api/folders", srv.hFolders)
	mux.HandleFunc("DELETE /api/folders/{name}", srv.hFolderDelete)
	mux.HandleFunc("POST /api/folders/delete", srv.hFolderDeleteJSON)
	mux.HandleFunc("POST /api/secret/folder", srv.hSecretFolder)
	mux.HandleFunc("POST /api/secret/move", srv.hSecretMove)
	mux.HandleFunc("GET /api/albums", srv.hAlbums)
	mux.HandleFunc("POST /api/albums", srv.hAlbumCreate)
	mux.HandleFunc("POST /api/albums/{id}/add", srv.hAlbumAdd)
	mux.HandleFunc("POST /api/albums/{id}/remove", srv.hAlbumRemove)
	mux.HandleFunc("POST /api/albums/{id}/rename", srv.hAlbumRename)
	mux.HandleFunc("DELETE /api/albums/{id}", srv.hAlbumDelete)

	sub, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Fatalf("web fs: %v", err)
	}
	files := http.FileServerFS(sub)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 单文件 SPA：HTML 必须每次回源，否则升级后浏览器还在跑旧脚本
		if r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/app.js" {
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=604800")
		}
		files.ServeHTTP(w, r)
	}))

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           logMW(srv.authMW(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		log.Printf("LuminaGallery listening on %s  root=%s cache=%s", cfg.Addr, cfg.Root, cfg.CacheDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	ctx, cancel := timeoutCtx(5 * time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

// lockID 给单个资源的缩略图生成加锁，避免并发重复解码。
func (s *Server) lockID(id int64) func() {
	m := &s.locks[((id%64)+64)%64]
	m.Lock()
	return m.Unlock
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/m/") {
			t := time.Now()
			next.ServeHTTP(w, r)
			if d := time.Since(t); d > 800*time.Millisecond {
				log.Printf("slow %s %s %v", r.Method, r.URL.Path, d)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------- handlers ----------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Server) hHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"ok":        true,
		"uptime_s":  int64(time.Since(s.started).Seconds()),
		"db":        s.st.Count(),
		"last_scan": s.st.GetMeta("last_scan"),
		"version":   "2.0.0-p1",
	})
}

func (s *Server) hStats(w http.ResponseWriter, r *http.Request) {
	m := s.st.Stats()
	m["last_scan"] = s.st.GetMeta("last_scan")
	m["last_scan_added"] = s.st.GetMeta("last_scan_added")
	m["last_scan_ms"] = s.st.GetMeta("last_scan_ms")
	// 别名条数 = 手机上有多少文件其实是同一份内容（去重后只存一份）
	var aliases, dupeGroups int64
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM asset_names`).Scan(&aliases)
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM (SELECT sha256 FROM assets WHERE status='active' AND sha256<>'' GROUP BY sha256 HAVING COUNT(*)>1)`).Scan(&dupeGroups)
	m["aliases"] = aliases
	m["dupe_groups"] = dupeGroups
	writeJSON(w, m)
}

func (s *Server) hConfig(w http.ResponseWriter, r *http.Request) {
	type o struct {
		Owner string `json:"owner"`
		Label string `json:"label"`
	}
	out := []o{}
	for _, x := range owners {
		if canSee(userOf(r), x.Owner) {
			out = append(out, o{x.Owner, x.Label})
		}
	}
	writeJSON(w, map[string]any{"owners": out})
}

func (s *Server) hBuckets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.st.BucketsScoped(s.visibleOwners(r, q.Get("owner")), q.Get("kind"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"buckets": list})
}

func (s *Server) hTimeline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 400 {
		limit = 120
	}
	var at, id int64
	if cur := q.Get("before"); cur != "" {
		parts := strings.SplitN(cur, "_", 2)
		if len(parts) == 2 {
			at, _ = strconv.ParseInt(parts[0], 10, 64)
			id, _ = strconv.ParseInt(parts[1], 10, 64)
		}
	}
	person, _ := strconv.ParseInt(q.Get("person"), 10, 64)
	album, _ := strconv.ParseInt(q.Get("album"), 10, 64)
	secret := 0
	if q.Get("secret") == "1" {
		secret = 1
	}
	// 三个分区互相隔离：普通/秘密 里不出现共享空间的照片
	excludeFamily := q.Get("owner") != "family"
	items, err := s.st.TimelineFiltered(TimelineFilter{
		ExcludeFamily: excludeFamily,
		Folder: q.Get("folder"),
		Owners: s.visibleOwners(r, q.Get("owner")), Kind: q.Get("kind"),
		BeforeAt: at, BeforeID: id, Limit: limit,
		Person: person, Album: album, Secret: secret,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	next := ""
	if len(items) == limit {
		last := items[len(items)-1]
		next = strconv.FormatInt(last.TakenAt, 10) + "_" + strconv.FormatInt(last.ID, 10)
	}
	writeJSON(w, map[string]any{"items": items, "next": next})
}

func (s *Server) hAsset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	a, err := s.st.Get(id)
	if err != nil || !canSee(userOf(r), a.Owner) {
		http.Error(w, "not found", 404)
		return
	}
	// 告诉客户端这个视频有没有封面，App 只在缺封面时才截帧
	hasPoster := false
	if a.Kind == "video" {
		if pp := s.posterPath(a); pp != "" {
			if fi, statErr := os.Stat(pp); statErr == nil && fi.Size() > 0 {
				hasPoster = true
			}
		}
	}
	writeJSON(w, map[string]any{
		"id": a.ID, "owner": a.Owner, "kind": a.Kind, "name": a.Name,
		"rel_path": a.RelPath, "size": a.Size, "mtime": a.MTime, "mime": a.Mime,
		"w": a.W, "h": a.H, "orient": a.Orient, "taken_at": a.TakenAt,
		"year_month": a.YearMonth, "duration": a.Duration, "folder": a.Folder,
		"sub": a.Sub, "lat": a.Lat, "lon": a.Lon,
		"has_gps": a.HasGPS, "fav": a.Fav, "poster": hasPoster,
	})
}

func (s *Server) hScan(w http.ResponseWriter, r *http.Request) {
	res := s.Scan()
	// 扫描完顺手把放错年份目录的文件归位
	moved := s.organizeMisplacedFiles()
	s.pruneEmptyDirs()
	writeJSON(w, map[string]any{
		"added": res.Added, "updated": res.Updated, "skipped": res.Skipped,
		"errors": res.Errors, "elapsed_ms": res.Elapsed, "organised": moved,
	})
}

func (s *Server) hThumb(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	name := r.PathValue("name") // 形如 t.jpg
	size := strings.TrimSuffix(name, ".jpg")
	if _, ok := thumbW[size]; !ok {
		size = "t"
	}
	a, err := s.st.Get(id)
	if err != nil || !canSee(userOf(r), a.Owner) {
		http.Error(w, "not found", 404)
		return
	}
	p, err := s.ensureThumb(a, size)
	if err != nil {
		// 解不了（HEVC 视频、损坏图片、HEIC 等）：返回占位图而不是 500 破图。
		// 只缓存 1 小时，等手机端补上封面后能自动替换。
		if ph := placeholderJPEG(size); ph != nil {
			w.Header().Set("Content-Type", "image/jpeg")
			// 占位图绝对不能缓存：一旦手机补上封面，客户端要能立刻拿到真图
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
			_, _ = w.Write(ph)
			return
		}
		http.Error(w, "thumb failed: "+err.Error(), 500)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "open failed", 500)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) hMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	a, err := s.st.Get(id)
	if err != nil || !canSee(userOf(r), a.Owner) {
		http.Error(w, "not found", 404)
		return
	}
	abs := filepath.Join(s.cfg.Root, a.RelPath)
	f, err := os.Open(abs)
	if err != nil {
		http.Error(w, "missing", 404)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "stat", 500)
		return
	}
	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, a.Name, fi.ModTime(), f)
}

// hFolders 列出通过"上传文件夹"产生的分区。
func (s *Server) hFolders(w http.ResponseWriter, r *http.Request) {
	clause, args := ownerIn(s.visibleOwners(r, ""))
	// ?secret=1 时列出秘密分区；默认只列普通分区。
	secretCond := ` AND secret=0`
	coverExpr := `MIN(CASE WHEN secret=0 THEN id END)`
	if r.URL.Query().Get("secret") == "1" {
		secretCond = ` AND secret=1`
		coverExpr = `MIN(id)`
	}
	// ?owner=family 时只列共享空间里的文件夹。
	// 注意：这种情况下不能再叠加 visibleOwners 的 owner IN (...) 条件，
	// 否则会变成 owner='family' AND owner IN ('lwm')，永远查不到东西。
	ownerCond := ` AND owner<>'family'`   // 普通文件夹列表不掺入共享空间
	if ow := r.URL.Query().Get("owner"); ow != "" {
		ownerCond = ` AND owner=?`
		args = []any{ow}
		clause = ""
	}
	rows, err := s.st.db.Query(`SELECT folder, COUNT(*), MIN(id), IFNULL(`+coverExpr+`, 0)
		FROM assets
		WHERE status='active' AND folder<>'' AND missing=0`+secretCond+ownerCond+clause+`
		GROUP BY folder ORDER BY folder`, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type folderRow struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
		Cover int64  `json:"cover"`
	}
	list := []folderRow{}
	for rows.Next() {
		var f folderRow
		var all int64
		if rows.Scan(&f.Name, &f.Count, &all, &f.Cover) == nil {
			list = append(list, f)
		}
	}
	writeJSON(w, map[string]any{"folders": list, "secret": r.URL.Query().Get("secret") == "1"})
}
// hFolderDelete 删除一个文件夹分区：其中的照片移入回收站（可恢复），并清掉分区标记。
// hFolderDeleteJSON 用 JSON 传名字，避免路径编码把空格变成 + 导致匹配不上。
func (s *Server) hFolderDeleteJSON(w http.ResponseWriter, r *http.Request) {
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
	s.deleteFolderFor(w, r, name)
}

func (s *Server) hFolderDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "bad name", 400)
		return
	}
	s.deleteFolderFor(w, r, name)
}

func (s *Server) deleteFolder(w http.ResponseWriter, name string) {
	s.deleteFolderFor(w, nil, name)
}

func (s *Server) deleteFolderFor(w http.ResponseWriter, r *http.Request, name string) {
	// 三个分区必须严格隔离，否则在共享里删文件夹会把原图一起删掉（曾经发生过）
	qv := r.URL.Query()
	clause, args := ownerIn(s.visibleOwners(r, ""))
	switch {
	case qv.Get("owner") == "family":
		clause = ` AND owner='family'`
		args = nil
	case qv.Get("secret") == "1":
		clause = ` AND secret=1 AND owner<>'family'`
		args = nil
	default:
		// 普通分区：绝不碰共享和秘密
		clause = ` AND owner<>'family' AND secret=0`
		args = nil
	}
	q := `SELECT id FROM assets WHERE status='active' AND folder=?` + clause
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

	moved, failed := 0, 0
	for _, id := range ids {
		if err := s.moveToTrash(id); err == nil {
			moved++
		} else {
			failed++
		}
	}
	// 已经在回收站的也清掉分区标记，避免分区列表里又冒出来
	_, _ = s.st.db.Exec(`UPDATE assets SET folder='', sub='' WHERE folder=?`+clause, append([]any{name}, args...)...)
	writeJSON(w, map[string]any{"ok": true, "moved": moved, "failed": failed})
}
// hPurgeMany 批量彻底删除（回收站多选）。
func (s *Server) hPurgeMany(w http.ResponseWriter, r *http.Request) {
	ids, err := decodeIDs(r)
	if err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	n := 0
	for _, id := range ids {
		if s.purgeAsset(id) == nil {
			n++
		}
	}
	writeJSON(w, map[string]any{"ok": true, "purged": n})
}
// hSecretFolder 把整个文件夹分区移入/移出秘密相册。
func (s *Server) hSecretFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Secret bool   `json:"secret"`
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
	flag := 0
	if req.Secret {
		flag = 1
	}
	clause, args := ownerIn(s.visibleOwners(r, ""))
	q := `UPDATE assets SET secret=? WHERE folder=? AND status='active'` + clause
	a := append([]any{flag, name}, args...)
	res, err := s.st.db.Exec(q, a...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	n, _ := res.RowsAffected()
	log.Printf("secret folder: %s -> %v (%d items)", name, req.Secret, n)
	writeJSON(w, map[string]any{"ok": true, "moved": n})
}

// hSecretMove 把照片移入/移出秘密相册。
func (s *Server) hSecretMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []int64 `json:"ids"`
		Secret bool    `json:"secret"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, map[string]any{"ok": true, "moved": 0})
		return
	}
	flag := 0
	if req.Secret {
		flag = 1
	}
	clause, args := ownerIn(s.visibleOwners(r, ""))
	n := 0
	for _, id := range req.IDs {
		q := `UPDATE assets SET secret=? WHERE id=?` + clause
		a := append([]any{flag, id}, args...)
		if res, err := s.st.db.Exec(q, a...); err == nil {
			if c, _ := res.RowsAffected(); c > 0 {
				n += int(c)
			}
		}
	}
	log.Printf("secret move: %d items -> secret=%v", n, req.Secret)
	writeJSON(w, map[string]any{"ok": true, "moved": n})
}

// hWarmThumbs 手动触发缩略图预热（幂等，只补缺的）。
func (s *Server) hWarmThumbs(w http.ResponseWriter, r *http.Request) {
	n := s.warmThumbs(300)
	writeJSON(w, map[string]any{"ok": true, "generated": n})
}

// fixDims 把「需要旋转 90°」的照片宽高交换后再输出。
// 缩略图生成时已经按方向旋转过，如果 JSON 里还是原始宽高，
// 前端算出来的格子比例就和图片不一致，看起来就被拉伸/裁切了。
func fixDims(list []Asset) []Asset {
	for i := range list {
		if list[i].Orient >= 5 && list[i].Orient <= 8 {
			list[i].W, list[i].H = list[i].H, list[i].W
		}
	}
	return list
}

// fixDurationsIfIdle 空闲时补齐视频时长（最多 n 个/轮）。
func (s *Server) fixDurationsIfIdle(n int) int {
	if load1() > 4.0 {
		return 0
	}
	fixed := 0
	rows, err := s.st.db.Query(`SELECT id, rel_path FROM assets
		WHERE kind='video' AND status='active' AND (duration IS NULL OR duration<=0) LIMIT ?`, n)
	if err != nil {
		return 0
	}
	type item struct {
		id  int64
		rel string
	}
	var list []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.rel) == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	for _, it := range list {
		var p Probe
		probeDurationByFFmpeg(filepath.Join(s.cfg.Root, it.rel), &p)
		if p.Duration > 0 {
			_, _ = s.st.db.Exec(`UPDATE assets SET duration=? WHERE id=?`, p.Duration, it.id)
			fixed++
		}
	}
	if fixed > 0 {
		log.Printf("fix duration: 补齐 %d 个视频的时长", fixed)
	}
	return fixed
}

// hAlias 记录「同一份内容还叫过什么名字」。
// 手机端把 HEIC 转成 JPEG 后上传，服务器上只有 .jpg 的名字；
// 备份对账用的却是手机里的 .heic 名字，永远对不上。这里把原始名字记下来。
func (s *Server) hAlias(w http.ResponseWriter, r *http.Request) {
	id := atoi64(r.PathValue("id"))
	var req struct {
		N string `json:"n"`
		S int64  `json:"s"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.N == "" || req.S <= 0 {
		http.Error(w, "bad json", 400)
		return
	}
	a, err := s.st.Get(id)
	if err != nil || !canSee(userOf(r), a.Owner) {
		http.Error(w, "not found", 404)
		return
	}
	_, _ = s.st.db.Exec(`INSERT OR IGNORE INTO asset_names(asset_id,name,size) VALUES(?,?,?)`,
		id, req.N, req.S)
	writeJSON(w, map[string]any{"ok": true})
}

// hAliasClear 清空别名表（管理员）。
// 用途：早先把「内容相同但名字不同」的文件合并成一条记录，导致文件夹数量偏少。
// 清掉别名后，下次备份会把它们各自上传成独立文件。
func (s *Server) hAliasClear(w http.ResponseWriter, r *http.Request) {
	if userOf(r) != "admin" {
		http.Error(w, "admin only", 403)
		return
	}
	if _, err := s.st.db.Exec(`DELETE FROM asset_names`); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}