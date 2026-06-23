package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Storage is the interface for persisting uploaded image bytes.
// The current implementation is local-filesystem only; an S3-compatible
// implementation can be added later behind this same interface.
type Storage interface {
	Save(id, ext string, r io.Reader) (path string, err error)
	Open(id, ext string) (io.ReadCloser, error)
}

type LocalStorage struct {
	Dir string
}

func NewLocal(dir string) (*LocalStorage, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return &LocalStorage{Dir: dir}, nil
}

func (s *LocalStorage) filename(id, ext string) string {
	return filepath.Join(s.Dir, id+"."+ext)
}

func (s *LocalStorage) Save(id, ext string, r io.Reader) (string, error) {
	path := s.filename(id, ext)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("write file: %w", err)
	}
	return path, nil
}

func (s *LocalStorage) Open(id, ext string) (io.ReadCloser, error) {
	return os.Open(s.filename(id, ext))
}
