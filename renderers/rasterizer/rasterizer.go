package rasterizer

import (
	"image"
	"math"

	"github.com/tdewolff/canvas"
	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/vector"
)

// Draw draws the canvas on a new image with given resolution (in dots-per-millimeter). Higher resolution will result in larger images.
func Draw(c *canvas.Canvas, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(c.W*resolution.DPMM()+0.5), int(c.H*resolution.DPMM()+0.5)))
	ras := FromImage(img, resolution, colorSpace)
	c.RenderTo(ras)
	ras.Close()
	return img
}

// Rasterizer is a rasterizing renderer.
type Rasterizer struct {
	draw.Image
	resolution canvas.Resolution
	colorSpace canvas.ColorSpace
}

// New returns a renderer that draws to a rasterized image. By default the linear color space is used, which assumes input and output colors are in linearRGB. If the sRGB color space is used for drawing with an average of gamma=2.2, the input and output colors are assumed to be in sRGB (a common assumption) and blending happens in linearRGB. Be aware that for text this results in thin stems for black-on-white (but wide stems for white-on-black).
func New(width, height float64, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer {
	img := image.NewRGBA(image.Rect(0, 0, int(width*resolution.DPMM()+0.5), int(height*resolution.DPMM()+0.5)))
	return FromImage(img, resolution, colorSpace)
}

// FromImage returns a renderer that draws to an existing image.
func FromImage(img draw.Image, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer {
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		panic("rasterizer size 0x0, increase resolution")
	} else if float64(math.MaxInt32) < float64(bounds.Dx())*float64(bounds.Dy()) {
		panic("rasterizer overflow, decrease resolution")
	}

	if colorSpace == nil {
		colorSpace = canvas.DefaultColorSpace
	}
	return &Rasterizer{
		Image:      img,
		resolution: resolution,
		colorSpace: colorSpace,
	}
}

func (r *Rasterizer) Close() {
	if _, ok := r.colorSpace.(canvas.LinearColorSpace); !ok {
		// gamma compress
		changeColorSpace(r.Image, r.Image, r.colorSpace.FromLinear)
	}
}

// Size returns the size of the canvas in millimeters.
func (r *Rasterizer) Size() (float64, float64) {
	size := r.Bounds().Size()
	return float64(size.X) / r.resolution.DPMM(), float64(size.Y) / r.resolution.DPMM()
}

// RenderPath renders a path to the canvas using a style and a transformation matrix.
func (r *Rasterizer) RenderPath(path *canvas.Path, style canvas.Style, m canvas.Matrix) {
	// TODO: use fill rule (EvenOdd, NonZero) for rasterizer
	bounds := canvas.Rect{}
	var fill, stroke *canvas.Path
	if style.HasFill() {
		fill = path.Transform(m)
		if !style.HasStroke() {
			bounds = fill.Bounds()
		}
	}
	if style.HasStroke() {
		tolerance := canvas.PixelTolerance / r.resolution.DPMM()
		stroke = path
		if 0 < len(style.Dashes) {
			stroke = stroke.Dash(style.DashOffset, style.Dashes...)
		}
		stroke = stroke.Stroke(style.StrokeWidth, style.StrokeCapper, style.StrokeJoiner, tolerance)
		stroke = stroke.Transform(m)
		bounds = stroke.Bounds()
	}

	padding := 2
	dx, dy := 0, 0
	size := r.Bounds().Size()
	dpmm := r.resolution.DPMM()
	x := int(bounds.X*dpmm) - padding
	y := size.Y - int((bounds.Y+bounds.H)*dpmm) - padding
	w := int(bounds.W*dpmm) + 2*padding
	h := int(bounds.H*dpmm) + 2*padding
	if (x+w <= 0 || size.X <= x) && (y+h <= 0 || size.Y <= y) {
		return // outside canvas
	}

	zp := image.Point{x, y}
	if x < 0 {
		dx = -x
		x = 0
	}
	if y < 0 {
		dy = -y
		y = 0
	}
	if size.X <= x+w {
		w = size.X - x
	}
	if size.Y <= y+h {
		h = size.Y - y
	}
	if w <= 0 || h <= 0 {
		return // has no size
	}

	if style.HasFill() {
		if style.Fill.IsPattern() {
			if hatch, ok := style.Fill.Pattern.(*canvas.HatchPattern); ok {
				style.Fill = hatch.Fill
				fill = hatch.Tile(fill)
			}
		}

		ras := vector.NewRasterizer(w, h)
		fill = fill.Translate(-float64(x)/dpmm, -float64(size.Y-y-h)/dpmm)
		fill.ToRasterizer(ras, r.resolution)
		var src image.Image
		if style.Fill.IsColor() {
			src = image.NewUniform(r.colorSpace.ToLinear(style.Fill.Color))
		} else if style.Fill.IsGradient() {
			gradient := style.Fill.Gradient.SetColorSpace(r.colorSpace)
			src = NewGradientImage(gradient, zp, size, r.resolution)
		} else if style.Fill.IsPattern() {
			pattern := style.Fill.Pattern.SetColorSpace(r.colorSpace)
			pattern.ClipTo(r, fill)
		}
		if src != nil {
			ras.Draw(r.Image, image.Rect(x, y, x+w, y+h), src, image.Point{dx, dy})
		}
	}
	if style.HasStroke() {
		if style.Stroke.IsPattern() {
			if hatch, ok := style.Stroke.Pattern.(*canvas.HatchPattern); ok {
				style.Stroke = hatch.Fill
				stroke = hatch.Tile(stroke)
			}
		}

		ras := vector.NewRasterizer(w, h)
		stroke = stroke.Translate(-float64(x)/dpmm, -float64(size.Y-y-h)/dpmm)
		stroke.ToRasterizer(ras, r.resolution)
		var src image.Image
		if style.Stroke.IsColor() {
			src = image.NewUniform(r.colorSpace.ToLinear(style.Stroke.Color))
		} else if style.Stroke.IsGradient() {
			gradient := style.Stroke.Gradient.SetColorSpace(r.colorSpace)
			src = NewGradientImage(gradient, zp, size, r.resolution)
		} else if style.Stroke.IsPattern() {
			pattern := style.Stroke.Pattern.SetColorSpace(r.colorSpace)
			pattern.ClipTo(r, stroke)
		}
		if src != nil {
			ras.Draw(r.Image, image.Rect(x, y, x+w, y+h), src, image.Point{dx, dy})
		}
	}
}

// RenderText renders a text object to the canvas using a transformation matrix.
func (r *Rasterizer) RenderText(text *canvas.Text, m canvas.Matrix) {
	text.RenderAsPath(r, m, r.resolution)
}

// RenderImage renders an image to the canvas using a transformation matrix.
func (r *Rasterizer) RenderImage(img image.Image, m canvas.Matrix) {
	// add transparent margin to image for smooth borders when rotating
	// TODO: optimize when transformation is only translation or stretch (if optimizing, dont overwrite original img when gamma correcting)
	margin := 4
	size := img.Bounds().Size()
	sp := img.Bounds().Min // starting point
	img2 := image.NewRGBA(image.Rect(0, 0, size.X+margin*2, size.Y+margin*2))
	draw.Draw(img2, image.Rect(margin, margin, size.X+margin, size.Y+margin), img, sp, draw.Over)

	// draw to destination image
	// note that we need to correct for the added margin in origin and m
	dpmm := r.resolution.DPMM()
	origin := m.Dot(canvas.Point{-float64(margin), float64(img2.Bounds().Size().Y - margin)}).Mul(dpmm)
	m = m.Scale(dpmm, dpmm)

	if _, ok := r.colorSpace.(canvas.LinearColorSpace); !ok {
		// gamma decompress
		changeColorSpace(img2, img2, r.colorSpace.ToLinear)
	}

	h := float64(r.Bounds().Size().Y)
	aff3 := f64.Aff3{m[0][0], -m[0][1], origin.X, -m[1][0], m[1][1], h - origin.Y}
	draw.CatmullRom.Transform(r, aff3, img2, img2.Bounds(), draw.Over, nil)
}
