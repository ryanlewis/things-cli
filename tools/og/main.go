// Command og renders the social preview image for things.rlew.io.
//
//	make og                        # writes docs/static/img/og.png
//	go run -C tools/og . out.png   # writes somewhere else
//
// It draws with the fonts the site uses, fetched once from pinned upstream
// commits into the user cache directory. Set FONT_DIR to point at local
// copies instead. Re-run it whenever the headline changes and commit the PNG.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

const (
	width  = 1200
	height = 630
	scale  = 2 // render at 2x, then downsample for crisp text
)

var (
	bg     = color.NRGBA{14, 14, 16, 255}
	text   = color.NRGBA{236, 236, 234, 255}
	muted  = color.NRGBA{163, 163, 168, 255}
	dim    = color.NRGBA{107, 107, 112, 255}
	accent = color.NRGBA{16, 185, 129, 255}
	mark   = color.NRGBA{156, 163, 175, 255}
	panel  = color.NRGBA{22, 22, 24, 255}
	border = color.NRGBA{42, 42, 46, 255}
)

// Pinned upstream commits so the render is reproducible.
const (
	instrumentSansCommit = "7fa22308a3d0c94ee2b3cd537a1196b65db34a3e" // Instrument/instrument-sans
	googleFontsCommit    = "5e35378e6bda803962ee6fd257e444a7d459660d" // google/fonts
)

var fonts = map[string]string{
	"InstrumentSans-SemiBold.ttf": "https://raw.githubusercontent.com/Instrument/instrument-sans/" + instrumentSansCommit + "/fonts/ttf/InstrumentSans-SemiBold.ttf",
	"InstrumentSans-Regular.ttf":  "https://raw.githubusercontent.com/Instrument/instrument-sans/" + instrumentSansCommit + "/fonts/ttf/InstrumentSans-Regular.ttf",
	"IBMPlexMono-Regular.ttf":     "https://raw.githubusercontent.com/google/fonts/" + googleFontsCommit + "/ofl/ibmplexmono/IBMPlexMono-Regular.ttf",
	"IBMPlexMono-Medium.ttf":      "https://raw.githubusercontent.com/google/fonts/" + googleFontsCommit + "/ofl/ibmplexmono/IBMPlexMono-Medium.ttf",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "og:", err)
		os.Exit(1)
	}
}

func run() error {
	out := filepath.Join(repoRoot(), "docs", "static", "img", "og.png")
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	dir, err := fontDir()
	if err != nil {
		return err
	}
	head, err := face(dir, "InstrumentSans-SemiBold.ttf", 84)
	if err != nil {
		return err
	}
	sub, err := face(dir, "InstrumentSans-Regular.ttf", 30)
	if err != nil {
		return err
	}
	mono, err := face(dir, "IBMPlexMono-Medium.ttf", 26)
	if err != nil {
		return err
	}
	monoSmall, err := face(dir, "IBMPlexMono-Regular.ttf", 24)
	if err != nil {
		return err
	}

	img := image.NewNRGBA(image.Rect(0, 0, width*scale, height*scale))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	glow(img)

	const m = 88 * scale // margin

	// Mark and wordmark.
	const box = 44 * scale
	ring(img, m, m, box, box, 10*scale, 3*scale, mark)
	check(img, m, m, accent)
	drawText(img, m+box+16*scale, m+8*scale, "things-cli", mono, text, 0)

	// Headline.
	y := 198 * scale
	drawText(img, m-3*scale, y, "Things 3 from your terminal,", head, text, -0.035)
	drawText(img, m-3*scale, y+96*scale, "or from your agent.", head, accent, -0.035)

	// Sub line.
	drawText(img, m, 428*scale, "Tasks from the shell. JSON for scripts. A skill for your agent.", sub, muted, 0)

	// Install pill, bottom left.
	py := 512 * scale
	label := "$ brew install ryanlewis/tap/things"
	pw := measure(monoSmall, label) + 44*scale
	roundedRect(img, m, py, pw, 54*scale, 10*scale, border)
	roundedRect(img, m+scale, py+scale, pw-2*scale, 54*scale-2*scale, 10*scale-scale, panel)
	drawText(img, m+22*scale, py+13*scale, label, monoSmall, text, 0)

	// Domain, bottom right.
	domain := "things.rlew.io"
	drawText(img, width*scale-m-measure(monoSmall, domain), py+13*scale, domain, monoSmall, dim, 0)

	final := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(final, final.Bounds(), img, img.Bounds(), draw.Src, nil)

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, final); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d KB)\n", out, info.Size()/1024)
	return nil
}

// repoRoot is two directories up from this source file (tools/og/main.go).
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// fontDir returns a directory holding every font in fonts, fetching any that
// are missing from the pinned upstream URLs into the user cache.
func fontDir() (string, error) {
	if dir := os.Getenv("FONT_DIR"); dir != "" {
		return dir, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "things-cli", "og-fonts", instrumentSansCommit[:12]+"-"+googleFontsCommit[:12])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for name, url := range fonts {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		fmt.Fprintln(os.Stderr, "fetching", name)
		if err := fetch(url, path); err != nil {
			return "", fmt.Errorf("fetching %s: %w", name, err)
		}
	}
	return dir, nil
}

func fetch(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// typeface is a font.Face that remembers the em size it was created at.
type typeface struct {
	font.Face
	em float64 // pixels, already multiplied by scale
}

// face loads a font at the given 1x pixel size.
func face(dir, name string, px float64) (typeface, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return typeface{}, err
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return typeface{}, fmt.Errorf("%s: %w", name, err)
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: px * scale, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return typeface{}, err
	}
	return typeface{Face: fc, em: px * scale}, nil
}

// drawText draws s with its ascender line at y (the same anchor as a CSS
// line box top), with optional letter-spacing as a fraction of the em.
func drawText(dst draw.Image, x, y int, s string, fc typeface, c color.Color, tracking float64) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: fc, Dot: fixed.P(x, y)}
	d.Dot.Y += fc.Metrics().Ascent
	if tracking == 0 {
		d.DrawString(s)
		return
	}
	step := fixed.Int26_6(math.Round(tracking * fc.em * 64))
	for _, r := range s {
		d.DrawString(string(r))
		d.Dot.X += step
	}
}

func measure(fc typeface, s string) int {
	return font.MeasureString(fc, s).Ceil()
}

// glow paints the emerald radial glow behind the top of the image, mirroring
// the landing page hero.
func glow(img *image.NRGBA) {
	const (
		cx, cy = width * scale / 2, -45 * scale
		rx, ry = 680.0 * scale, 440.0 * scale
		peak   = 50.0 / 255
	)
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		dy := (float64(y) - cy) / ry
		for x := b.Min.X; x < b.Max.X; x++ {
			dx := (float64(x) - cx) / rx
			t := math.Sqrt(dx*dx + dy*dy)
			if t >= 1 {
				continue
			}
			a := peak * (1 - t) * (1 - t)
			i := img.PixOffset(x, y)
			img.Pix[i+0] = blend(img.Pix[i+0], accent.R, a)
			img.Pix[i+1] = blend(img.Pix[i+1], accent.G, a)
			img.Pix[i+2] = blend(img.Pix[i+2], accent.B, a)
		}
	}
}

func blend(under, over uint8, a float64) uint8 {
	return uint8(math.Round(float64(under)*(1-a) + float64(over)*a))
}

// paint fills the accumulated path in r onto dst with c.
func paint(dst *image.NRGBA, r *vector.Rasterizer, c color.Color) {
	r.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
}

const kappa = 0.5522847498 // cubic approximation of a quarter circle

// roundedPath appends a rounded rectangle to r, clockwise when cw is true.
func roundedPath(r *vector.Rasterizer, x, y, w, h, rad float64, cw bool) {
	k := rad * kappa
	if cw {
		r.MoveTo(float32(x+rad), float32(y))
		r.LineTo(float32(x+w-rad), float32(y))
		r.CubeTo(float32(x+w-rad+k), float32(y), float32(x+w), float32(y+rad-k), float32(x+w), float32(y+rad))
		r.LineTo(float32(x+w), float32(y+h-rad))
		r.CubeTo(float32(x+w), float32(y+h-rad+k), float32(x+w-rad+k), float32(y+h), float32(x+w-rad), float32(y+h))
		r.LineTo(float32(x+rad), float32(y+h))
		r.CubeTo(float32(x+rad-k), float32(y+h), float32(x), float32(y+h-rad+k), float32(x), float32(y+h-rad))
		r.LineTo(float32(x), float32(y+rad))
		r.CubeTo(float32(x), float32(y+rad-k), float32(x+rad-k), float32(y), float32(x+rad), float32(y))
		r.ClosePath()
		return
	}
	r.MoveTo(float32(x+rad), float32(y))
	r.CubeTo(float32(x+rad-k), float32(y), float32(x), float32(y+rad-k), float32(x), float32(y+rad))
	r.LineTo(float32(x), float32(y+h-rad))
	r.CubeTo(float32(x), float32(y+h-rad+k), float32(x+rad-k), float32(y+h), float32(x+rad), float32(y+h))
	r.LineTo(float32(x+w-rad), float32(y+h))
	r.CubeTo(float32(x+w-rad+k), float32(y+h), float32(x+w), float32(y+h-rad+k), float32(x+w), float32(y+h-rad))
	r.LineTo(float32(x+w), float32(y+rad))
	r.CubeTo(float32(x+w), float32(y+rad-k), float32(x+w-rad+k), float32(y), float32(x+w-rad), float32(y))
	r.ClosePath()
}

func roundedRect(dst *image.NRGBA, x, y, w, h, rad int, c color.Color) {
	r := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
	roundedPath(r, float64(x), float64(y), float64(w), float64(h), float64(rad), true)
	paint(dst, r, c)
}

// ring strokes a rounded rectangle: the outer outline clockwise minus the
// inner one anticlockwise, which the non-zero winding rule turns into a band.
func ring(dst *image.NRGBA, x, y, w, h, rad, stroke int, c color.Color) {
	r := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
	roundedPath(r, float64(x), float64(y), float64(w), float64(h), float64(rad), true)
	s := float64(stroke)
	roundedPath(r, float64(x)+s, float64(y)+s, float64(w)-2*s, float64(h)-2*s, float64(rad)-s, false)
	paint(dst, r, c)
}

// check draws the tick inside the mark: two round-capped strokes.
func check(dst *image.NRGBA, ox, oy int, c color.Color) {
	pts := [][2]float64{{12, 23}, {19, 30}, {31, 15}}
	r := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
	for i := 0; i+1 < len(pts); i++ {
		capsule(r,
			float64(ox)+pts[i][0]*scale, float64(oy)+pts[i][1]*scale,
			float64(ox)+pts[i+1][0]*scale, float64(oy)+pts[i+1][1]*scale,
			4*scale)
	}
	paint(dst, r, c)
}

// capsule appends a line from (x1,y1) to (x2,y2) with round caps of width w.
func capsule(r *vector.Rasterizer, x1, y1, x2, y2, w float64) {
	dx, dy := x2-x1, y2-y1
	l := math.Hypot(dx, dy)
	nx, ny := -dy/l*w/2, dx/l*w/2
	r.MoveTo(float32(x1+nx), float32(y1+ny))
	r.LineTo(float32(x2+nx), float32(y2+ny))
	r.LineTo(float32(x2-nx), float32(y2-ny))
	r.LineTo(float32(x1-nx), float32(y1-ny))
	r.ClosePath()
	circle(r, x1, y1, w/2)
	circle(r, x2, y2, w/2)
}

func circle(r *vector.Rasterizer, cx, cy, rad float64) {
	k := rad * kappa
	r.MoveTo(float32(cx+rad), float32(cy))
	r.CubeTo(float32(cx+rad), float32(cy+k), float32(cx+k), float32(cy+rad), float32(cx), float32(cy+rad))
	r.CubeTo(float32(cx-k), float32(cy+rad), float32(cx-rad), float32(cy+k), float32(cx-rad), float32(cy))
	r.CubeTo(float32(cx-rad), float32(cy-k), float32(cx-k), float32(cy-rad), float32(cx), float32(cy-rad))
	r.CubeTo(float32(cx+k), float32(cy-rad), float32(cx+rad), float32(cy-k), float32(cx+rad), float32(cy))
	r.ClosePath()
}
