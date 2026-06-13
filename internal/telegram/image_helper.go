package telegram

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

var simpleFont = map[rune][5]uint8{
	'0': {0x1f, 0x11, 0x11, 0x11, 0x1f},
	'1': {0x00, 0x00, 0x1f, 0x00, 0x00},
	'2': {0x1d, 0x15, 0x15, 0x15, 0x17},
	'3': {0x15, 0x15, 0x15, 0x15, 0x1f},
	'4': {0x07, 0x04, 0x04, 0x04, 0x1f},
	'5': {0x17, 0x15, 0x15, 0x15, 0x1d},
	'6': {0x1f, 0x15, 0x15, 0x15, 0x1d},
	'7': {0x01, 0x01, 0x19, 0x05, 0x03},
	'8': {0x1f, 0x15, 0x15, 0x15, 0x1f},
	'9': {0x17, 0x15, 0x15, 0x15, 0x1f},
	'%': {0x19, 0x08, 0x04, 0x02, 0x13},
}

func drawRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			img.Set(x+dx, y+dy, c)
		}
	}
}

func drawChar(img *image.RGBA, x, y int, char rune, scale int, c color.Color) {
	grid, ok := simpleFont[char]
	if !ok {
		return
	}
	for col := 0; col < 5; col++ {
		val := grid[col]
		for row := 0; row < 5; row++ {
			if (val & (1 << row)) != 0 {
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						img.Set(x+col*scale+dx, y+row*scale+dy, c)
					}
				}
			}
		}
	}
}

func drawString(img *image.RGBA, x, y int, text string, scale int, c color.Color) {
	currX := x
	for _, char := range text {
		drawChar(img, currX, y, char, scale, c)
		currX += (5 + 1) * scale
	}
}

func generateProgressImage(percent int) ([]byte, error) {
	width, height := 512, 512
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Background: dark gray (RGB 45, 45, 45)
	bg := color.RGBA{45, 45, 45, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// Draw progress track container
	trackBg := color.RGBA{80, 80, 80, 255}
	trackWidth := 320
	trackHeight := 16
	trackX := (width - trackWidth) / 2
	trackY := (height - trackHeight) / 2 + 40 // offset downwards a bit

	// Draw track background
	drawRect(img, trackX, trackY, trackWidth, trackHeight, trackBg)

	// Draw track fill (nice teal/green accent color)
	trackFill := color.RGBA{74, 222, 128, 255}
	fillWidth := (trackWidth * percent) / 100
	if fillWidth > 0 {
		drawRect(img, trackX, trackY, fillWidth, trackHeight, trackFill)
	}

	// Draw percentage text (e.g. "50%")
	percentText := ""
	if percent < 10 {
		percentText = string(rune('0'+percent)) + "%"
	} else if percent < 100 {
		percentText = string(rune('0'+percent/10)) + string(rune('0'+percent%10)) + "%"
	} else {
		percentText = "100%"
	}

	scale := 8 // 5x5 font scaled up by 8 makes it 40x40 per char
	charWidth := 5 * scale
	charSpacing := 1 * scale
	totalTextWidth := len(percentText)*charWidth + (len(percentText)-1)*charSpacing
	textX := (width - totalTextWidth) / 2
	textY := trackY - 80 // position above the progress bar

	textColor := color.RGBA{240, 240, 240, 255}
	drawString(img, textX, textY, percentText, scale, textColor)

	// Encode to PNG
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
