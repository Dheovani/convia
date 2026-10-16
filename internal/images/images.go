/*
Package images knows the pictures Convia shows in a conversation — PNG, JPEG, GIF and WebP — and removes
what they record beyond the picture before one is sent (ADR 0036): the place and the camera a photograph
records, and any text it carries. It keeps what drawing the picture needs, and reads the orientation a
photograph records before that record goes, so the interface can still turn it.
*/
package images

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// The types of picture shown in a conversation.
const (
	PNG  = "image/png"
	JPEG = "image/jpeg"
	GIF  = "image/gif"
	WebP = "image/webp"
)

var ErrMalformed = errors.New("images: not a picture this package reads")

// Type is the picture type data holds, by its first bytes, or "" if it is none of them.
func Type(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return PNG
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return JPEG
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return GIF
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && string(data[8:12]) == "WEBP":
		return WebP
	}
	return ""
}

/*
Clean is the picture in data without what it records beyond the picture, and the orientation it
recorded: 1 is upright, and 2 to 8 are the turns and mirrors of the EXIF standard.
*/
func Clean(data []byte) (cleaned []byte, orientation int, err error) {
	switch Type(data) {
	case PNG:
		return cleanPNG(data)
	case JPEG:
		return cleanJPEG(data)
	case GIF:
		cleaned, err = cleanGIF(data)
		return cleaned, 1, err
	case WebP:
		return cleanWebP(data)
	}
	return nil, 0, ErrMalformed
}

/*
exifOrientation reads the orientation from TIFF-structured EXIF data, or 1 when it records none. The
orientation is tag 0x0112 of the first image directory, a 16-bit number from 1 to 8.
*/
func exifOrientation(tiff []byte) int {
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(tiff, []byte("II*\x00")):
		order = binary.LittleEndian
	case bytes.HasPrefix(tiff, []byte("MM\x00*")):
		order = binary.BigEndian
	default:
		return 1
	}
	if len(tiff) < 8 {
		return 1
	}
	directory := int64(order.Uint32(tiff[4:8]))
	if directory+2 > int64(len(tiff)) {
		return 1
	}
	count := int64(order.Uint16(tiff[directory:]))
	for i := int64(0); i < count; i++ {
		entry := directory + 2 + i*12
		if entry+12 > int64(len(tiff)) {
			return 1
		}
		if order.Uint16(tiff[entry:]) == 0x0112 && order.Uint16(tiff[entry+2:]) == 3 {
			if o := int(order.Uint16(tiff[entry+8:])); o >= 1 && o <= 8 {
				return o
			}
		}
	}
	return 1
}

// exifPrefix starts EXIF data as JPEG and WebP carry it, before its TIFF structure.
var exifPrefix = []byte("Exif\x00\x00")

func cleanPNG(data []byte) ([]byte, int, error) {
	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write(data[:8])
	orientation := 1
	for at := 8; ; {
		if at+12 > len(data) {
			return nil, 0, ErrMalformed
		}
		size := int(binary.BigEndian.Uint32(data[at:]))
		kind := string(data[at+4 : at+8])
		end := at + 12 + size
		if size < 0 || end > len(data) || end < at {
			return nil, 0, ErrMalformed
		}
		switch kind {
		case "eXIf":
			orientation = exifOrientation(data[at+8 : at+8+size])
		case "tEXt", "zTXt", "iTXt", "tIME":
		default:
			out.Write(data[at:end])
		}
		if kind == "IEND" {
			return out.Bytes(), orientation, nil
		}
		at = end
	}
}

func cleanJPEG(data []byte) ([]byte, int, error) {
	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write(data[:2])
	orientation := 1
	for at := 2; ; {
		if at+2 > len(data) || data[at] != 0xFF {
			return nil, 0, ErrMalformed
		}
		marker := data[at+1]
		switch {
		case marker == 0xFF:
			// A fill byte before a marker.
			at++
			continue
		case marker == 0x01 || marker >= 0xD0 && marker <= 0xD7:
			out.Write(data[at : at+2])
			at += 2
			continue
		case marker == 0xD9:
			out.Write(data[at : at+2])
			return out.Bytes(), orientation, nil
		}
		if at+4 > len(data) {
			return nil, 0, ErrMalformed
		}
		size := int(binary.BigEndian.Uint16(data[at+2:]))
		end := at + 2 + size
		if size < 2 || end > len(data) {
			return nil, 0, ErrMalformed
		}
		payload := data[at+4 : end]
		if marker == 0xDA {
			// The scan, and everything after it, is the picture itself.
			out.Write(data[at:])
			return out.Bytes(), orientation, nil
		}
		switch {
		case marker == 0xE1:
			if bytes.HasPrefix(payload, exifPrefix) {
				orientation = exifOrientation(payload[len(exifPrefix):])
			}
		case marker == 0xE0,
			// The colour profile, and Adobe's note of how the colours were encoded, are part of drawing it.
			marker == 0xE2 && bytes.HasPrefix(payload, []byte("ICC_PROFILE\x00")),
			marker == 0xEE && bytes.HasPrefix(payload, []byte("Adobe")):
			out.Write(data[at:end])
		case marker >= 0xE0 && marker <= 0xEF, marker == 0xFE:
		default:
			out.Write(data[at:end])
		}
		at = end
	}
}

func cleanGIF(data []byte) ([]byte, error) {
	if len(data) < 13 {
		return nil, ErrMalformed
	}
	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	at := 13
	if flags := data[10]; flags&0x80 != 0 {
		at += 3 << ((flags & 0x07) + 1)
	}
	if at > len(data) {
		return nil, ErrMalformed
	}
	out.Write(data[:at])

	// blocks returns where the sub-blocks starting at from end, after their terminator.
	blocks := func(from int) (int, error) {
		for from < len(data) {
			size := int(data[from])
			from++
			if size == 0 {
				return from, nil
			}
			from += size
		}
		return 0, ErrMalformed
	}

	for at < len(data) {
		switch data[at] {
		case 0x3B:
			out.WriteByte(0x3B)
			return out.Bytes(), nil
		case 0x21:
			if at+2 > len(data) {
				return nil, ErrMalformed
			}
			label := data[at+1]
			end, err := blocks(at + 2)
			if err != nil {
				return nil, err
			}
			keep := label != 0xFE
			if label == 0xFF {
				// Of applications' extensions, only the one that makes an animation loop is part of the picture.
				keep = end > at+14 && data[at+2] == 11 &&
					(string(data[at+3:at+14]) == "NETSCAPE2.0" || string(data[at+3:at+14]) == "ANIMEXTS1.0")
			}
			if keep {
				out.Write(data[at:end])
			}
			at = end
		case 0x2C:
			if at+10 > len(data) {
				return nil, ErrMalformed
			}
			start := at
			at += 10
			if flags := data[start+9]; flags&0x80 != 0 {
				at += 3 << ((flags & 0x07) + 1)
			}
			// The image's smallest code size, then its data.
			at++
			if at > len(data) {
				return nil, ErrMalformed
			}
			end, err := blocks(at)
			if err != nil {
				return nil, err
			}
			out.Write(data[start:end])
			at = end
		default:
			return nil, ErrMalformed
		}
	}
	return nil, ErrMalformed
}

func cleanWebP(data []byte) ([]byte, int, error) {
	// A file cut short ends between its chunks as easily as within one: the size it states says which.
	if int64(binary.LittleEndian.Uint32(data[4:]))+8 != int64(len(data)) {
		return nil, 0, ErrMalformed
	}
	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write(data[:12])
	orientation := 1
	for at := 12; at < len(data); {
		if at+8 > len(data) {
			return nil, 0, ErrMalformed
		}
		kind := string(data[at : at+4])
		size := int(binary.LittleEndian.Uint32(data[at+4:]))
		end := at + 8 + size + size%2
		if size < 0 || end > len(data) || end < at {
			return nil, 0, ErrMalformed
		}
		switch kind {
		case "EXIF":
			payload := bytes.TrimPrefix(data[at+8:at+8+size], exifPrefix)
			orientation = exifOrientation(payload)
		case "XMP ":
		case "VP8X":
			chunk := bytes.Clone(data[at:end])
			if len(chunk) > 8 {
				// The flags that announce EXIF and XMP, which are no longer there.
				chunk[8] &^= 0x08 | 0x04
			}
			out.Write(chunk)
		default:
			out.Write(data[at:end])
		}
		at = end
	}
	cleaned := out.Bytes()
	binary.LittleEndian.PutUint32(cleaned[4:], uint32(len(cleaned)-8))
	return cleaned, orientation, nil
}
