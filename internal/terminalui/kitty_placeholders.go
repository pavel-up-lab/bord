package terminalui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"strings"
)

const (
	kittyPlaceholder = '\U0010eeee'
	maxKittyCells    = 256

	// The first 256 entries from Kitty's published rowcolumn-diacritics.txt.
	// Keeping the protocol table here avoids locale/font-dependent generation.
	kittyDiacritics = "\u0305\u030d\u030e\u0310\u0312\u033d\u033e\u033f\u0346\u034a\u034b\u034c\u0350\u0351\u0352\u0357\u035b\u0363\u0364\u0365\u0366\u0367\u0368\u0369\u036a\u036b\u036c\u036d\u036e\u036f\u0483\u0484" +
		"\u0485\u0486\u0487\u0592\u0593\u0594\u0595\u0597\u0598\u0599\u059c\u059d\u059e\u059f\u05a0\u05a1\u05a8\u05a9\u05ab\u05ac\u05af\u05c4\u0610\u0611\u0612\u0613\u0614\u0615\u0616\u0617\u0657\u0658" +
		"\u0659\u065a\u065b\u065d\u065e\u06d6\u06d7\u06d8\u06d9\u06da\u06db\u06dc\u06df\u06e0\u06e1\u06e2\u06e4\u06e7\u06e8\u06eb\u06ec\u0730\u0732\u0733\u0735\u0736\u073a\u073d\u073f\u0740\u0741\u0743" +
		"\u0745\u0747\u0749\u074a\u07eb\u07ec\u07ed\u07ee\u07ef\u07f0\u07f1\u07f3\u0816\u0817\u0818\u0819\u081b\u081c\u081d\u081e\u081f\u0820\u0821\u0822\u0823\u0825\u0826\u0827\u0829\u082a\u082b\u082c" +
		"\u082d\u0951\u0953\u0954\u0f82\u0f83\u0f86\u0f87\u135d\u135e\u135f\u17dd\u193a\u1a17\u1a75\u1a76\u1a77\u1a78\u1a79\u1a7a\u1a7b\u1a7c\u1b6b\u1b6d\u1b6e\u1b6f\u1b70\u1b71\u1b72\u1b73\u1cd0\u1cd1" +
		"\u1cd2\u1cda\u1cdb\u1ce0\u1dc0\u1dc1\u1dc3\u1dc4\u1dc5\u1dc6\u1dc7\u1dc8\u1dc9\u1dcb\u1dcc\u1dd1\u1dd2\u1dd3\u1dd4\u1dd5\u1dd6\u1dd7\u1dd8\u1dd9\u1dda\u1ddb\u1ddc\u1ddd\u1dde\u1ddf\u1de0\u1de1" +
		"\u1de2\u1de3\u1de4\u1de5\u1de6\u1dfe\u20d0\u20d1\u20d4\u20d5\u20d6\u20d7\u20db\u20dc\u20e1\u20e7\u20e9\u20f0\u2cef\u2cf0\u2cf1\u2de0\u2de1\u2de2\u2de3\u2de4\u2de5\u2de6\u2de7\u2de8\u2de9\u2dea" +
		"\u2deb\u2dec\u2ded\u2dee\u2def\u2df0\u2df1\u2df2\u2df3\u2df4\u2df5\u2df6\u2df7\u2df8\u2df9\u2dfa\u2dfb\u2dfc\u2dfd\u2dfe\u2dff\ua66f\ua67c\ua67d\ua6f0\ua6f1\ua8e0\ua8e1\ua8e2\ua8e3\ua8e4\ua8e5"
)

type KittyFrame struct {
	Control       string
	Lines         []string
	Delete        string
	Columns, Rows int
}

// RenderKittyFrame uploads a verified PNG without displaying it directly,
// creates a virtual placement, and returns ordinary text placeholder rows for
// Bubble Tea to position as part of its frame.
func RenderKittyFrame(pngData []byte, imageID uint32, columns, rows int) (KittyFrame, error) {
	if imageID == 0 || imageID > 0x00ffffff {
		return KittyFrame{}, errors.New("invalid Kitty image ID")
	}
	columns, rows = min(columns, maxKittyCells), min(rows, maxKittyCells)
	if columns <= 0 || rows <= 0 {
		return KittyFrame{}, errors.New("invalid Kitty image area")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(pngData))
	if err != nil || format != "png" || config.Width <= 0 || config.Height <= 0 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return KittyFrame{}, errors.New("invalid Kitty PNG")
	}

	frame := KittyFrame{Columns: columns, Rows: rows, Delete: KittyDeleteImage(imageID)}
	frame.Control = kittyUpload(pngData, imageID, columns, rows)
	diacritics := []rune(kittyDiacritics)
	red, green, blue := byte(imageID>>16), byte(imageID>>8), byte(imageID)
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", red, green, blue)
	frame.Lines = make([]string, rows)
	for row := range rows {
		var line strings.Builder
		line.Grow(len(color) + columns*9 + len("\x1b[39m"))
		line.WriteString(color)
		for column := range columns {
			line.WriteRune(kittyPlaceholder)
			line.WriteRune(diacritics[row])
			line.WriteRune(diacritics[column])
		}
		line.WriteString("\x1b[39m")
		frame.Lines[row] = line.String()
	}
	return frame, nil
}

func kittyUpload(pngData []byte, imageID uint32, columns, rows int) string {
	encoded := base64.StdEncoding.EncodeToString(pngData)
	const chunkSize = 4096
	var output strings.Builder
	for offset := 0; offset < len(encoded); offset += chunkSize {
		end := min(offset+chunkSize, len(encoded))
		more := 0
		if end < len(encoded) {
			more = 1
		}
		if offset == 0 {
			fmt.Fprintf(&output, "\x1b_Ga=T,f=100,i=%d,c=%d,r=%d,U=1,q=2,m=%d;%s\x1b\\", imageID, columns, rows, more, encoded[offset:end])
		} else {
			fmt.Fprintf(&output, "\x1b_Gm=%d;%s\x1b\\", more, encoded[offset:end])
		}
	}
	return output.String()
}

func KittyDeleteImage(imageID uint32) string {
	if imageID == 0 || imageID > 0x00ffffff {
		return ""
	}
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", imageID)
}
