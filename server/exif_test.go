package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// 诊断用：打印 IFD0 里读到的每个 tag。
func TestDumpIFD0(t *testing.T) {
	path := os.Getenv("EXIF_FILE")
	if path == "" {
		t.Skip("EXIF_FILE not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte("Exif\x00\x00"))
	if i < 0 {
		t.Fatal("no exif")
	}
	tiff := data[i+6:]
	var bo binary.ByteOrder = binary.LittleEndian
	if len(tiff) > 2 && tiff[0] == 'M' {
		bo = binary.BigEndian
	}
	off := int(bo.Uint32(tiff[4:8]))
	t.Logf("tiff len=%d ifd0=%d", len(tiff), off)
	m := readIFD(tiff, bo, off)
	for k, v := range m {
		t.Logf("tag %#04x len=%d raw=%x", k, len(v), v)
	}
	if v, ok := m[0x0112]; ok {
		t.Logf("orientation raw=%x -> %d", v, bo.Uint16(v))
	} else {
		t.Logf("orientation tag MISSING")
	}
	p := Open(path)
	t.Logf("parsed Orientation=%d Make=%q", p.Orientation, p.Make)

	p2 := &Probe{Orientation: 1}
	parseTIFF(tiff, p2)
	t.Logf("direct parseTIFF Orientation=%d Make=%q", p2.Orientation, p2.Make)
}
