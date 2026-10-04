package main

import (
	"bytes"
	"image"
	"image/png"
	"os/exec"
)

const autostartLabel = "Oturum açılışında başlat"

func openInEditor(path string) {
	exec.Command("open", "-t", path).Start()
}

// appIcon returns the tray icon as a PNG file.
func appIcon(r, g, b byte) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	copy(img.Pix, iconRGBA(r, g, b))
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
