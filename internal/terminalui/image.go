package terminalui

import (
	"errors"
	"mime"
)

type ImageCache interface {
	PutImage(id, sha256, mediaType string, body []byte) (string, error)
}

type ImageMetadata struct {
	ID, SHA256, MediaType string
	Size                  int64
}

type PreparedImage struct {
	Path, Warning string
	Inline        []byte
}

func VerifyAndCacheImage(cache ImageCache, renderer KittyRenderer, meta ImageMetadata, body []byte, responseType string) (PreparedImage, error) {
	mediaType, _, err := mime.ParseMediaType(responseType)
	if err != nil || mediaType != meta.MediaType || mediaType != "image/jpeg" && mediaType != "image/png" {
		return PreparedImage{}, errors.New("тип загруженного вложения не совпадает")
	}
	if meta.Size < 0 || int64(len(body)) != meta.Size {
		return PreparedImage{}, errors.New("размер загруженного вложения не совпадает")
	}
	if cache == nil {
		return PreparedImage{}, errors.New("кэш вложений недоступен")
	}
	path, err := cache.PutImage(meta.ID, meta.SHA256, mediaType, body)
	if err != nil {
		return PreparedImage{}, err
	}
	result := PreparedImage{Path: path}
	if renderer.Available() {
		result.Inline, err = PrepareKittyImage(body, mediaType)
		if err != nil {
			result.Warning = "Inline-показ недоступен: " + err.Error()
		}
	}
	return result, nil
}
