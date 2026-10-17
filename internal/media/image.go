package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	JPEG = "image/jpeg"
	PNG  = "image/png"
)

var (
	ErrTooLarge      = errors.New("media: compressed image is too large")
	ErrUnsupported   = errors.New("media: unsupported image format")
	ErrInvalid       = errors.New("media: invalid image")
	ErrDimensions    = errors.New("media: image dimensions exceed limits")
	ErrTrailingData  = errors.New("media: trailing data is not allowed")
	ErrInvalidConfig = errors.New("media: invalid configuration")
)

// Limits bounds both compressed input and decoded image work.
type Limits struct {
	MaxBytes    int64
	MaxWidth    int
	MaxHeight   int
	MaxPixels   int64
	JPEGQuality int
}

func DefaultLimits() Limits {
	return Limits{MaxBytes: 10 << 20, MaxWidth: 10_000, MaxHeight: 10_000, MaxPixels: 40_000_000, JPEGQuality: 90}
}

type Image struct {
	Data      []byte
	MediaType string
	Filename  string
	Width     int
	Height    int
	SHA256    [32]byte
}

func (i Image) Size() int64 { return int64(len(i.Data)) }

type Processor struct{ Limits Limits }

// Normalize consumes at most MaxBytes+1 compressed bytes, validates dimensions
// before full decode, then re-encodes the pixels to strip ancillary metadata.
func (p Processor) Normalize(ctx context.Context, r io.Reader, filename string) (Image, error) {
	if err := p.Limits.validate(); err != nil {
		return Image{}, err
	}
	if err := ctx.Err(); err != nil {
		return Image{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(r, p.Limits.MaxBytes+1))
	if err != nil {
		return Image{}, fmt.Errorf("read image: %w", err)
	}
	if int64(len(raw)) > p.Limits.MaxBytes {
		return Image{}, ErrTooLarge
	}
	mediaType, err := sniff(raw)
	if err != nil {
		return Image{}, err
	}
	if !hasExactEnd(raw, mediaType) {
		return Image{}, ErrTrailingData
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != formatName(mediaType) {
		return Image{}, fmt.Errorf("%w: decode config", ErrInvalid)
	}
	if !p.Limits.accepts(cfg.Width, cfg.Height) {
		return Image{}, ErrDimensions
	}
	if err := ctx.Err(); err != nil {
		return Image{}, err
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(raw))
	if err != nil || decodedFormat != format {
		return Image{}, fmt.Errorf("%w: decode", ErrInvalid)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != cfg.Width || bounds.Dy() != cfg.Height || !p.Limits.accepts(bounds.Dx(), bounds.Dy()) {
		return Image{}, ErrDimensions
	}
	var out bytes.Buffer
	switch mediaType {
	case JPEG:
		err = jpeg.Encode(&out, decoded, &jpeg.Options{Quality: p.Limits.JPEGQuality})
	case PNG:
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		err = enc.Encode(&out, decoded)
	}
	if err != nil {
		return Image{}, fmt.Errorf("encode image: %w", err)
	}
	if int64(out.Len()) > p.Limits.MaxBytes {
		return Image{}, ErrTooLarge
	}
	data := out.Bytes()
	return Image{
		Data: data, MediaType: mediaType, Filename: SafeFilename(filename, mediaType),
		Width: cfg.Width, Height: cfg.Height, SHA256: sha256.Sum256(data),
	}, nil
}

func (l Limits) validate() error {
	if l.MaxBytes <= 0 || l.MaxWidth <= 0 || l.MaxHeight <= 0 || l.MaxPixels <= 0 || l.JPEGQuality < 1 || l.JPEGQuality > 100 {
		return ErrInvalidConfig
	}
	return nil
}

func (l Limits) accepts(width, height int) bool {
	if width <= 0 || height <= 0 || width > l.MaxWidth || height > l.MaxHeight {
		return false
	}
	return int64(width) <= l.MaxPixels/int64(height)
}

func sniff(data []byte) (string, error) {
	switch {
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{0xff, 0xd8, 0xff}):
		return JPEG, nil
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return PNG, nil
	default:
		return "", ErrUnsupported
	}
}

func hasExactEnd(data []byte, mediaType string) bool {
	if mediaType == JPEG {
		return jpegHasExactEnd(data)
	}
	// Walk the chunk framing so an attacker cannot append another fake IEND
	// after the decoder's real end marker.
	for offset := 8; offset+12 <= len(data); {
		length := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		next := uint64(offset) + 12 + length
		if next > uint64(len(data)) {
			return false
		}
		if bytes.Equal(data[offset+4:offset+8], []byte("IEND")) {
			return length == 0 && next == uint64(len(data))
		}
		offset = int(next)
	}
	return false
}

func jpegHasExactEnd(data []byte) bool {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return false
	}
	pos := 2
	for pos < len(data) {
		if data[pos] != 0xff {
			return false
		}
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos >= len(data) {
			return false
		}
		marker := data[pos]
		pos++
		switch {
		case marker == 0xd9:
			return pos == len(data)
		case marker == 0xd8 || marker == 0x00:
			return false
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			continue
		}
		if pos+2 > len(data) {
			return false
		}
		length := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if length < 2 || pos+length > len(data) {
			return false
		}
		pos += length
		if marker != 0xda {
			continue
		}
		// Scan entropy-coded bytes. FF00 is stuffed data and RST markers are
		// in-band; any other marker resumes the outer marker parser.
		for pos < len(data) {
			if data[pos] != 0xff {
				pos++
				continue
			}
			candidate := pos
			for pos < len(data) && data[pos] == 0xff {
				pos++
			}
			if pos >= len(data) {
				return false
			}
			next := data[pos]
			if next == 0x00 || (next >= 0xd0 && next <= 0xd7) {
				pos++
				continue
			}
			pos = candidate
			break
		}
	}
	return false
}

func formatName(mediaType string) string {
	if mediaType == JPEG {
		return "jpeg"
	}
	return "png"
}

// SafeFilename removes paths, controls, bidi formatting, and misleading
// extensions. It is download metadata only and is never used as a disk path.
func SafeFilename(name, mediaType string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	ext := ".png"
	if mediaType == JPEG {
		ext = ".jpg"
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	base = strings.Trim(strings.TrimSpace(base), ".")
	if base == "" {
		base = "image"
	}
	for len(base)+len(ext) > 255 {
		_, size := utf8.DecodeLastRuneInString(base)
		base = base[:len(base)-size]
	}
	return base + ext
}

// Extension is the canonical on-disk extension for a normalized media type.
func Extension(mediaType string) (string, error) {
	if mediaType == JPEG {
		return ".jpg", nil
	}
	if mediaType == PNG {
		return ".png", nil
	}
	return "", ErrUnsupported
}
