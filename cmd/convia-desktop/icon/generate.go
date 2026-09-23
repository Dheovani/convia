//go:build ignore

/*
Draws Convia's mark as the application's icon.

	go run ./cmd/convia-desktop/icon/generate.go

The mark is an open ring with a point at the opening: a conversation that is
not closed, and somebody about to join it. The numbers below are the ones in
web/src/components/Mark.tsx and the colours are the ones in
web/src/styles/theme.css, so that what Windows shows in the taskbar and what
the interface draws on its own sign-in screen are the same mark. Changing it in
one place means running this again.

It is a generator rather than a committed picture alone because a mark that
cannot be redrawn is a mark nobody will ever change. The .ico it writes is
committed beside it, so that building the application needs none of this.
*/
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

/*
The mark, in the 32-unit box web/src/components/Mark.tsx draws it in.

The ring is open on the right, from 45 degrees round to 315, which leaves the
gap where the point sits.
*/
const (
	centre = 16.0
	radius = 10.0
	stroke = 3.5

	pointX = 26.0
	pointY = 16.0
	pointR = 3.0

	// The arc's ends, which are also where its round caps are centred.
	firstEndX, firstEndY   = 23.07, 23.07
	secondEndX, secondEndY = 23.07, 8.93

	// The gap: everything closer to straight right than this is not drawn.
	gap = math.Pi / 4
)

// The colours, from web/src/styles/theme.css. The tile is the deep surface, so
// that the mark reads on a light taskbar and a dark one alike.
var (
	tile  = color.NRGBA{R: 0x12, G: 0x12, B: 0x18, A: 0xff}
	ink   = color.NRGBA{R: 0xec, G: 0xed, B: 0xf2, A: 0xff}
	point = color.NRGBA{R: 0x6f, G: 0x4f, B: 0xf5, A: 0xff}
)

/*
How much of the tile the mark fills, and how round its corners are.

The mark is not centred in its own box — the point pushes it right — so what is
centred is what it actually covers rather than the box it was drawn in.
*/
const (
	fills   = 0.74
	rounded = 0.20

	/*
		Samples across one pixel, each way.

		The edges are all curves, and eight is enough that a sixteen-pixel icon
		has no stairs on them.
	*/
	samples = 8
)

// The sizes Windows asks for. The small ones are the ones people see most, and
// they are the ones a mark has to survive.
var sizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

func main() {
	working, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "locate the working directory:", err)
		os.Exit(1)
	}

	drawn := make([]*image.NRGBA, 0, len(sizes))
	for _, size := range sizes {
		drawn = append(drawn, draw(size))
	}

	rendered, err := pack(drawn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pack the icon:", err)
		os.Exit(1)
	}

	target := filepath.Join(working, "cmd", "convia-desktop", "icon", "convia.ico")
	if err := os.WriteFile(target, rendered, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write the icon:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", target, len(rendered), "bytes")
}

// draw renders the mark at one size.
func draw(size int) *image.NRGBA {
	picture := image.NewNRGBA(image.Rect(0, 0, size, size))

	/*
		What the mark covers in its own box, and what it is scaled to.

		The ring reaches half a stroke beyond its radius, and the point reaches
		further right than that, so the two decide the box between them.
	*/
	left := centre - radius - stroke/2
	right := math.Max(centre+radius+stroke/2, pointX+pointR)
	top := left
	bottom := centre + radius + stroke/2

	span := math.Max(right-left, bottom-top)
	scale := float64(size) * fills / span
	offsetX := float64(size)/2 - (left+right)/2*scale
	offsetY := float64(size)/2 - (top+bottom)/2*scale

	corner := float64(size) * rounded

	for y := range size {
		for x := range size {
			var tileHits, inkHits, pointHits int

			for subY := range samples {
				for subX := range samples {
					// The centre of one sub-pixel, in the picture.
					px := float64(x) + (float64(subX)+0.5)/samples
					py := float64(y) + (float64(subY)+0.5)/samples

					if insideTile(px, py, float64(size), corner) {
						tileHits++
					}

					// The same point, in the box the mark is drawn in.
					mx := (px - offsetX) / scale
					my := (py - offsetY) / scale

					switch {
					case insidePoint(mx, my):
						pointHits++
					case insideRing(mx, my):
						inkHits++
					}
				}
			}

			total := float64(samples * samples)
			pixel := blend(color.NRGBA{}, tile, float64(tileHits)/total)
			pixel = blend(pixel, ink, float64(inkHits)/total)
			pixel = blend(pixel, point, float64(pointHits)/total)
			picture.SetNRGBA(x, y, pixel)
		}
	}

	return picture
}

// insideTile is the rounded square the mark sits on.
func insideTile(x, y, size, corner float64) bool {
	dx := math.Max(math.Abs(x-size/2)-(size/2-corner), 0)
	dy := math.Max(math.Abs(y-size/2)-(size/2-corner), 0)
	return dx*dx+dy*dy <= corner*corner
}

// insideRing is the open ring, with a round cap at each of its ends.
func insideRing(x, y float64) bool {
	dx, dy := x-centre, y-centre
	distance := math.Hypot(dx, dy)

	if distance >= radius-stroke/2 && distance <= radius+stroke/2 {
		if math.Abs(math.Atan2(dy, dx)) >= gap {
			return true
		}
	}

	return math.Hypot(x-firstEndX, y-firstEndY) <= stroke/2 ||
		math.Hypot(x-secondEndX, y-secondEndY) <= stroke/2
}

func insidePoint(x, y float64) bool {
	return math.Hypot(x-pointX, y-pointY) <= pointR
}

// blend puts one colour over another, by how much of the pixel it covers.
func blend(under, over color.NRGBA, coverage float64) color.NRGBA {
	if coverage <= 0 {
		return under
	}

	alpha := coverage * float64(over.A) / 255
	result := float64(under.A)/255*(1-alpha) + alpha
	if result <= 0 {
		return color.NRGBA{}
	}

	mix := func(u, o uint8) uint8 {
		value := (float64(o)*alpha + float64(u)*float64(under.A)/255*(1-alpha)) / result
		return uint8(math.Round(math.Min(math.Max(value, 0), 255)))
	}

	return color.NRGBA{
		R: mix(under.R, over.R),
		G: mix(under.G, over.G),
		B: mix(under.B, over.B),
		A: uint8(math.Round(result * 255)),
	}
}

/*
pack writes the pictures as one .ico.

Everything is stored as PNG. Windows has read PNG entries at every size since
Vista, and the alternative — a bottom-up bitmap with a mask nothing uses — is
three times the bytes for a format this application will never run on.
*/
func pack(pictures []*image.NRGBA) ([]byte, error) {
	var body bytes.Buffer
	var directory bytes.Buffer

	// ICONDIR: reserved, type 1 (icon), count.
	_ = binary.Write(&directory, binary.LittleEndian, uint16(0))
	_ = binary.Write(&directory, binary.LittleEndian, uint16(1))
	_ = binary.Write(&directory, binary.LittleEndian, uint16(len(pictures)))

	offset := 6 + 16*len(pictures)

	for _, picture := range pictures {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, picture); err != nil {
			return nil, err
		}

		size := picture.Bounds().Dx()
		stored := byte(size)
		if size >= 256 {
			stored = 0 // 256 is written as zero, which is what makes it 256.
		}

		directory.WriteByte(stored) // width
		directory.WriteByte(stored) // height
		directory.WriteByte(0)      // colours in the palette: none
		directory.WriteByte(0)      // reserved

		_ = binary.Write(&directory, binary.LittleEndian, uint16(1))             // planes
		_ = binary.Write(&directory, binary.LittleEndian, uint16(32))            // bits per pixel
		_ = binary.Write(&directory, binary.LittleEndian, uint32(encoded.Len())) // bytes
		_ = binary.Write(&directory, binary.LittleEndian, uint32(offset))        // where

		offset += encoded.Len()
		body.Write(encoded.Bytes())
	}

	return append(directory.Bytes(), body.Bytes()...), nil
}
