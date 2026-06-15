package telegram

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func TestClamp(t *testing.T) {
	tests := []struct {
		val, min, max float64
		expected      float64
	}{
		{5.0, 1.0, 10.0, 5.0},
		{0.5, 1.0, 10.0, 1.0},
		{11.0, 1.0, 10.0, 10.0},
		{-5.0, -10.0, 0.0, -5.0},
		{-15.0, -10.0, 0.0, -10.0},
	}

	for _, tt := range tests {
		got := clamp(tt.val, tt.min, tt.max)
		if got != tt.expected {
			t.Errorf("clamp(%f, %f, %f) = %f; want %f", tt.val, tt.min, tt.max, got, tt.expected)
		}
	}
}

func TestDistanceToSegment(t *testing.T) {
	// Zero length segment
	d1 := distanceToSegment(1, 1, 0, 0, 0, 0)
	if math.Abs(d1-math.Sqrt(2)) > 1e-9 {
		t.Errorf("distanceToSegment at zero length: got %f, want %f", d1, math.Sqrt(2))
	}

	// Point closer to start (t < 0)
	d2 := distanceToSegment(-1, 0, 0, 0, 5, 0)
	if math.Abs(d2-1.0) > 1e-9 {
		t.Errorf("distanceToSegment close to start: got %f, want 1.0", d2)
	}

	// Point closer to end (t > 1)
	d3 := distanceToSegment(6, 0, 0, 0, 5, 0)
	if math.Abs(d3-1.0) > 1e-9 {
		t.Errorf("distanceToSegment close to end: got %f, want 1.0", d3)
	}

	// Point in the middle (0 <= t <= 1)
	d4 := distanceToSegment(2, 3, 0, 0, 5, 0)
	if math.Abs(d4-3.0) > 1e-9 {
		t.Errorf("distanceToSegment middle: got %f, want 3.0", d4)
	}
}

func TestDrawingHelpers(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	c := color.RGBA{255, 0, 0, 255}

	// Test drawSegment
	drawSegment(img, 5, 5, 45, 5, 2.0, c)
	// Assert at least one pixel changed to red (or blended)
	hasColored := false
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			if img.RGBAAt(x, y).R > 0 {
				hasColored = true
				break
			}
		}
	}
	if !hasColored {
		t.Error("drawSegment did not color any pixels")
	}

	// Reset image
	img = image.NewRGBA(image.Rect(0, 0, 50, 50))
	// Test drawFilledCircle
	drawFilledCircle(img, 25, 25, 10, c)
	hasColored = false
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			if img.RGBAAt(x, y).R > 0 {
				hasColored = true
				break
			}
		}
	}
	if !hasColored {
		t.Error("drawFilledCircle did not color any pixels")
	}

	// Test drawDigit with '%'
	img = image.NewRGBA(image.Rect(0, 0, 50, 50))
	drawDigit(img, 10, 10, 10, 20, '%', 2.0, c)
	hasColored = false
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			if img.RGBAAt(x, y).R > 0 {
				hasColored = true
				break
			}
		}
	}
	if !hasColored {
		t.Error("drawDigit '%' did not color any pixels")
	}

	// Test drawDigit with normal digit '5'
	img = image.NewRGBA(image.Rect(0, 0, 50, 50))
	drawDigit(img, 10, 10, 10, 20, '5', 2.0, c)
	hasColored = false
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			if img.RGBAAt(x, y).R > 0 {
				hasColored = true
				break
			}
		}
	}
	if !hasColored {
		t.Error("drawDigit '5' did not color any pixels")
	}

	// Test drawDigit with unknown char
	img = image.NewRGBA(image.Rect(0, 0, 50, 50))
	drawDigit(img, 10, 10, 10, 20, 'A', 2.0, c)
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			if img.RGBAAt(x, y).R > 0 {
				t.Error("drawDigit with unknown character should not draw anything")
			}
		}
	}

	// Test drawPercentText
	img = image.NewRGBA(image.Rect(0, 0, 100, 50))
	drawPercentText(img, 50, 25, 42, c)
	hasColored = false
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			if img.RGBAAt(x, y).R > 0 {
				hasColored = true
				break
			}
		}
	}
	if !hasColored {
		t.Error("drawPercentText did not color any pixels")
	}

	// Test drawRadialGlow
	img = image.NewRGBA(image.Rect(0, 0, 100, 100))
	drawRadialGlow(img, 50, 50, 30, c)
	hasColored = false
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if img.RGBAAt(x, y).R > 0 {
				hasColored = true
				break
			}
		}
	}
	if !hasColored {
		t.Error("drawRadialGlow did not color any pixels")
	}
}

func TestGenerateProgressImage(t *testing.T) {
	percents := []int{0, 25, 50, 75, 100}
	for _, p := range percents {
		data, err := generateProgressImage(p)
		if err != nil {
			t.Fatalf("generateProgressImage(%d) failed: %v", p, err)
		}
		if len(data) == 0 {
			t.Fatalf("generateProgressImage(%d) returned empty bytes", p)
		}

		// Verify it is a valid PNG
		_, err = png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Errorf("generateProgressImage(%d) returned invalid PNG data: %v", p, err)
		}
	}
}
