package metadata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// ArtCache stores TMDB images on local disk so browsing never waits on the
// network (or on the media mount) after the first view.
type ArtCache struct {
	dir    string
	client *http.Client

	mu       sync.Mutex
	inflight map[string]*fetch
	prefetch chan [2]string
}

type fetch struct {
	done chan struct{}
	err  error
}

var (
	validSize = map[string]bool{"w92": true, "w185": true, "w300": true, "w342": true, "w500": true, "w780": true, "w1280": true, "original": true}
	validPath = regexp.MustCompile(`^/[A-Za-z0-9_\-]+\.(jpg|png|webp)$`)
)

func NewArtCache(dir string) *ArtCache {
	c := &ArtCache{
		dir:      dir,
		client:   &http.Client{Timeout: 30 * time.Second},
		inflight: map[string]*fetch{},
		prefetch: make(chan [2]string, 4096),
	}
	for i := 0; i < 4; i++ {
		go func() {
			for p := range c.prefetch {
				c.Get(context.Background(), p[1], p[0])
			}
		}()
	}
	return c
}

// Prefetch queues an image for download without blocking.
func (c *ArtCache) Prefetch(path, size string) {
	if path == "" {
		return
	}
	select {
	case c.prefetch <- [2]string{path, size}:
	default:
	}
}

// Get returns the local file path for a TMDB image, downloading it once.
func (c *ArtCache) Get(ctx context.Context, size, path string) (string, error) {
	if !validSize[size] || !validPath.MatchString(path) {
		return "", fmt.Errorf("invalid image %s %s", size, path)
	}
	local := filepath.Join(c.dir, size, path[1:3], path[1:])
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}

	c.mu.Lock()
	if f, ok := c.inflight[local]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return local, f.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f := &fetch{done: make(chan struct{})}
	c.inflight[local] = f
	c.mu.Unlock()

	f.err = c.download(size, path, local)
	close(f.done)
	c.mu.Lock()
	delete(c.inflight, local)
	c.mu.Unlock()
	return local, f.err
}

func (c *ArtCache) download(size, path, local string) error {
	resp, err := c.client.Get("https://image.tmdb.org/t/p/" + size + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("image fetch: %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(local), ".dl-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 32<<20)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), local)
}
