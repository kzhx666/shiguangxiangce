package main

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Probe struct {
	W, H        int
	Orientation int
	Make        string
	Model       string
	Lens        string
	Aperture    string
	ISO         int
	Focal       string
	TakenAt     time.Time
	HasTaken    bool
	Lat, Lon    float64
	HasGPS      bool
	Duration    float64
}

// Open: 读取媒体元数据。图片靠自研 EXIF 解析，视频走 ffprobe。
func Open(absPath string) *Probe { return OpenNamed(absPath, filepath.Base(absPath)) }

// OpenNamed 允许指定用于文件名兜底的原始文件名（上传时临时文件叫 <sid>.part）。
func OpenNamed(absPath, name string) *Probe {
	p := &Probe{Orientation: 1}
	ext := strings.ToLower(filepath.Ext(absPath))
	if (ext == ".part" || ext == "") && name != "" {
		ext = strings.ToLower(filepath.Ext(name))
	}
	switch ext {
	case ".jpg", ".jpeg", ".jpe":
		parseJPEG(absPath, p)
	case ".png":
		parsePNG(absPath, p)
	case ".heic", ".heif":
		parseHEIC(absPath, p)
	case ".mp4", ".mov", ".m4v", ".3gp", ".mkv", ".webm", ".avi":
		probeVideo(absPath, p)
	}
	if !p.HasTaken {
		if p.TakenAt.IsZero() {
			if t, ok := dateFromName(name); ok {
				p.TakenAt, p.HasTaken = t, true
			}
		}
	}
	if !p.HasTaken {
		if fi, err := os.Stat(absPath); err == nil {
			p.TakenAt, p.HasTaken = fi.ModTime(), true
		}
	}

	// 有些 JPEG（渐进式 / 特殊 EXIF 布局）自研解析拿不到尺寸，
	// 用标准库再读一次文件头，避免按 0×0 处理把缩略图压成正方形。
	if p.W == 0 || p.H == 0 {
		if f, err := os.Open(absPath); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				p.W, p.H = cfg.Width, cfg.Height
			}
			f.Close()
		}
	}
	return p
}

// ---------- JPEG ----------

func parseJPEG(path string, p *Probe) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, 512*1024)
	n, _ := f.Read(buf)
	data := buf[:n]
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return
	}
	i := 2
	seenExif := false
	for i+3 < len(data) {
		if data[i] != 0xFF {
			i++
			continue
		}
		m := data[i+1]
		if m == 0xFF {
			i++
			continue
		}
		if m == 0xD8 || m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			i += 2
			continue
		}
		if m == 0xD9 || m == 0xDA {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(data) {
			break
		}
		seg := data[i+4 : i+2+segLen]
		switch {
		case m == 0xE1 && !seenExif && len(seg) > 6 && string(seg[0:6]) == "Exif\x00\x00":
			// 只认第一个 Exif 块：导出/编辑过的 JPEG 常在后面残留一份旧 EXIF，
			// 若一并解析会把方向、机型等覆盖成原图的旧值。
			parseTIFF(seg[6:], p)
			seenExif = true
		case isSOF(m) && len(seg) >= 5:
			p.H = int(binary.BigEndian.Uint16(seg[1:3]))
			p.W = int(binary.BigEndian.Uint16(seg[3:5]))
		}
		i += 2 + segLen
	}
}

func isSOF(m byte) bool {
	switch m {
	case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
		return true
	}
	return false
}

// ---------- PNG ----------

func parsePNG(path string, p *Probe) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	h := make([]byte, 33)
	n, _ := f.Read(h)
	if n < 33 || !bytes.Equal(h[0:8], []byte("\x89PNG\r\n\x1a\n")) {
		return
	}
	p.W = int(binary.BigEndian.Uint32(h[16:20]))
	p.H = int(binary.BigEndian.Uint32(h[20:24]))
}

// ---------- HEIC / HEIF ----------

func parseHEIC(path string, p *Probe) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	buf := make([]byte, 512*1024)
	n, _ := io.ReadFull(f, buf)
	if n < 32 {
		return
	}
	data := buf[:n]

	// 1. 扫描 EXIF 块并解析元数据 (拍摄时间、方向、机型、镜头、GPS等)
	exifIdx := bytes.Index(data, []byte("Exif\x00\x00"))
	if exifIdx >= 0 && exifIdx+6 < len(data) {
		rem := data[exifIdx+6:]
		ii := bytes.Index(rem, []byte("II*\x00"))
		mm := bytes.Index(rem, []byte("MM\x00*"))
		if ii >= 0 && (mm < 0 || ii < mm) {
			parseTIFF(rem[ii:], p)
		} else if mm >= 0 {
			parseTIFF(rem[mm:], p)
		}
	}

	// 2. 扫描 ISOBMFF ispe box 提取主图尺寸
	pos := 0
	maxPixels := 0
	for pos+16 <= len(data) {
		idx := bytes.Index(data[pos:], []byte("ispe"))
		if idx == -1 {
			break
		}
		ispePos := pos + idx
		if ispePos+16 <= len(data) {
			w := int(binary.BigEndian.Uint32(data[ispePos+8 : ispePos+12]))
			h := int(binary.BigEndian.Uint32(data[ispePos+12 : ispePos+16]))
			if w > 0 && h > 0 && w < 30000 && h < 30000 {
				if w*h > maxPixels {
					maxPixels = w * h
					p.W = w
					p.H = h
				}
			}
		}
		pos = ispePos + 4
	}
}

// ---------- TIFF / EXIF ----------

func typeSize(t uint16) int {
	switch t {
	case 1, 2, 6, 7:
		return 1
	case 3, 8:
		return 2
	case 4, 9, 11:
		return 4
	case 5, 10, 12:
		return 8
	}
	return 0
}

func readIFD(b []byte, bo binary.ByteOrder, off int) map[uint16][]byte {
	out := map[uint16][]byte{}
	if off <= 0 || off+2 > len(b) {
		return out
	}
	n := int(bo.Uint16(b[off:]))
	base := off + 2
	for i := 0; i < n; i++ {
		e := base + i*12
		if e+12 > len(b) {
			break
		}
		tag := bo.Uint16(b[e:])
		typ := bo.Uint16(b[e+2:])
		cnt := int(bo.Uint32(b[e+4:]))
		sz := typeSize(typ) * cnt
		if sz <= 0 {
			continue
		}
		if sz <= 4 {
			out[tag] = b[e+8 : e+8+sz]
		} else {
			o := int(bo.Uint32(b[e+8:]))
			if o > 0 && o+sz <= len(b) {
				out[tag] = b[o : o+sz]
			}
		}
	}
	return out
}

func asciiOf(v []byte) string {
	if i := bytes.IndexByte(v, 0); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(string(v))
}

func parseTIFF(b []byte, p *Probe) {
	if len(b) < 8 {
		return
	}
	var bo binary.ByteOrder
	switch string(b[0:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return
	}
	if bo.Uint16(b[2:4]) != 42 {
		return
	}
	ifd0 := int(bo.Uint32(b[4:8]))
	t0 := readIFD(b, bo, ifd0)

	if v, ok := t0[0x010F]; ok {
		p.Make = asciiOf(v)
	}
	if v, ok := t0[0x0110]; ok {
		p.Model = asciiOf(v)
	}
	if v, ok := t0[0x0112]; ok && len(v) >= 2 {
		p.Orientation = int(bo.Uint16(v))
	}

	var exifOff, gpsOff int
	if v, ok := t0[0x8769]; ok && len(v) >= 4 {
		exifOff = int(bo.Uint32(v))
	}
	if v, ok := t0[0x8825]; ok && len(v) >= 4 {
		gpsOff = int(bo.Uint32(v))
	}

	if exifOff > 0 {
		te := readIFD(b, bo, exifOff)
		if v, ok := te[0x9003]; ok {
			if t, err := time.Parse("2006:01:02 15:04:05", asciiOf(v)); err == nil {
				p.TakenAt, p.HasTaken = t, true
			}
		} else if v, ok := te[0x9004]; ok {
			if t, err := time.Parse("2006:01:02 15:04:05", asciiOf(v)); err == nil {
				p.TakenAt, p.HasTaken = t, true
			}
		}
		if v, ok := te[0xA434]; ok {
			p.Lens = asciiOf(v)
		}
		if v, ok := te[0x829D]; ok && len(v) >= 8 {
			n, d := bo.Uint32(v[0:4]), bo.Uint32(v[4:8])
			if d > 0 {
				p.Aperture = "ƒ/" + f2(float64(n)/float64(d))
			}
		}
		if v, ok := te[0x8827]; ok && len(v) >= 2 {
			p.ISO = int(bo.Uint16(v))
		}
		if v, ok := te[0x920A]; ok && len(v) >= 8 {
			n, d := bo.Uint32(v[0:4]), bo.Uint32(v[4:8])
			if d > 0 {
				p.Focal = f1(float64(n)/float64(d)) + "mm"
			}
		}
	}

	if gpsOff > 0 {
		tg := readIFD(b, bo, gpsOff)
		lat, okLat := gpsCoord(tg, 0x0002, 0x0001, bo)
		lon, okLon := gpsCoord(tg, 0x0004, 0x0003, bo)
		if okLat && okLon {
			p.Lat, p.Lon, p.HasGPS = lat, lon, true
		}
	}
}

func gpsCoord(t map[uint16][]byte, valTag, refTag uint16, bo binary.ByteOrder) (float64, bool) {
	v, ok := t[valTag]
	if !ok || len(v) < 24 {
		return 0, false
	}
	d := float64(bo.Uint32(v[0:4])) / float64(maxu(bo.Uint32(v[4:8]), 1))
	m := float64(bo.Uint32(v[8:12])) / float64(maxu(bo.Uint32(v[12:16]), 1))
	s := float64(bo.Uint32(v[16:20])) / float64(maxu(bo.Uint32(v[20:24]), 1))
	out := d + m/60 + s/3600
	if r, ok := t[refTag]; ok {
		rs := asciiOf(r)
		if rs == "S" || rs == "W" {
			out = -out
		}
	}
	return out, true
}

func maxu(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// ---------- 视频 ----------

type ffprobeOut struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		Duration     string `json:"duration"`
		Tags         struct {
			CreationTime string `json:"creation_time"`
		} `json:"tags"`
		SideDataList []struct {
			Rotation int `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		Tags     struct {
			CreationTime string `json:"creation_time"`
		} `json:"tags"`
	} `json:"format"`
}

func probeVideo(path string, p *Probe) {
	// 优先自己解析 MP4/MOV 容器：这台机器的 ffmpeg 选中了坏的 h264_v4l2m2m
	// 硬件解码器，任何 H.264 视频都探测失败，但容器里的时长/尺寸不需要解码。
	if ok := probeMP4(path, p); ok && p.Duration > 0 && p.W > 0 {
		return
	}

	ctx, cancel := timeoutCtx(15 * time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet", "-print_format", "json",
		"-show_format", "-show_streams", path).Output()
	if err != nil {
		return
	}
	var j ffprobeOut
	if json.Unmarshal(out, &j) != nil {
		return
	}
	for _, s := range j.Streams {
		if s.CodecType == "video" {
			p.W, p.H = s.Width, s.Height
			for _, sd := range s.SideDataList {
				if sd.Rotation == 90 || sd.Rotation == -90 {
					p.W, p.H = p.H, p.W
				}
			}
			break
		}
	}
	if d, err := strconv.ParseFloat(j.Format.Duration, 64); err == nil {
		p.Duration = d
	}
	ts := j.Format.Tags.CreationTime
	if ts == "" && len(j.Streams) > 0 {
		ts = j.Streams[0].Tags.CreationTime
	}
	if ts != "" {
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000000Z", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, ts); err == nil {
				p.TakenAt, p.HasTaken = t.UTC(), true
				break
			}
		}
	}
	// 这台路由器的 ffprobe 有时读不到容器信息，但 ffmpeg -i 一定能打出 Duration
	if p.Duration <= 0 {
		probeDurationByFFmpeg(path, p)
	}
}

// ---------- 文件名时间兜底 ----------

func dateFromName(name string) (time.Time, bool) {
	low := strings.ToLower(name)
	// 微信 mmexport<13位毫秒>
	if i := strings.Index(low, "mmexport"); i >= 0 {
		digits := takeDigits(low[i+8:], 13)
		if len(digits) == 13 {
			if ms, err := strconv.ParseInt(digits, 10, 64); err == nil {
				return time.UnixMilli(ms), true
			}
		}
	}
	// 通用 yyyyMMdd[_-]HHmmss
	for i := 0; i+8 <= len(low); i++ {
		if !allDigits(low[i : i+8]) {
			continue
		}
		rest := low[i+8:]
		j := 0
		for j < len(rest) && !isDigit(rest[j]) {
			j++
		}
		if j+6 > len(rest) || !allDigits(rest[j:j+6]) {
			continue
		}
		y, _ := strconv.Atoi(low[i : i+4])
		mo, _ := strconv.Atoi(low[i+4 : i+6])
		d, _ := strconv.Atoi(low[i+6 : i+8])
		hh, _ := strconv.Atoi(rest[j : j+2])
		mm, _ := strconv.Atoi(rest[j+2 : j+4])
		ss, _ := strconv.Atoi(rest[j+4 : j+6])
		if y >= 1990 && y <= 2100 && mo >= 1 && mo <= 12 && d >= 1 && d <= 31 &&
			hh < 24 && mm < 60 && ss < 60 {
			return time.Date(y, time.Month(mo), d, hh, mm, ss, 0, time.Local), true
		}
	}
	return time.Time{}, false
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func takeDigits(s string, n int) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < n; i++ {
		if isDigit(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ---------- 极简 MP4/MOV 容器解析（不依赖 ffmpeg 解码能力）----------

type mp4Box struct {
	typ   string
	start int64 // 内容起点
	size  int64 // 内容长度
}

func mp4Walk(r io.ReaderAt, off, end int64, fn func(mp4Box) bool) {
	for off+8 <= end {
		var hdr [8]byte
		if _, err := r.ReadAt(hdr[:], off); err != nil {
			return
		}
		size := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		hdrLen := int64(8)
		if size == 1 {
			var ext [8]byte
			if _, err := r.ReadAt(ext[:], off+8); err != nil {
				return
			}
			size = int64(binary.BigEndian.Uint64(ext[:]))
			hdrLen = 16
		} else if size == 0 {
			size = end - off
		}
		if size < hdrLen || off+size > end {
			return
		}
		if fn(mp4Box{typ: typ, start: off + hdrLen, size: size - hdrLen}) {
			return
		}
		off += size
	}
}

// probeMP4 读取 mvhd（时长）与 tkhd（画面尺寸），成功返回 true。
func probeMP4(path string, p *Probe) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	end := fi.Size()

	var ok bool
	mp4Walk(f, 0, end, func(b mp4Box) bool {
		if b.typ != "moov" {
			return false
		}
		mp4Walk(f, b.start, b.start+b.size, func(c mp4Box) bool {
			switch c.typ {
			case "mvhd":
				buf := make([]byte, 32)
				n, _ := f.ReadAt(buf, c.start)
				if n >= 20 {
					ver := buf[0]
					var timescale, duration uint64
					if ver == 1 && n >= 32 {
						timescale = uint64(binary.BigEndian.Uint32(buf[20:24]))
						duration = binary.BigEndian.Uint64(buf[24:32])
					} else {
						timescale = uint64(binary.BigEndian.Uint32(buf[12:16]))
						duration = uint64(binary.BigEndian.Uint32(buf[16:20]))
					}
					if timescale > 0 {
						p.Duration = float64(duration) / float64(timescale)
						ok = true
					}
				}
			case "tkhd":
				buf := make([]byte, 96)
				n, _ := f.ReadAt(buf, c.start)
				if n >= 84 {
					// 宽高在 tkhd 末尾，16.16 定点
					base := n - 8
					w := int(binary.BigEndian.Uint32(buf[base : base+4]) >> 16)
					h := int(binary.BigEndian.Uint32(buf[base+4:base+8]) >> 16)
					if w > 0 && h > 0 && p.W == 0 {
						p.W, p.H = w, h
					}
				}
			}
			return false
		})
		return true
	})
	return ok
}

// probeDurationByFFmpeg 用 `ffmpeg -i` 的 stderr 里那行 Duration 兜底。
// 这台机器上 ffprobe 经常读不出格式信息，但 ffmpeg 永远会打印时长。
func probeDurationByFFmpeg(path string, p *Probe) {
	ctx, cancel := timeoutCtx(20 * time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-i", path)
	out, _ := cmd.CombinedOutput() // 没给输出文件，ffmpeg 会以非 0 退出，stderr 里有 Duration
	txt := string(out)
	i := strings.Index(txt, "Duration:")
	if i < 0 {
		return
	}
	rest := strings.TrimSpace(txt[i+len("Duration:"):])
	if len(rest) < 11 {
		return
	}
	// 形如 00:00:01.20,
	h, _ := strconv.Atoi(strings.TrimSpace(rest[0:2]))
	m, _ := strconv.Atoi(strings.TrimSpace(rest[3:5]))
	secPart := rest[6:11]
	sec, err := strconv.ParseFloat(strings.TrimSuffix(secPart, ","), 64)
	if err != nil {
		return
	}
	p.Duration = float64(h*3600+m*60) + sec
}