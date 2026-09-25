package images

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func picture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	return img
}

func encoded(t *testing.T, img image.Image, format string) []byte {
	t.Helper()
	var b bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&b, img)
	case "jpeg":
		err = jpeg.Encode(&b, img, nil)
	case "gif":
		err = gif.Encode(&b, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// What a terminal types when a file is dropped on it: escaped spaces, or quotes.
func TestWordsAreSplitAsAShellWould(t *testing.T) {
	got := words(`see /tmp/Screen\ Shot.png and "/tmp/a b.jpg" or '/tmp/c d.gif' ok`)
	want := []string{"see", "/tmp/Screen Shot.png", "and", "/tmp/a b.jpg", "or", "/tmp/c d.gif", "ok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestFindPathsNamesOnlyPicturesThatExist(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "Screen Shot.png")
	notes := filepath.Join(dir, "notes.txt")
	for _, p := range []string{shot, notes} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	text := "why is " + strings.ReplaceAll(shot, " ", `\ `) + " broken, see " + notes + " and " +
		filepath.Join(dir, "missing.png") + " and " + strings.ReplaceAll(shot, " ", `\ `)
	if got := FindPaths(text); len(got) != 1 || got[0] != shot {
		t.Fatalf("found %q", got)
	}
}

func TestALargePictureIsScaledAndASmallOneKept(t *testing.T) {
	big, err := Prepare(encoded(t, picture(3000, 2000), "png"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(big.Data))
	if err != nil || config.Width != MaxSide || config.Height != 853 || big.MediaType != "image/png" {
		t.Fatalf("scaled to %dx%d %s, %v", config.Width, config.Height, big.MediaType, err)
	}
	tall, _ := Prepare(encoded(t, picture(500, 2560), "jpeg"), "image/jpeg")
	config, format, _ := image.DecodeConfig(bytes.NewReader(tall.Data))
	if config.Height != MaxSide || config.Width != 250 || format != "jpeg" || tall.MediaType != "image/jpeg" {
		t.Fatalf("a tall JPEG became %dx%d %s", config.Width, config.Height, format)
	}
	small := encoded(t, picture(200, 100), "png")
	kept, _ := Prepare(small, "image/png")
	if !bytes.Equal(kept.Data, small) {
		t.Fatal("a picture small enough was encoded again")
	}
	animated, _ := Prepare(encoded(t, picture(64, 64), "gif"), "image/gif")
	if animated.MediaType != "image/png" {
		t.Fatalf("a GIF was sent as %s", animated.MediaType)
	}
}

// A small file can declare a canvas that would take gigabytes to decode; it is refused on its header.
func TestAnEnormousCanvasIsRefusedUnread(t *testing.T) {
	data := encoded(t, picture(2, 2), "png")
	// The IHDR chunk's width and height, then its checksum again.
	binary.BigEndian.PutUint32(data[16:], 60000)
	binary.BigEndian.PutUint32(data[20:], 60000)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
	if _, err := Prepare(data, "image/png"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("refused with %v", err)
	}
	if _, err := Prepare([]byte("not a picture"), "image/png"); err == nil {
		t.Fatal("something that is not a picture was accepted")
	}
}

func TestLoadRefusesWhatItCannotSend(t *testing.T) {
	dir := t.TempDir()
	webp := filepath.Join(dir, "a.webp")
	if err := os.WriteFile(webp, []byte("RIFF....WEBP"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(webp); err != nil || got.MediaType != "image/webp" {
		t.Fatalf("a small WebP: %+v %v", got.MediaType, err)
	}
	if err := os.WriteFile(webp, make([]byte, maxSent+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(webp); err == nil {
		t.Fatal("a WebP too large to send unscaled was accepted")
	}
	if _, err := Load(filepath.Join(dir, "a.bmp")); err == nil {
		t.Fatal("a BMP was accepted")
	}
	if _, err := Load(dir + "/"); err == nil {
		t.Fatal("a directory was read")
	}
}
