package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func processor() Processor { return Processor{Limits: DefaultLimits()} }

func TestNormalizeValidAndStripsMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, media string }{
		{"valid.png", PNG}, {"valid.jpg", JPEG}, {"exif-gps.jpg", JPEG},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := fixture(t, test.name)
			got, err := processor().Normalize(context.Background(), bytes.NewReader(source), "../../photo.exe")
			if err != nil {
				t.Fatal(err)
			}
			if got.MediaType != test.media || got.Width != 2 || got.Height != 2 {
				t.Fatalf("unexpected metadata: %+v", got)
			}
			if strings.Contains(string(got.Data), "GPSLatitude") || strings.Contains(string(got.Data), "Exif") {
				t.Fatal("source metadata survived normalization")
			}
			if got.Filename != "photo"+map[string]string{PNG: ".png", JPEG: ".jpg"}[test.media] {
				t.Fatalf("unsafe name: %q", got.Filename)
			}
			again, err := processor().Normalize(context.Background(), bytes.NewReader(got.Data), got.Filename)
			if err != nil {
				t.Fatal(err)
			}
			if again.MediaType != got.MediaType || again.Width != got.Width || again.Height != got.Height {
				t.Fatal("normalized metadata is not stable")
			}
			sameSource, err := processor().Normalize(context.Background(), bytes.NewReader(source), "different-name.bin")
			if err != nil || sameSource.SHA256 != got.SHA256 {
				t.Fatal("normalization is not deterministic for identical source pixels")
			}
		})
	}
}

func TestNormalizeRejectsAdversarialInput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		want error
	}{
		{"polyglot.png", ErrTrailingData}, {"dimension-bomb.png", ErrDimensions}, {"invalid.png", ErrTrailingData},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := processor().Normalize(context.Background(), bytes.NewReader(fixture(t, test.name)), test.name)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
	_, err := processor().Normalize(context.Background(), strings.NewReader("MZ executable"), "nice.png")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	jpegPolyglot := append(append(append([]byte{}, fixture(t, "valid.jpg")...), []byte("payload")...), 0xff, 0xd9)
	_, err = processor().Normalize(context.Background(), bytes.NewReader(jpegPolyglot), "polyglot.jpg")
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("JPEG polyglot ending in a fake EOI was accepted: %v", err)
	}
}

func TestNormalizeCompressedBoundIsStreaming(t *testing.T) {
	t.Parallel()
	p := processor()
	p.Limits.MaxBytes = 32
	r := &countingReader{remaining: 1 << 20}
	_, err := p.Normalize(context.Background(), r, "large.png")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	if r.read > 33 {
		t.Fatalf("read %d bytes beyond configured bound", r.read)
	}
}

func TestNormalizeResourceBoundCorpus(t *testing.T) {
	deadline := time.Now().Add(2 * time.Second)
	for i := 0; i < 500; i++ {
		for _, name := range []string{"valid.png", "valid.jpg", "invalid.png", "dimension-bomb.png", "polyglot.png"} {
			_, _ = processor().Normalize(context.Background(), bytes.NewReader(fixture(t, name)), name)
		}
	}
	if time.Now().After(deadline) {
		t.Fatal("bounded media corpus exceeded two seconds")
	}
}

type countingReader struct{ remaining, read int }

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, nil
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 'x'
	}
	r.remaining -= len(p)
	r.read += len(p)
	return len(p), nil
}

func FuzzNormalize(f *testing.F) {
	for _, name := range []string{"valid.png", "valid.jpg", "exif-gps.jpg", "invalid.png", "dimension-bomb.png", "polyglot.png"} {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	p := processor()
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		image, err := p.Normalize(context.Background(), bytes.NewReader(data), "fuzz.bin")
		if err == nil {
			if image.Width <= 0 || image.Height <= 0 || image.Size() > p.Limits.MaxBytes {
				t.Fatalf("invalid accepted result: %+v", image)
			}
			if image.SHA256 != sha256Bytes(image.Data) {
				t.Fatal("incorrect digest")
			}
		}
	})
}
