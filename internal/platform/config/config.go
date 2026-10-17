// Package config loads server configuration with explicit precedence and secret handling.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP    HTTP    `yaml:"http"`
	Storage Storage `yaml:"storage"`
	Privacy Privacy `yaml:"privacy"`
	Secrets Secrets `yaml:"-"`
}
type HTTP struct {
	Address         string        `yaml:"address"`
	MaxBodyBytes    int64         `yaml:"max_body_bytes"`
	BodyReadTimeout time.Duration `yaml:"body_read_timeout"`
}
type Storage struct {
	Database string `yaml:"database"`
	Blobs    string `yaml:"blobs"`
}
type Privacy struct {
	StoreRawIP bool `yaml:"store_raw_ip"`
}
type Secrets struct {
	IPLookupKey               []byte
	IPEncryptionKey           []byte
	IPEncryptionKeyID         string
	IPPreviousEncryptionKey   []byte
	IPPreviousEncryptionKeyID string
}

func Defaults() Config {
	return Config{HTTP: HTTP{Address: "127.0.0.1:8080", MaxBodyBytes: 1 << 20, BodyReadTimeout: 15 * time.Second}, Storage: Storage{Database: "bord.db", Blobs: "data/blobs"}}
}

// Load applies defaults, then an optional YAML file, then BORD_* environment values.
func Load(path string, getenv func(string) string) (Config, error) {
	c := Defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, &c); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	if v := getenv("BORD_HTTP_ADDRESS"); v != "" {
		c.HTTP.Address = v
	}
	if v := getenv("BORD_HTTP_MAX_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Config{}, errors.New("invalid BORD_HTTP_MAX_BODY_BYTES")
		}
		c.HTTP.MaxBodyBytes = n
	}
	if v := getenv("BORD_HTTP_BODY_READ_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, errors.New("invalid BORD_HTTP_BODY_READ_TIMEOUT")
		}
		c.HTTP.BodyReadTimeout = d
	}
	if v := getenv("BORD_DATABASE"); v != "" {
		c.Storage.Database = v
	}
	if v := getenv("BORD_BLOBS"); v != "" {
		c.Storage.Blobs = v
	}
	if v := getenv("BORD_STORE_RAW_IP"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, errors.New("invalid BORD_STORE_RAW_IP")
		}
		c.Privacy.StoreRawIP = b
	}
	var err error
	c.Secrets.IPLookupKey, err = secret(getenv, "BORD_IP_LOOKUP_KEY")
	if err != nil {
		return Config{}, err
	}
	c.Secrets.IPEncryptionKey, err = secret(getenv, "BORD_IP_ENCRYPTION_KEY")
	if err != nil {
		return Config{}, err
	}
	if len(c.Secrets.IPLookupKey) < 32 {
		return Config{}, errors.New("IP lookup key is required and must contain at least 32 bytes")
	}
	if c.Privacy.StoreRawIP && len(c.Secrets.IPEncryptionKey) != 32 {
		return Config{}, errors.New("IP encryption key is required and must contain exactly 32 bytes when raw IP retention is enabled")
	}
	c.Secrets.IPEncryptionKeyID = getenv("BORD_IP_ENCRYPTION_KEY_ID")
	if c.Secrets.IPEncryptionKeyID == "" {
		c.Secrets.IPEncryptionKeyID = "primary"
	}
	c.Secrets.IPPreviousEncryptionKeyID = getenv("BORD_IP_PREVIOUS_ENCRYPTION_KEY_ID")
	c.Secrets.IPPreviousEncryptionKey, err = secret(getenv, "BORD_IP_PREVIOUS_ENCRYPTION_KEY")
	if err != nil {
		return Config{}, err
	}
	if (c.Secrets.IPPreviousEncryptionKeyID == "") != (len(c.Secrets.IPPreviousEncryptionKey) == 0) || len(c.Secrets.IPPreviousEncryptionKey) != 0 && len(c.Secrets.IPPreviousEncryptionKey) != 32 {
		return Config{}, errors.New("previous IP encryption key id and 32-byte key must be configured together")
	}
	if c.Secrets.IPPreviousEncryptionKeyID == c.Secrets.IPEncryptionKeyID && c.Secrets.IPPreviousEncryptionKeyID != "" {
		return Config{}, errors.New("current and previous IP encryption key ids must differ")
	}
	if c.HTTP.MaxBodyBytes <= 0 {
		return Config{}, errors.New("HTTP body limit must be positive")
	}
	if c.HTTP.BodyReadTimeout <= 0 {
		return Config{}, errors.New("HTTP body read timeout must be positive")
	}
	return c, nil
}

func secret(getenv func(string) string, name string) ([]byte, error) {
	value, file := getenv(name), getenv(name+"_FILE")
	if value != "" && file != "" {
		return nil, fmt.Errorf("%s and %s_FILE are mutually exclusive", name, name)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s_FILE: %w", name, err)
		}
		value = strings.TrimSpace(string(data))
	}
	if value == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s: invalid base64", name)
	}
	return decoded, nil
}
