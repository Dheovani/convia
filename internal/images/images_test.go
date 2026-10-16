package images

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// secret is what metadata could betray: none of it may survive cleaning.
const secret = "GPS -23.5505 -46.6333 Pixel 9 Pro"

func picture() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := range 6 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(30 * x), G: uint8(40 * y), B: 128, A: 255})
		}
	}
	return img
}

// tiff is EXIF data recording an orientation, and something of where it was taken.
func tiff(orientation uint16, order binary.ByteOrder) []byte {
	var b bytes.Buffer
	if order == binary.LittleEndian {
		b.WriteString("II*\x00")
	} else {
		b.WriteString("MM\x00*")
	}
	binary.Write(&b, order, uint32(8))
	binary.Write(&b, order, uint16(2))
	// Orientation, a SHORT.
	binary.Write(&b, order, uint16(0x0112))
	binary.Write(&b, order, uint16(3))
	binary.Write(&b, order, uint32(1))
	binary.Write(&b, order, orientation)
	binary.Write(&b, order, uint16(0))
	// The camera's make, an ASCII string after the directory.
	binary.Write(&b, order, uint16(0x010F))
	binary.Write(&b, order, uint16(2))
	binary.Write(&b, order, uint32(len(secret)))
	binary.Write(&b, order, uint32(8+2+2*12+4))
	binary.Write(&b, order, uint32(0))
	b.WriteString(secret)
	return b.Bytes()
}

func pngChunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(kind)
	b.Write(data)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	return b.Bytes()
}

func samplePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, picture()); err != nil {
		t.Fatal(err)
	}
	encoded := b.Bytes()
	// After the signature and the header chunk: a text, a time, and EXIF.
	header := 8 + 12 + 13
	var out bytes.Buffer
	out.Write(encoded[:header])
	out.Write(pngChunk("tEXt", []byte("Comment\x00"+secret)))
	out.Write(pngChunk("iTXt", []byte("Location\x00\x00\x00\x00\x00"+secret)))
	out.Write(pngChunk("tIME", []byte{0x07, 0xEA, 10, 16, 12, 0, 0}))
	out.Write(pngChunk("eXIf", tiff(6, binary.BigEndian)))
	out.Write(encoded[header:])
	return out.Bytes()
}

func jpegSegment(marker byte, data []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, marker})
	binary.Write(&b, binary.BigEndian, uint16(len(data)+2))
	b.Write(data)
	return b.Bytes()
}

func sampleJPEG(t *testing.T, orientation uint16) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, picture(), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	encoded := b.Bytes()
	var out bytes.Buffer
	out.Write(encoded[:2])
	out.Write(jpegSegment(0xE1, append(append([]byte{}, exifPrefix...), tiff(orientation, binary.LittleEndian)...)))
	out.Write(jpegSegment(0xE1, []byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>"+secret+"</x:xmpmeta>")))
	out.Write(jpegSegment(0xE2, []byte("ICC_PROFILE\x00\x01\x01 a colour profile")))
	out.Write(jpegSegment(0xED, []byte("Photoshop 3.0\x00"+secret)))
	out.Write(jpegSegment(0xFE, []byte(secret)))
	out.Write(encoded[2:])
	return out.Bytes()
}

func sampleGIF(t *testing.T) []byte {
	t.Helper()
	frame := image.NewPaletted(image.Rect(0, 0, 8, 6), palette.Plan9)
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}, LoopCount: 0}); err != nil {
		t.Fatal(err)
	}
	encoded := b.Bytes()
	// Before the trailer: a comment, and an application's XMP.
	var out bytes.Buffer
	out.Write(encoded[:len(encoded)-1])
	out.Write([]byte{0x21, 0xFE, byte(len(secret))})
	out.WriteString(secret)
	out.WriteByte(0)
	out.Write([]byte{0x21, 0xFF, 11})
	out.WriteString("XMP DataXMP")
	out.WriteByte(byte(len(secret)))
	out.WriteString(secret)
	out.WriteByte(0)
	out.WriteByte(0x3B)
	return out.Bytes()
}

func webpChunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	b.WriteString(kind)
	binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	if len(data)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func sampleWebP() []byte {
	var body bytes.Buffer
	body.Write(webpChunk("VP8X", []byte{0x08 | 0x04 | 0x10, 0, 0, 0, 7, 0, 0, 5, 0, 0}))
	body.Write(webpChunk("VP8L", []byte("the picture itself")))
	body.Write(webpChunk("EXIF", append(append([]byte{}, exifPrefix...), tiff(8, binary.BigEndian)...)))
	body.Write(webpChunk("XMP ", []byte("<x:xmpmeta>"+secret+"</x:xmpmeta>")))
	var out bytes.Buffer
	out.WriteString("RIFF")
	binary.Write(&out, binary.LittleEndian, uint32(4+body.Len()))
	out.WriteString("WEBP")
	out.Write(body.Bytes())
	return out.Bytes()
}

func mustClean(t *testing.T, data []byte) ([]byte, int) {
	t.Helper()
	cleaned, orientation, err := Clean(data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cleaned, []byte(secret)) || bytes.Contains(cleaned, []byte("GPS")) {
		t.Fatalf("what it recorded survived cleaning")
	}
	return cleaned, orientation
}

func TestEachTypeIsKnownByItsFirstBytes(t *testing.T) {
	for want, data := range map[string][]byte{
		PNG: samplePNG(t), JPEG: sampleJPEG(t, 1), GIF: sampleGIF(t), WebP: sampleWebP(),
		"": []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"),
	} {
		if got := Type(data); got != want {
			t.Errorf("Type = %q, want %q", got, want)
		}
	}
}

func TestAPNGLosesItsTextAndKeepsItsPicture(t *testing.T) {
	cleaned, orientation := mustClean(t, samplePNG(t))
	if orientation != 6 {
		t.Fatalf("orientation %d, want 6", orientation)
	}
	img, err := png.Decode(bytes.NewReader(cleaned))
	if err != nil || img.Bounds() != picture().Bounds() {
		t.Fatalf("the cleaned PNG decodes to %v, %v", img, err)
	}
	for _, kind := range []string{"tEXt", "iTXt", "tIME", "eXIf"} {
		if bytes.Contains(cleaned, []byte(kind)) {
			t.Errorf("%s survived", kind)
		}
	}
}

func TestAJPEGLosesItsRecordsAndKeepsItsColours(t *testing.T) {
	for _, orientation := range []uint16{1, 3, 6, 8} {
		cleaned, got := mustClean(t, sampleJPEG(t, orientation))
		if got != int(orientation) {
			t.Fatalf("orientation %d, want %d", got, orientation)
		}
		img, err := jpeg.Decode(bytes.NewReader(cleaned))
		if err != nil || img.Bounds() != picture().Bounds() {
			t.Fatalf("the cleaned JPEG decodes to %v, %v", img, err)
		}
		if !bytes.Contains(cleaned, []byte("ICC_PROFILE")) {
			t.Fatal("the colour profile was removed")
		}
		if bytes.Contains(cleaned, exifPrefix) || bytes.Contains(cleaned, []byte("xmpmeta")) || bytes.Contains(cleaned, []byte("Photoshop")) {
			t.Fatal("a record survived")
		}
	}
}

func TestAGIFLosesItsCommentsAndKeepsItsAnimation(t *testing.T) {
	cleaned, orientation := mustClean(t, sampleGIF(t))
	if orientation != 1 {
		t.Fatalf("orientation %d", orientation)
	}
	animation, err := gif.DecodeAll(bytes.NewReader(cleaned))
	if err != nil || len(animation.Image) != 2 || animation.LoopCount != 0 {
		t.Fatalf("the cleaned GIF decodes to %v, %v", animation, err)
	}
	if !bytes.Contains(cleaned, []byte("NETSCAPE2.0")) {
		t.Fatal("the loop was removed")
	}
}

func TestAWebPLosesItsRecordsAndSaysSo(t *testing.T) {
	cleaned, orientation := mustClean(t, sampleWebP())
	if orientation != 8 {
		t.Fatalf("orientation %d, want 8", orientation)
	}
	if bytes.Contains(cleaned, []byte("EXIF")) || bytes.Contains(cleaned, []byte("XMP ")) {
		t.Fatal("a record survived")
	}
	if !bytes.Contains(cleaned, []byte("the picture itself")) {
		t.Fatal("the picture was removed")
	}
	if size := binary.LittleEndian.Uint32(cleaned[4:]); int(size) != len(cleaned)-8 {
		t.Fatalf("RIFF says %d bytes for %d", size, len(cleaned)-8)
	}
	if flags := cleaned[20]; flags&(0x08|0x04) != 0 || flags&0x10 == 0 {
		t.Fatalf("VP8X flags are %08b", flags)
	}
}

func TestWhatIsNotAPictureIsRefused(t *testing.T) {
	cases := [][]byte{
		[]byte("not a picture"),
		samplePNG(t)[:40],
		sampleJPEG(t, 1)[:30],
		sampleGIF(t)[:20],
		sampleWebP()[:30],
	}
	for i, data := range cases {
		if _, _, err := Clean(data); err == nil {
			t.Errorf("case %d was cleaned", i)
		}
	}
}

func TestAnUnusualOrientationIsUpright(t *testing.T) {
	if got := exifOrientation(tiff(9, binary.BigEndian)); got != 1 {
		t.Fatalf("orientation 9 read as %d", got)
	}
	if got := exifOrientation([]byte("II*\x00\xFF\xFF\xFF\xFF")); got != 1 {
		t.Fatalf("a directory past the end read as %d", got)
	}
}

func FuzzClean(f *testing.F) {
	t := &testing.T{}
	for _, seed := range [][]byte{samplePNG(t), sampleJPEG(t, 6), sampleGIF(t), sampleWebP()} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		cleaned, orientation, err := Clean(data)
		if err != nil {
			return
		}
		if orientation < 1 || orientation > 8 {
			t.Fatalf("orientation %d", orientation)
		}
		if len(cleaned) > len(data)+1 {
			t.Fatalf("cleaning grew %d bytes into %d", len(data), len(cleaned))
		}
		if Type(cleaned) != Type(data) {
			t.Fatal("cleaning changed the type")
		}
	})
}
