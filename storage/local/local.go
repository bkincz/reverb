package local

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkincz/reverb/storage"
)

// ---------------------------------------------------------------------------
// Adapter
// ---------------------------------------------------------------------------

type Adapter struct {
	root    string
	baseURL string
}

func New(root, baseURL string) *Adapter {
	return &Adapter{root: root, baseURL: baseURL}
}

// ---------------------------------------------------------------------------
// Adapter implementation
// ---------------------------------------------------------------------------

func (a *Adapter) Upload(_ context.Context, input storage.UploadInput) error {
	dest := filepath.Join(a.root, filepath.FromSlash(input.Key))

	rel, err := filepath.Rel(a.root, dest)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("local storage: path traversal detected")
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("local storage: mkdir: %w", err)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("local storage: create file: %w", err)
	}

	if _, err := io.Copy(f, input.Body); err != nil {
		f.Close()
		return fmt.Errorf("local storage: write file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("local storage: close file: %w", err)
	}
	return nil
}

// Delete returns nil if the file does not exist.
func (a *Adapter) Delete(_ context.Context, key string) error {
	dest := filepath.Join(a.root, filepath.FromSlash(key))
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local storage: delete file: %w", err)
	}
	return nil
}

func (a *Adapter) URL(key string) string {
	return a.baseURL + "/" + key
}

func (a *Adapter) List(_ context.Context, prefix string, limit int) ([]storage.ListItem, error) {
	root := filepath.Join(a.root, filepath.FromSlash(prefix))
	var items []storage.ListItem

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(a.root, path)
		if err != nil {
			return err
		}
		items = append(items, storage.ListItem{
			Key:          filepath.ToSlash(rel),
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})
		if limit > 0 && len(items) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("local storage: list: %w", err)
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// FileServer
// ---------------------------------------------------------------------------

func (a *Adapter) FileServer() http.Handler {
	return http.StripPrefix(a.baseURL, http.FileServer(http.Dir(a.root)))
}

// FileServePath always ends with a trailing slash so the stdlib mux treats it as a subtree.
func (a *Adapter) FileServePath() string {
	return a.baseURL + "/"
}
