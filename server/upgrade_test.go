package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func upgradeServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	st, e := OpenStore(filepath.Join(root, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{st: st, cfg: Config{Root: root, DataDir: root, CacheDir: filepath.Join(root, "cache"), UploadChunk: 256 << 10}, queue: make(chan int64, 100)}
	if e = s.ensureAuthTables(); e != nil {
		t.Fatal(e)
	}
	s.migrateUserSpace()
	s.ensureAdmin()
	if e = s.ensureMembers(); e != nil {
		t.Fatal(e)
	}
	s.loadSpaces()
	if e = s.ensureUploadTable(); e != nil {
		t.Fatal(e)
	}
	if e = s.ensureFamilyTables(); e != nil {
		t.Fatal(e)
	}
	s.ensureSmartColumns()
	s.ensureFaceTables()
	if e = s.ensureAlbumTables(); e != nil {
		t.Fatal(e)
	}
	return s
}
func call(t *testing.T, fn http.HandlerFunc, method, user string, body any, path map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if b, ok := body.([]byte); ok {
		data = b
	} else {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "/", bytes.NewReader(data))
	r.Header.Set("X-Lumina-User", user)
	for k, v := range path {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}
func addMember(t *testing.T, s *Server, name string, shared bool, quota int64) {
	t.Helper()
	w := call(t, s.hMemberSave, "POST", "admin", map[string]any{"name": name, "password": "test-pass-123", "shared": shared, "quota": quota}, nil)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
}
func addAsset(t *testing.T, s *Server, owner, name string, at int64) int64 {
	t.Helper()
	r, e := s.st.db.Exec(`INSERT INTO assets(owner,kind,rel_path,name,size,mtime,taken_at,year_month) VALUES(?,'photo',?,?,10,0,?,'2026-10')`, owner, owner+"/"+name, name, at)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := r.LastInsertId()
	return id
}
func TestMembersIsolationAndQuota(t *testing.T) {
	s := upgradeServer(t)
	addMember(t, s, "alice", false, 100)
	addMember(t, s, "bob", true, 0)
	if !canSee("alice", "alice") || canSee("alice", "bob") || canSee("alice", "family") || !canSee("bob", "family") {
		t.Fatal("scope mismatch")
	}
	if ownerOf("alice/手机照片与视频备份/a.jpg") != "alice" || !validOwner("alice") {
		t.Fatal("dynamic directory")
	}
	if w := call(t, s.hMemberSave, "POST", "alice", map[string]any{"name": "evil", "password": "password"}, nil); w.Code != 403 {
		t.Fatal("non-admin member creation")
	}
	if w := call(t, s.hUploadInit, "POST", "alice", map[string]any{"owner": "bob", "name": "a.jpg", "size": 10}, nil); w.Code != 403 {
		t.Fatal("cross-space upload")
	}
	if w := call(t, s.hUploadInit, "POST", "alice", map[string]any{"owner": "alice", "name": "a.jpg", "size": 101}, nil); w.Code != 409 {
		t.Fatalf("quota: %d %s", w.Code, w.Body)
	}
	if _, e := os.Stat(filepath.Join(s.cfg.Root, "alice", "手机照片与视频备份")); e != nil {
		t.Fatal(e)
	}
}
func TestSearchCombinedAndScoped(t *testing.T) {
	s := upgradeServer(t)
	addMember(t, s, "alice", false, 0)
	addMember(t, s, "bob", false, 0)
	at := searchDate("2026-10-20", false) + 3600
	id := addAsset(t, s, "alice", "trip.jpg", at)
	addAsset(t, s, "bob", "trip.jpg", at)
	_, _ = s.st.db.Exec(`UPDATE assets SET fav=1,lat=30.27,lon=120.15,has_gps=1 WHERE id=?`, id)
	f := TimelineFilter{Owners: []string{"alice"}, Query: "trip", From: searchDate("2026-10-20", false), To: searchDate("2026-10-20", true), Favorites: true, Limit: 10}
	list, e := s.st.TimelineFiltered(f)
	if e != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("search %v %v", list, e)
	}
	f.Query = "杭州"
	list, e = s.st.TimelineFiltered(f)
	if e != nil || len(list) != 1 {
		t.Fatalf("city search %v %v", list, e)
	}
	f.Query = "%' OR 1=1 --"
	list, e = s.st.TimelineFiltered(f)
	if e != nil || len(list) != 0 {
		t.Fatalf("literal search %v %v", list, e)
	}
}
func TestResumeBitmapAndMissingChunk(t *testing.T) {
	s := upgradeServer(t)
	addMember(t, s, "alice", false, 0)
	addMember(t, s, "bob", false, 0)
	req := map[string]any{"owner": "alice", "name": "test.jpg", "size": 262145, "mtime": 100}
	w := call(t, s.hUploadInit, "POST", "alice", req, nil)
	var init struct {
		SID   string
		Chunk int64
	}
	if json.Unmarshal(w.Body.Bytes(), &init) != nil || init.SID == "" {
		t.Fatalf("init: %s", w.Body)
	}
	path := map[string]string{"sid": init.SID, "idx": "1"}
	w = call(t, s.hUploadChunk, "PUT", "alice", []byte{1}, path)
	if w.Code != 200 {
		t.Fatalf("chunk %s", w.Body)
	}
	w = call(t, s.hUploadComplete, "POST", "alice", nil, path)
	if w.Code != 409 {
		t.Fatal("sparse incomplete file accepted")
	}
	w = call(t, s.hUploadStatus, "GET", "bob", nil, path)
	if w.Code != 403 {
		t.Fatal("cross-user status")
	}
	w = call(t, s.hUploadInit, "POST", "alice", req, nil)
	var resumed struct {
		SID      string
		Received []int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resumed)
	if resumed.SID != init.SID || len(resumed.Received) != 1 || resumed.Received[0] != 1 {
		t.Fatalf("resume %s", w.Body)
	}
	path["idx"] = "0"
	w = call(t, s.hUploadChunk, "PUT", "alice", []byte{1}, path)
	if w.Code != 400 {
		t.Fatal("short chunk accepted")
	}
	w = call(t, s.hUploadChunk, "PUT", "alice", make([]byte, 262144), path)
	if w.Code != 200 {
		t.Fatalf("full chunk %s", w.Body)
	}
	row, e := s.loadUpload(init.SID)
	if e != nil || countBits(row.Bits) != 2 {
		t.Fatalf("bitmap %v", e)
	}
	w = call(t, s.hUploadComplete, "POST", "alice", nil, path)
	if w.Code != 200 {
		t.Fatalf("complete %d %s", w.Code, w.Body)
	}
}
func TestPeopleCorrectionsSurviveRebuild(t *testing.T) {
	s := upgradeServer(t)
	addMember(t, s, "alice", false, 0)
	addMember(t, s, "bob", false, 0)
	a := addAsset(t, s, "alice", "a.jpg", 1)
	b := addAsset(t, s, "alice", "b.jpg", 2)
	c := addAsset(t, s, "bob", "c.jpg", 3)
	for _, id := range []int64{a, b, c} {
		if _, e := s.st.db.Exec(`INSERT INTO faces(asset,emb,created) VALUES(?,?,0)`, id, vecToBytes([]float32{1, 0})); e != nil {
			t.Fatal(e)
		}
	}
	s.rebuildPeople()
	var first, other int64
	_ = s.st.db.QueryRow(`SELECT person FROM faces WHERE asset=?`, a).Scan(&first)
	_ = s.st.db.QueryRow(`SELECT person FROM faces WHERE asset=?`, c).Scan(&other)
	if first == other {
		t.Fatal("cross-space clustering")
	}
	w := call(t, s.hPeopleName, "POST", "alice", map[string]any{"name": "家人"}, map[string]string{"id": fmtID(first)})
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	var face int64
	_ = s.st.db.QueryRow(`SELECT id FROM faces WHERE asset=?`, b).Scan(&face)
	w = call(t, s.hPeopleSplit, "POST", "alice", map[string]any{"person": first, "faces": []int64{face}, "name": "另一个人"}, nil)
	if w.Code != 200 {
		t.Fatalf("split %s", w.Body)
	}
	s.rebuildPeople()
	var pid int64
	_ = s.st.db.QueryRow(`SELECT person FROM faces WHERE id=?`, face).Scan(&pid)
	if pid == first {
		t.Fatal("manual split lost")
	}
	var name string
	_ = s.st.db.QueryRow(`SELECT name FROM people WHERE id=?`, first).Scan(&name)
	if name != "家人" {
		t.Fatal("name lost")
	}
	w = call(t, s.hPeopleMerge, "POST", "alice", map[string]any{"from": pid, "to": first}, nil)
	if w.Code != 200 {
		t.Fatalf("merge %s", w.Body)
	}
	w = call(t, s.hPeopleMerge, "POST", "alice", map[string]any{"from": other, "to": first}, nil)
	if w.Code != 403 {
		t.Fatal("cross-member merge")
	}
}
func fmtID(id int64) string { b, _ := json.Marshal(id); return string(b) }
