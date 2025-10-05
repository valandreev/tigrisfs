package files

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestContainerWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")

	container, err := OpenContainer(path)
	if err != nil {
		t.Fatalf("OpenContainer returned error: %v", err)
	}

	data := []byte("hello persistent cache")
	if _, err := container.WriteAt(data, 0); err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}

	buf := make([]byte, len(data))
	if _, err := container.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt failed: %v", err)
	}
	if string(buf) != string(data) {
		t.Fatalf("ReadAt returned %q, want %q", string(buf), string(data))
	}

	if err := container.Fsync(); err != nil {
		t.Fatalf("Fsync failed: %v", err)
	}
	if err := container.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	finalData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading final file failed: %v", err)
	}
	if string(finalData) != string(data) {
		t.Fatalf("final file contents %q, want %q", string(finalData), string(data))
	}
}

func TestContainerPartialWritesAndReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.bin")

	container, err := OpenContainer(path)
	if err != nil {
		t.Fatalf("OpenContainer returned error: %v", err)
	}
	defer func() {
		_ = container.Close()
	}()

	if _, err := container.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatalf("WriteAt segment 1 failed: %v", err)
	}
	if _, err := container.WriteAt([]byte("world"), 5); err != nil {
		t.Fatalf("WriteAt segment 2 failed: %v", err)
	}

	buf := make([]byte, 10)
	if _, err := container.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt failed: %v", err)
	}
	if string(buf) != "helloworld" {
		t.Fatalf("unexpected combined data: %q", string(buf))
	}
}

func TestContainerTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncate.bin")

	container, err := OpenContainer(path)
	if err != nil {
		t.Fatalf("OpenContainer returned error: %v", err)
	}

	if _, err := container.WriteAt([]byte("123456789"), 0); err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}

	if err := container.Truncate(0); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	buf := make([]byte, 1)
	if _, err := container.ReadAt(buf, 0); err != io.EOF {
		t.Fatalf("expected EOF after truncate, got %v", err)
	}

	if err := container.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected final file size 0, got %d", info.Size())
	}
}

func TestContainerAtomicCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "atomic.bin")

	if err := os.WriteFile(path, []byte("old-data"), 0o600); err != nil {
		t.Fatalf("failed to seed original file: %v", err)
	}

	container, err := OpenContainer(path)
	if err != nil {
		t.Fatalf("OpenContainer returned error: %v", err)
	}

	if _, err := container.WriteAt([]byte("new-data"), 0); err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}

	// before closing, the on-disk file should still contain old data
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(persisted) != "old-data" {
		t.Fatalf("expected on-disk data %q before close, got %q", "old-data", string(persisted))
	}

	if err := container.Fsync(); err != nil {
		t.Fatalf("Fsync failed: %v", err)
	}
	if err := container.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	finalData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading final file failed: %v", err)
	}
	if string(finalData) != "new-data" {
		t.Fatalf("final file contents %q, want %q", string(finalData), "new-data")
	}
}

func TestContainerCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idempotent.bin")

	container, err := OpenContainer(path)
	if err != nil {
		t.Fatalf("OpenContainer returned error: %v", err)
	}

	if err := container.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := container.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}
