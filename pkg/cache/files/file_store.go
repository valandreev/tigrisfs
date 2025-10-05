package files

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrClosed is returned if an operation is attempted on a closed container.
	ErrClosed = errors.New("cache file container is closed")
)

// Container represents a mutable cache file; writes occur on a temporary file until Close commits atomically.
type Container struct {
	mu        sync.Mutex
	file      *os.File
	finalPath string
	tempPath  string
	closed    bool
}

// OpenContainer prepares a container for the given path, copying any existing data to a staging file.
func OpenContainer(path string) (*Container, error) {
	if path == "" {
		return nil, errors.New("cache file path must not be empty")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache directory: %w", err)
	}

	pattern := filepath.Base(path) + ".tmp-*"
	tempFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("create staging file: %w", err)
	}

	if err := enableSparse(tempFile); err != nil {
		// Sparse allocation is a best-effort optimization; log/telemetry will be added later.
		// For now we fall back to a regular file by ignoring this error.
	}

	if err := copyExisting(path, tempFile); err != nil {
		tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return nil, err
	}

	return &Container{
		file:      tempFile,
		finalPath: path,
		tempPath:  tempFile.Name(),
	}, nil
}

// WriteAt writes data into the staged container at the given offset.
func (c *Container) WriteAt(p []byte, off int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0, ErrClosed
	}
	return c.file.WriteAt(p, off)
}

// ReadAt reads data from the staged container at the given offset.
func (c *Container) ReadAt(p []byte, off int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0, ErrClosed
	}
	return c.file.ReadAt(p, off)
}

// Truncate resizes the staged container to the provided size.
func (c *Container) Truncate(size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClosed
	}
	return c.file.Truncate(size)
}

// Fsync flushes the staged container to disk.
func (c *Container) Fsync() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClosed
	}
	return c.file.Sync()
}

// Close flushes and atomically renames the staged file into place.
func (c *Container) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	// Ensure metadata and data are persisted before moving into place.
	if err := c.file.Sync(); err != nil {
		_ = c.file.Close()
		_ = os.Remove(c.tempPath)
		c.closed = true
		return err
	}
	if err := c.file.Close(); err != nil {
		_ = os.Remove(c.tempPath)
		c.closed = true
		return err
	}

	if err := replaceFile(c.tempPath, c.finalPath); err != nil {
		c.closed = true
		return err
	}

	c.closed = true
	return nil
}

func copyExisting(srcPath string, dest *os.File) error {
	source, err := os.Open(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open existing cache file: %w", err)
	}
	defer source.Close()

	if _, err := io.Copy(dest, source); err != nil {
		return fmt.Errorf("copy existing cache data: %w", err)
	}
	if _, err := dest.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind staging file: %w", err)
	}
	return nil
}

func replaceFile(tempPath, finalPath string) error {
	if err := os.Remove(finalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove old cache file: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("commit cache file: %w", err)
	}
	return nil
}
