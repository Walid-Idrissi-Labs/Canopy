// Package images turns a picture a person points at into one a model can read: found in what they
// typed, read with limits, and scaled so its long side is at most MaxSide pixels.
package images

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// MaxSide is the longest side a picture is sent at. Larger costs more and tells a model no more.
const MaxSide = 1280

// maxFile is the largest picture file read, and maxSent the largest one sent as it is.
const (
	maxFile = 20 << 20
	maxSent = 3 << 20
)

var extensions = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
}

// FindPaths are the picture files named in text, the way a terminal writes a file dropped on it:
// a path, perhaps quoted, perhaps with its spaces escaped. Only files that exist are returned.
func FindPaths(text string) []string {
	var found []string
	seen := map[string]bool{}
	for _, word := range words(text) {
		path := word
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		if _, ok := extensions[strings.ToLower(filepath.Ext(path))]; !ok || seen[path] {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			seen[path] = true
			found = append(found, path)
		}
	}
	return found
}

// words splits text as a shell would: on unescaped whitespace, with quotes and backslashes removed.
func words(text string) []string {
	var out []string
	var current strings.Builder
	var quote rune
	escaped, inWord := false, false
	for _, r := range text {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped, inWord = false, true
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote, inWord = r, true
		case quote == 0 && (r == ' ' || r == '\t' || r == '\n'):
			if inWord {
				out = append(out, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, current.String())
	}
	return out
}

// Load reads a picture and returns it ready to send: scaled down to MaxSide, as PNG, JPEG or the
// original WebP, which is sent as it is when it is small enough since nothing here can decode it.
func Load(path string) (core.Image, error) {
	mediaType, ok := extensions[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return core.Image{}, fmt.Errorf("%s is not a PNG, JPEG, GIF or WebP picture", filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return core.Image{}, err
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return core.Image{}, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFile+1))
	if err != nil {
		return core.Image{}, err
	}
	if len(data) > maxFile {
		return core.Image{}, fmt.Errorf("%s is larger than 20 MB", filepath.Base(path))
	}
	if mediaType == "image/webp" {
		if len(data) > maxSent {
			return core.Image{}, fmt.Errorf("%s is a WebP larger than 3 MB; save it as PNG or JPEG to have it scaled", filepath.Base(path))
		}
		return core.Image{MediaType: mediaType, Data: data}, nil
	}
	return Prepare(data, mediaType)
}

// Prepare scales a PNG, JPEG or GIF so its long side is at most MaxSide, and encodes it again: JPEG
// stays JPEG, anything else becomes PNG. A picture already small enough in size and on disk is sent
// as it came.
func Prepare(data []byte, mediaType string) (core.Image, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return core.Image{}, errors.New("the picture could not be read")
	}
	// Checked before decoding, since a small file can declare an enormous canvas.
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 100_000_000 {
		return core.Image{}, errors.New("the picture is too large to read")
	}
	if config.Width <= MaxSide && config.Height <= MaxSide && len(data) <= maxSent && format != "gif" {
		return core.Image{MediaType: mediaType, Data: data}, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return core.Image{}, errors.New("the picture could not be read")
	}
	scaled := shrink(img, MaxSide)
	var out bytes.Buffer
	if format == "jpeg" {
		if err := jpeg.Encode(&out, scaled, &jpeg.Options{Quality: 85}); err != nil {
			return core.Image{}, err
		}
		return core.Image{MediaType: "image/jpeg", Data: out.Bytes()}, nil
	}
	if err := png.Encode(&out, scaled); err != nil {
		return core.Image{}, err
	}
	if out.Len() > maxSent {
		// A photograph saved as PNG can stay large at this size; JPEG is what it should have been.
		out.Reset()
		if err := jpeg.Encode(&out, scaled, &jpeg.Options{Quality: 85}); err != nil {
			return core.Image{}, err
		}
		return core.Image{MediaType: "image/jpeg", Data: out.Bytes()}, nil
	}
	return core.Image{MediaType: "image/png", Data: out.Bytes()}, nil
}

// shrink scales img so its long side is at most side, averaging each block of source pixels into
// one, which keeps text in a screenshot readable where sampling would lose strokes.
func shrink(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
		return rgba
	}
	nw, nh := side, h*side/w
	if h > w {
		nw, nh = w*side/h, side
	}
	nw, nh = max(nw, 1), max(nh, 1)
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := y*h/nh, max((y+1)*h/nh, y*h/nh+1)
		for x := 0; x < nw; x++ {
			x0, x1 := x*w/nw, max((x+1)*w/nw, x*w/nw+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r, g, bl, a = r+uint64(p[0]), g+uint64(p[1]), bl+uint64(p[2]), a+uint64(p[3])
					n++
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return dst
}

// gif registers the GIF decoder; PNG and JPEG are registered by the imports that encode them.
var _ = gif.Decode

// MaxPerMessage is how many pictures one message carries.
const MaxPerMessage = 5

// ForMessage loads every picture a message names. One that cannot be read is an error, so a message
// is never sent with its picture silently missing.
func ForMessage(text string) ([]core.Image, error) {
	paths := FindPaths(text)
	if len(paths) > MaxPerMessage {
		return nil, fmt.Errorf("a message carries at most %d pictures, and this one names %d", MaxPerMessage, len(paths))
	}
	var out []core.Image
	for _, path := range paths {
		picture, err := Load(path)
		if err != nil {
			return nil, fmt.Errorf("the picture was not attached, so nothing was sent: %v", err)
		}
		out = append(out, picture)
	}
	return out, nil
}
