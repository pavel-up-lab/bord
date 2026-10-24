package terminalui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"sync"

	sixel "github.com/mattn/go-sixel"
)

const (
	sixelCellWidth    = 8
	sixelCellHeight   = 16
	maxSixelWidth     = 1200
	maxSixelHeight    = 800
	maxSixelPayload   = 4 << 20
	maxSixelDimension = 6000
)

type SixelFrame struct {
	Control       string
	Lines         []string
	Columns, Rows int
}

type SixelRenderer struct {
	mu    sync.Mutex
	cache sixelCache
}

type sixelCache struct {
	attachment    string
	columns, rows int
	generation    uint64
	frame         SixelFrame
}

// Render returns a DCS payload followed by the text rows the TUI must reserve.
// Results are cached for a session generation so ordinary Bubble Tea updates
// do not repeatedly quantize and encode the same image.
func (r *SixelRenderer) Render(data []byte, attachment string, columns, rows int, generation uint64) (SixelFrame, error) {
	if r == nil || attachment == "" || generation == 0 {
		return SixelFrame{}, errors.New("invalid Sixel frame identity")
	}
	columns = min(max(0, columns), maxSixelWidth/sixelCellWidth)
	rows = min(max(0, rows), maxSixelHeight/sixelCellHeight)
	if columns == 0 || rows == 0 {
		return SixelFrame{}, errors.New("invalid Sixel image area")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache.attachment == attachment && r.cache.columns == columns && r.cache.rows == rows && r.cache.generation == generation {
		return cloneSixelFrame(r.cache.frame), nil
	}
	frame, err := encodeSixelFrame(data, columns, rows)
	if err != nil {
		return SixelFrame{}, err
	}
	r.cache = sixelCache{attachment: attachment, columns: columns, rows: rows, generation: generation, frame: cloneSixelFrame(frame)}
	return frame, nil
}

func encodeSixelFrame(data []byte, columns, rows int) (SixelFrame, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > maxSixelDimension || config.Height > maxSixelDimension || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return SixelFrame{}, errors.New("invalid Sixel image dimensions")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return SixelFrame{}, errors.New("decode Sixel image")
	}
	maxWidth, maxHeight := min(columns*sixelCellWidth, maxSixelWidth), min(rows*sixelCellHeight, maxSixelHeight)
	scale := minFloat(float64(maxWidth)/float64(config.Width), float64(maxHeight)/float64(config.Height))
	width := max(1, min(maxWidth, int(float64(config.Width)*scale)))
	height := max(1, min(maxHeight, int(float64(config.Height)*scale)))
	resized := scaleImageNearest(source, width, height)

	var encoded limitedBuffer
	encoded.limit = maxSixelPayload
	encoder := sixel.NewEncoder(&encoded)
	encoder.Colors = 256
	encoder.Transparent = true
	if err := encoder.Encode(resized); err != nil {
		return SixelFrame{}, fmt.Errorf("encode Sixel image: %w", err)
	}
	if !bytes.HasPrefix(encoded.Bytes(), []byte("\x1bP")) || !bytes.HasSuffix(encoded.Bytes(), []byte("\x1b\\")) {
		return SixelFrame{}, errors.New("invalid Sixel framing")
	}
	frameColumns := min(columns, (width+sixelCellWidth-1)/sixelCellWidth)
	frameRows := min(rows, (height+sixelCellHeight-1)/sixelCellHeight)
	lines := make([]string, frameRows)
	for index := range lines {
		lines[index] = strings.Repeat(" ", frameColumns)
	}
	return SixelFrame{Control: encoded.String(), Lines: lines, Columns: frameColumns, Rows: frameRows}, nil
}

func scaleImageNearest(source image.Image, width, height int) *image.RGBA {
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	bounds := source.Bounds()
	for y := range height {
		sourceY := bounds.Min.Y + min(bounds.Dy()-1, y*bounds.Dy()/height)
		for x := range width {
			sourceX := bounds.Min.X + min(bounds.Dx()-1, x*bounds.Dx()/width)
			target.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return target
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		return 0, errors.New("Sixel payload exceeds limit")
	}
	return b.Buffer.Write(data)
}

func cloneSixelFrame(frame SixelFrame) SixelFrame {
	frame.Lines = append([]string(nil), frame.Lines...)
	return frame
}
