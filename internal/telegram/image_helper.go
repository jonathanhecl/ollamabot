package telegram

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

type point2D struct {
	x, y int
}

var digitSegments = map[rune][][]point2D{
	'0': {{{0, 0}, {2, 0}}, {{2, 0}, {2, 4}}, {{2, 4}, {0, 4}}, {{0, 4}, {0, 0}}},
	'1': {{{1, 0}, {1, 4}}},
	'2': {{{0, 0}, {2, 0}}, {{2, 0}, {2, 2}}, {{2, 2}, {0, 2}}, {{0, 2}, {0, 4}}, {{0, 4}, {2, 4}}},
	'3': {{{0, 0}, {2, 0}}, {{2, 0}, {2, 4}}, {{0, 2}, {2, 2}}, {{0, 4}, {2, 4}}},
	'4': {{{0, 0}, {0, 2}}, {{0, 2}, {2, 2}}, {{2, 0}, {2, 4}}},
	'5': {{{2, 0}, {0, 0}}, {{0, 0}, {0, 2}}, {{0, 2}, {2, 2}}, {{2, 2}, {2, 4}}, {{2, 4}, {0, 4}}},
	'6': {{{2, 0}, {0, 0}}, {{0, 0}, {0, 4}}, {{0, 4}, {2, 4}}, {{2, 4}, {2, 2}}, {{2, 2}, {0, 2}}},
	'7': {{{0, 0}, {2, 0}}, {{2, 0}, {2, 4}}},
	'8': {{{0, 0}, {2, 0}}, {{2, 0}, {2, 4}}, {{2, 4}, {0, 4}}, {{0, 4}, {0, 0}}, {{0, 2}, {2, 2}}},
	'9': {{{0, 2}, {0, 0}}, {{0, 0}, {2, 0}}, {{2, 0}, {2, 4}}, {{0, 2}, {2, 2}}, {{0, 4}, {2, 4}}},
}

func clamp(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func distanceToSegment(px, py, x1, y1, x2, y2 float64) float64 {
	dx := x2 - x1
	dy := y2 - y1
	if dx == 0 && dy == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / (dx*dx + dy*dy)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
}

func drawSegment(img *image.RGBA, x1, y1, x2, y2 float64, thickness float64, c color.RGBA) {
	minX := math.Min(x1, x2) - thickness - 1
	maxX := math.Max(x1, x2) + thickness + 1
	minY := math.Min(y1, y2) - thickness - 1
	maxY := math.Max(y1, y2) + thickness + 1

	rf, gf, bf, af := float64(c.R)/255.0, float64(c.G)/255.0, float64(c.B)/255.0, float64(c.A)/255.0

	for y := int(minY); y <= int(maxY); y++ {
		for x := int(minX); x <= int(maxX); x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			d := distanceToSegment(float64(x), float64(y), x1, y1, x2, y2)
			if d <= thickness/2.0+0.5 {
				factor := 1.0
				if d > thickness/2.0-0.5 {
					factor = 1.0 - (d - (thickness/2.0 - 0.5))
				}
				if factor > 0 {
					curr := img.RGBAAt(x, y)
					cf := factor * af
					nr := uint8(clamp((rf*cf+float64(curr.R)/255.0*(1.0-cf))*255.0, 0, 255))
					ng := uint8(clamp((gf*cf+float64(curr.G)/255.0*(1.0-cf))*255.0, 0, 255))
					nb := uint8(clamp((bf*cf+float64(curr.B)/255.0*(1.0-cf))*255.0, 0, 255))
					img.SetRGBA(x, y, color.RGBA{nr, ng, nb, 255})
				}
			}
		}
	}
}

func drawFilledCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	minX := cx - r - 1
	maxX := cx + r + 1
	minY := cy - r - 1
	maxY := cy + r + 1

	rf, gf, bf, af := float64(c.R)/255.0, float64(c.G)/255.0, float64(c.B)/255.0, float64(c.A)/255.0

	for y := int(minY); y <= int(maxY); y++ {
		for x := int(minX); x <= int(maxX); x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Hypot(dx, dy)
			if dist <= r+0.5 {
				factor := 1.0
				if dist > r-0.5 {
					factor = 1.0 - (dist - (r - 0.5))
				}
				if factor > 0 {
					curr := img.RGBAAt(x, y)
					cf := factor * af
					nr := uint8(clamp((rf*cf+float64(curr.R)/255.0*(1.0-cf))*255.0, 0, 255))
					ng := uint8(clamp((gf*cf+float64(curr.G)/255.0*(1.0-cf))*255.0, 0, 255))
					nb := uint8(clamp((bf*cf+float64(curr.B)/255.0*(1.0-cf))*255.0, 0, 255))
					img.SetRGBA(x, y, color.RGBA{nr, ng, nb, 255})
				}
			}
		}
	}
}

func drawDigit(img *image.RGBA, cx, cy, cw, ch float64, char rune, thickness float64, c color.RGBA) {
	if char == '%' {
		x1 := cx
		y1 := cy + ch
		x2 := cx + cw
		y2 := cy
		drawSegment(img, x1, y1, x2, y2, thickness, c)

		dotR := thickness * 0.9
		drawFilledCircle(img, cx+cw*0.25, cy+ch*0.25, dotR, c)
		drawFilledCircle(img, cx+cw*0.75, cy+ch*0.75, dotR, c)
		return
	}

	segs, ok := digitSegments[char]
	if !ok {
		return
	}
	for _, seg := range segs {
		gx1, gy1 := seg[0].x, seg[0].y
		gx2, gy2 := seg[1].x, seg[1].y

		x1 := cx + float64(gx1)*(cw/2.0)
		y1 := cy + float64(gy1)*(ch/4.0)
		x2 := cx + float64(gx2)*(cw/2.0)
		y2 := cy + float64(gy2)*(ch/4.0)

		drawSegment(img, x1, y1, x2, y2, thickness, c)
	}
}

func drawPercentText(img *image.RGBA, cx, cy float64, percent int, c color.RGBA) {
	text := fmt.Sprintf("%d%%", percent)
	digitW := 12.0
	digitH := 20.0
	spacing := 3.0
	thickness := 2.5

	totalW := float64(len(text))*digitW + float64(len(text)-1)*spacing
	startX := cx - totalW/2.0
	startY := cy - digitH/2.0

	for i, char := range text {
		drawDigit(img, startX+float64(i)*(digitW+spacing), startY, digitW, digitH, char, thickness, c)
	}
}

func drawRadialGlow(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	minX := math.Max(0, cx-r)
	maxX := math.Min(float64(img.Bounds().Dx()-1), cx+r)
	minY := math.Max(0, cy-r)
	maxY := math.Min(float64(img.Bounds().Dy()-1), cy+r)

	rf, gf, bf, af := float64(c.R)/255.0, float64(c.G)/255.0, float64(c.B)/255.0, float64(c.A)/255.0

	for y := int(minY); y <= int(maxY); y++ {
		for x := int(minX); x <= int(maxX); x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Hypot(dx, dy)
			if dist < r {
				pct := 1.0 - (dist / r)
				pct = pct * pct * (3.0 - 2.0*pct) // Smoothstep

				factor := pct * af
				if factor > 0 {
					curr := img.RGBAAt(x, y)
					nr := uint8(clamp((rf*factor+float64(curr.R)/255.0*(1.0-factor))*255.0, 0, 255))
					ng := uint8(clamp((gf*factor+float64(curr.G)/255.0*(1.0-factor))*255.0, 0, 255))
					nb := uint8(clamp((bf*factor+float64(curr.B)/255.0*(1.0-factor))*255.0, 0, 255))
					img.SetRGBA(x, y, color.RGBA{nr, ng, nb, 255})
				}
			}
		}
	}
}

func generateProgressImage(percent int) ([]byte, error) {
	width, height := 512, 512
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Base background: very dark blue/black (hex #0f0f11)
	bg := color.RGBA{15, 15, 17, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// Draw glowing blurred blobs (like the web UI's premium background)
	// Blob 1: Indigo/Blue near top-left
	drawRadialGlow(img, 120, 120, 260, color.RGBA{99, 102, 241, 60}) // Indigo
	// Blob 2: Cyan near bottom-right
	drawRadialGlow(img, 390, 390, 240, color.RGBA{6, 182, 212, 50}) // Cyan
	// Blob 3: Pink near bottom-left/middle
	drawRadialGlow(img, 160, 370, 220, color.RGBA{236, 72, 153, 40}) // Pink

	// Draw spinner in the center
	spinCX, spinCY := 256.0, 220.0
	spinInnerR := 40.0
	spinOuterR := 46.0

	// Draw sweep spinner (sweep gradient opacity)
	for y := int(spinCY - spinOuterR - 2); y <= int(spinCY + spinOuterR + 2); y++ {
		for x := int(spinCX - spinOuterR - 2); x <= int(spinCX + spinOuterR + 2); x++ {
			if x < 0 || y < 0 || x >= width || y >= height {
				continue
			}
			dx := float64(x) - spinCX
			dy := float64(y) - spinCY
			dist := math.Hypot(dx, dy)
			if dist >= spinInnerR && dist <= spinOuterR {
				theta := math.Atan2(dy, dx)
				intensity := (theta + math.Pi) / (2.0 * math.Pi)
				factor := 0.15 + 0.85*intensity

				// Apply slight anti-aliasing on the edges of the ring
				edgeDistMin := dist - spinInnerR
				edgeDistMax := spinOuterR - dist
				edgeFactor := 1.0
				if edgeDistMin < 0.5 {
					edgeFactor = edgeDistMin + 0.5
				} else if edgeDistMax < 0.5 {
					edgeFactor = edgeDistMax + 0.5
				}

				factor *= edgeFactor

				curr := img.RGBAAt(x, y)
				nr := uint8(clamp((1.0*factor+float64(curr.R)/255.0*(1.0-factor))*255.0, 0, 255))
				ng := uint8(clamp((1.0*factor+float64(curr.G)/255.0*(1.0-factor))*255.0, 0, 255))
				nb := uint8(clamp((1.0*factor+float64(curr.B)/255.0*(1.0-factor))*255.0, 0, 255))
				img.SetRGBA(x, y, color.RGBA{nr, ng, nb, 255})
			}
		}
	}

	// Draw percentage text inside the spinner
	drawPercentText(img, spinCX, spinCY, percent, color.RGBA{255, 255, 255, 240})

	// Draw progress bar track container near the bottom (width 360, height 6)
	barCX, barCY := 256.0, 400.0
	barW := 360.0
	barThickness := 6.0

	// Draw track background (semi-transparent white)
	drawSegment(img, barCX-barW/2.0, barCY, barCX+barW/2.0, barCY, barThickness, color.RGBA{255, 255, 255, 38})

	// Draw track fill (solid white with glow)
	if percent > 0 {
		fillW := barW * float64(percent) / 100.0
		drawSegment(img, barCX-barW/2.0, barCY, barCX-barW/2.0+fillW, barCY, barThickness, color.RGBA{255, 255, 255, 230})
	}

	// Encode to PNG
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
