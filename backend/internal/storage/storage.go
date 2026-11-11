// Package storage keeps original files on the local filesystem under a configured root.
// Files are addressed by generated relative paths only; user-supplied names never touch the filesystem.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type FS struct{ Root string }

func New(root string) (*FS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &FS{Root: abs}, nil
}

// Save streams r to a new file named <uuid><ext> under YYYY/MM and returns its relative path,
// size and SHA-256. ext must be a known safe extension chosen by the caller.
func (f *FS) Save(r io.Reader, ext string) (rel string, size int64, sum string, err error) {
	now := time.Now().UTC()
	rel = filepath.Join(fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", now.Month()), uuid.NewString()+ext)
	full := filepath.Join(f.Root, rel)
	if err = os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return
	}
	if err = os.Chmod(tmp.Name(), 0o640); err != nil {
		return
	}
	if err = os.Rename(tmp.Name(), full); err != nil {
		return
	}
	return rel, size, hex.EncodeToString(h.Sum(nil)), nil
}

// Path resolves a stored relative path, refusing anything that escapes the root.
func (f *FS) Path(rel string) (string, error) {
	full := filepath.Join(f.Root, filepath.Clean("/"+rel))
	if !strings.HasPrefix(full, f.Root+string(os.PathSeparator)) {
		return "", errors.New("invalid storage path")
	}
	return full, nil
}

func (f *FS) Open(rel string) (*os.File, error) {
	p, err := f.Path(rel)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}
