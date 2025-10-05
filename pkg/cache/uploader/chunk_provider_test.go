package uploader

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/valandreev/tigrisfs/pkg/cache/index"
)

func TestLocalFileChunkProviderReadsSection(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "objects", "file.bin")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := []byte("hello world")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	provider := LocalFileChunkProvider{Root: dir}
	record := index.UploadRecord{Path: "objects/file.bin", Offset: 6, Length: 5}

	chunk, err := provider.OpenChunk(context.Background(), record)
	if err != nil {
		t.Fatalf("OpenChunk returned error: %v", err)
	}
	defer chunk.Close()

	data, err := io.ReadAll(chunk)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != "world" {
		t.Fatalf("expected \"world\", got %q", string(data))
	}
}

func TestLocalFileChunkProviderRejectsTraversal(t *testing.T) {
	provider := LocalFileChunkProvider{Root: t.TempDir()}
	record := index.UploadRecord{Path: "../etc/passwd", Offset: 0, Length: 10}

	if _, err := provider.OpenChunk(context.Background(), record); err == nil {
		t.Fatalf("expected error for path traversal, got nil")
	}
}

func TestLocalFileChunkProviderDerivesLength(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.bin")
	payload := []byte("abcdef")
	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	provider := LocalFileChunkProvider{Root: dir}
	record := index.UploadRecord{Path: "data.bin", Offset: 2, Length: 0}

	chunk, err := provider.OpenChunk(context.Background(), record)
	if err != nil {
		t.Fatalf("OpenChunk returned error: %v", err)
	}
	defer chunk.Close()

	data, err := io.ReadAll(chunk)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != "cdef" {
		t.Fatalf("expected derived data 'cdef', got %q", string(data))
	}
}
