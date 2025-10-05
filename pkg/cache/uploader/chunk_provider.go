package uploader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/valandreev/tigrisfs/pkg/cache/index"
)

// LocalFileChunkProvider reads chunk data directly from the cache directory on disk.
type LocalFileChunkProvider struct {
	// Root is the base directory that contains cached file data.
	Root string
}

// OpenChunk opens a reader for the requested upload record, constrained to the
// specified offset and length.
func (p LocalFileChunkProvider) OpenChunk(ctx context.Context, record index.UploadRecord) (ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Root == "" {
		return nil, errors.New("chunk provider: root directory is not configured")
	}
	if record.Path == "" {
		return nil, errors.New("chunk provider: record path is empty")
	}
	if record.Offset < 0 {
		return nil, fmt.Errorf("chunk provider: negative offset %d", record.Offset)
	}

	cleanPath := filepath.Clean(record.Path)
	if filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
		return nil, fmt.Errorf("chunk provider: invalid path %q", record.Path)
	}

	fullPath := filepath.Join(p.Root, cleanPath)
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("chunk provider: open file %q: %w", fullPath, err)
	}

	length := record.Length
	if length <= 0 {
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("chunk provider: stat file %q: %w", fullPath, statErr)
		}
		length = info.Size() - record.Offset
		if length < 0 {
			_ = file.Close()
			return nil, fmt.Errorf("chunk provider: invalid length derived from file size")
		}
	}

	section := io.NewSectionReader(file, record.Offset, length)
	return &fileSectionReadCloser{SectionReader: section, file: file}, nil
}

type fileSectionReadCloser struct {
	*io.SectionReader
	file *os.File
}

func (f *fileSectionReadCloser) Close() error {
	return f.file.Close()
}
