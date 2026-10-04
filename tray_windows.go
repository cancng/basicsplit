package main

import (
	"bytes"
	"encoding/binary"
	"os/exec"
)

const autostartLabel = "Windows ile başlat"

// The tray and the engine are the same process here.
func setEnabled(on bool) { enabled.Store(on) }
func stopEngine()        {}

func openInEditor(path string) {
	exec.Command("notepad.exe", path).Start()
}

// appIcon returns the tray icon as an .ico file.
func appIcon(r, g, b byte) []byte {
	const size = iconSize
	var buf bytes.Buffer
	le := binary.LittleEndian

	// ICONDIR + one ICONDIRENTRY
	pixels := size * size * 4
	mask := size * size / 8
	binary.Write(&buf, le, []uint16{0, 1, 1})
	buf.Write([]byte{size, size, 0, 0})
	binary.Write(&buf, le, []uint16{1, 32})
	binary.Write(&buf, le, []uint32{uint32(40 + pixels + mask), 22})

	// BITMAPINFOHEADER; height is doubled because it covers the mask too
	binary.Write(&buf, le, []uint32{40, size, size * 2})
	binary.Write(&buf, le, []uint16{1, 32})
	binary.Write(&buf, le, []uint32{0, uint32(pixels), 0, 0, 0, 0})

	// BGRA rows, stored bottom-up
	pix := iconRGBA(r, g, b)
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			p := pix[(y*size+x)*4:]
			buf.Write([]byte{p[2], p[1], p[0], p[3]})
		}
	}
	buf.Write(make([]byte, mask))
	return buf.Bytes()
}
