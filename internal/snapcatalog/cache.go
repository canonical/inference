package snapcatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	CacheTTL      = time.Hour
	cacheFilename = "snap-catalog.json"
)

type Cache struct {
	Path string
}

func DefaultCachePath() (string, error) {
	if common := os.Getenv("SNAP_USER_COMMON"); common != "" {
		return filepath.Join(common, cacheFilename), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache directory: %w", err)
	}
	return filepath.Join(dir, "inference", cacheFilename), nil
}

func (c Cache) Load(now time.Time) (Document, error) {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return Document{}, err
	}
	document, err := DecodeDocument(data)
	if err != nil {
		return Document{}, err
	}
	if document.FetchedAt.After(now) {
		return Document{}, fmt.Errorf("snap catalog cache is dated in the future")
	}
	return document, nil
}

func (c Cache) Save(document Document) error {
	data, err := EncodeDocument(document)
	if err != nil {
		return err
	}

	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, c.Path)
}
