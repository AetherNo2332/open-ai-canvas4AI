package tasks

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

type EmotionRegion struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type EmotionContext struct {
	SourceResourceID string        `json:"sourceResourceId"`
	EditRegion       EmotionRegion `json:"editRegion"`
	FaceBox          EmotionRegion `json:"faceBox"`
}

func validRegion(r EmotionRegion) bool {
	for _, v := range []float64{r.X, r.Y, r.Width, r.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return r.Width > 0 && r.Height > 0
}
func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Port of Canary's ellipse, edge color gains and smoothstep feather blend.
// Generated crop scaling uses bilinear sampling; pixels outside the ellipse
// remain byte-for-byte identical to the source, without mutating either input.
func CompositeEmotion(source, generated image.Image, region, face EmotionRegion) (*image.NRGBA, error) {
	if source == nil || generated == nil || !validRegion(region) || !validRegion(face) {
		return nil, fmt.Errorf("invalid emotion edit region")
	}
	sb, gb := source.Bounds(), generated.Bounds()
	w, h := sb.Dx(), sb.Dy()
	if w < 1 || h < 1 || int64(w)*int64(h) > 32_000_000 || gb.Dx() < 1 || gb.Dy() < 1 {
		return nil, fmt.Errorf("emotion image exceeds pixel limit")
	}
	region.X = clamp(math.Round(region.X), 0, float64(w-1))
	region.Y = clamp(math.Round(region.Y), 0, float64(h-1))
	region.Width = clamp(math.Round(region.Width), 1, float64(w)-region.X)
	region.Height = clamp(math.Round(region.Height), 1, float64(h)-region.Y)
	face.X = clamp(face.X, 0, float64(w-1))
	face.Y = clamp(face.Y, 0, float64(h-1))
	face.Width = clamp(face.Width, 1, float64(w)-face.X)
	face.Height = clamp(face.Height, 1, float64(h)-face.Y)
	rw, rh := int(region.Width), int(region.Height)
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	crop := image.NewNRGBA(image.Rect(0, 0, rw, rh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(source.At(sb.Min.X+x, sb.Min.Y+y)).(color.NRGBA))
		}
	}
	for y := 0; y < rh; y++ {
		for x := 0; x < rw; x++ {
			crop.SetNRGBA(x, y, bilinear(generated, (float64(x)+0.5)*float64(gb.Dx())/float64(rw)-0.5, (float64(y)+0.5)*float64(gb.Dy())/float64(rh)-0.5))
		}
	}
	cx := face.X - region.X + face.Width/2
	cy := face.Y - region.Y - face.Height*0.28 + face.Height*1.48/2
	rx, ry := math.Max(1, face.Width*1.44/2), math.Max(1, face.Height*1.48/2)
	distance := func(x, y int) float64 { return math.Hypot((float64(x)+0.5-cx)/rx, (float64(y)+0.5-cy)/ry) }
	var ss, gs [3]float64
	count := 0
	for y := 0; y < rh; y++ {
		for x := 0; x < rw; x++ {
			d := distance(x, y)
			if d < 0.72 || d > 0.96 {
				continue
			}
			s, g := out.NRGBAAt(int(region.X)+x, int(region.Y)+y), crop.NRGBAAt(x, y)
			ss[0] += float64(s.R)
			ss[1] += float64(s.G)
			ss[2] += float64(s.B)
			gs[0] += float64(g.R)
			gs[1] += float64(g.G)
			gs[2] += float64(g.B)
			count++
		}
	}
	gains := [3]float64{1, 1, 1}
	if count >= 32 {
		for i := range gains {
			gains[i] = clamp(ss[i]/math.Max(1, gs[i]), 0.78, 1.22)
		}
	}
	for y := 0; y < rh; y++ {
		for x := 0; x < rw; x++ {
			d := distance(x, y)
			if d >= 1 {
				continue
			}
			alpha := 1.0
			if d > 0.7 {
				t := (d - 0.7) / 0.3
				alpha = 1 - t*t*(3-2*t)
			}
			sx, sy := int(region.X)+x, int(region.Y)+y
			s, g := out.NRGBAAt(sx, sy), crop.NRGBAAt(x, y)
			sourceRGB := [3]uint8{s.R, s.G, s.B}
			genRGB := [3]uint8{g.R, g.G, g.B}
			var rgb [3]uint8
			for i := range rgb {
				gain := 1 + (gains[i]-1)*(0.42+(1-alpha)*0.58)
				rgb[i] = uint8(math.Round(float64(sourceRGB[i])*(1-alpha) + clamp(float64(genRGB[i])*gain, 0, 255)*alpha))
			}
			out.SetNRGBA(sx, sy, color.NRGBA{rgb[0], rgb[1], rgb[2], uint8(math.Round(float64(s.A)*(1-alpha) + float64(g.A)*alpha))})
		}
	}
	return out, nil
}
func bilinear(img image.Image, x, y float64) color.NRGBA {
	b := img.Bounds()
	x = clamp(x, 0, float64(b.Dx()-1))
	y = clamp(y, 0, float64(b.Dy()-1))
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	x1, y1 := min(x0+1, b.Dx()-1), min(y0+1, b.Dy()-1)
	fx, fy := x-float64(x0), y-float64(y0)
	coords := [][2]int{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}}
	weights := []float64{(1 - fx) * (1 - fy), fx * (1 - fy), (1 - fx) * fy, fx * fy}
	values := [4]float64{}
	for i, c := range coords {
		pixel := color.NRGBAModel.Convert(img.At(b.Min.X+c[0], b.Min.Y+c[1])).(color.NRGBA)
		for j, v := range []uint8{pixel.R, pixel.G, pixel.B, pixel.A} {
			values[j] += float64(v) * weights[i]
		}
	}
	return color.NRGBA{uint8(math.Round(values[0])), uint8(math.Round(values[1])), uint8(math.Round(values[2])), uint8(math.Round(values[3]))}
}
