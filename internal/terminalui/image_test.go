package terminalui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

type imageCacheFake struct {
	id, hash, mediaType string
	body                []byte
}

func (c *imageCacheFake) PutImage(id, hash, mediaType string, body []byte) (string, error) {
	c.id, c.hash, c.mediaType, c.body = id, hash, mediaType, append([]byte(nil), body...)
	return "/safe/cache/image.png", nil
}

func TestVerifyAndCacheImageBoundary(t *testing.T) {
	var body bytes.Buffer
	imageBody := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageBody.Set(0, 0, color.White)
	if err := png.Encode(&body, imageBody); err != nil {
		t.Fatal(err)
	}
	cache := &imageCacheFake{}
	result, err := VerifyAndCacheImage(cache, KittyRenderer{Mode: InlineOff}, ImageMetadata{ID: "attach_1", SHA256: "hash", MediaType: "image/png", Size: int64(body.Len())}, body.Bytes(), "image/png; charset=binary")
	if err != nil || result.Path != "/safe/cache/image.png" || cache.id != "attach_1" || !bytes.Equal(cache.body, body.Bytes()) {
		t.Fatalf("result=%#v cache=%#v err=%v", result, cache, err)
	}
	if _, err := VerifyAndCacheImage(cache, KittyRenderer{Mode: InlineOff}, ImageMetadata{ID: "attach_1", SHA256: "hash", MediaType: "image/jpeg", Size: int64(body.Len())}, body.Bytes(), "image/png"); err == nil {
		t.Fatal("MIME mismatch accepted")
	}
	if _, err := VerifyAndCacheImage(cache, KittyRenderer{Mode: InlineOff}, ImageMetadata{ID: "attach_1", SHA256: "hash", MediaType: "image/png", Size: int64(body.Len() + 1)}, body.Bytes(), "image/png"); err == nil {
		t.Fatal("size mismatch accepted")
	}
}
