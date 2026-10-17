package tasks

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestEmotionCompositeOnlyChangesFeatheredFace(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	generated := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			source.SetNRGBA(x, y, color.NRGBA{200, 100, 30, 255})
		}
	}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			generated.SetNRGBA(x, y, color.NRGBA{30, 100, 200, 255})
		}
	}
	out, err := CompositeEmotion(source, generated, EmotionRegion{X: 16, Y: 16, Width: 32, Height: 32}, EmotionRegion{X: 24, Y: 24, Width: 16, Height: 16})
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds() != source.Bounds() || out.NRGBAAt(0, 0) != source.NRGBAAt(0, 0) || out.NRGBAAt(16, 16) != source.NRGBAAt(16, 16) {
		t.Fatal("whole image overwritten")
	}
	if out.NRGBAAt(32, 32).B < 150 || source.NRGBAAt(32, 32).B != 30 {
		t.Fatal("face missing or source mutated")
	}
	for _, r := range []EmotionRegion{{X: math.NaN(), Width: 1, Height: 1}, {Width: -1, Height: 1}, {Width: 1, Height: math.Inf(1)}} {
		if _, err = CompositeEmotion(source, generated, r, EmotionRegion{Width: 1, Height: 1}); err == nil {
			t.Fatal("invalid region accepted")
		}
	}
}
