package uploader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexpkg "github.com/valandreev/tigrisfs/pkg/cache/index"
	bboltpkg "github.com/valandreev/tigrisfs/pkg/cache/index/bbolt"
)

func TestUploaderProcessesQueuedUploads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idx := newTestIndex(t)

	record, err := idx.AddUpload(ctx, indexpkg.UploadRecord{
		Path:   "objects/file.bin",
		Offset: 0,
		Length: 1024,
		Status: indexpkg.UploadStatusQueued,
	})
	if err != nil {
		t.Fatalf("AddUpload failed: %v", err)
	}

	chunkData := map[string][]byte{
		record.Path: bytes.Repeat([]byte{'a'}, int(record.Length)),
	}
	backend := &stubBackend{
		responses: []error{nil},
	}
	sleeper := &stubSleeper{}
	logger := &captureLogger{}
	provider := newStubChunkProvider(chunkData)
	metrics := &stubMetrics{}

	cfg := Config{
		MaxConcurrentUploads: 1,
		MaxAttempts:          3,
		BaseRetryDelay:       5 * time.Millisecond,
		MaxRetryDelay:        50 * time.Millisecond,
		PollInterval:         5 * time.Millisecond,
	}

	uploader, err := New(cfg, idx, backend, WithSleeper(sleeper), WithLogger(logger), WithChunkProvider(provider), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("New uploader failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = uploader.Run(ctx)
		close(done)
	}()

	waitForStatus(t, idx, record.ID, indexpkg.UploadStatusComplete, 500*time.Millisecond)

	cancel()
	<-done

	if len(backend.calls) != 1 {
		t.Fatalf("expected 1 backend call, got %d", len(backend.calls))
	}
	if len(backend.payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(backend.payloads))
	}
	if !bytes.Equal(backend.payloads[0], chunkData[record.Path]) {
		t.Fatalf("payload mismatch: got %d bytes", len(backend.payloads[0]))
	}
	if provider.OpenCount(record.Path) != 1 {
		t.Fatalf("expected provider to open chunk once, got %d", provider.OpenCount(record.Path))
	}

	snap := metrics.Snapshot()
	if snap.queued != 1 || snap.started != 1 || snap.completed != 1 || snap.failed != 0 || snap.retried != 0 {
		t.Fatalf("unexpected metrics snapshot %+v", snap)
	}
}

func TestUploaderRetriesWithBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idx := newTestIndex(t)

	record, err := idx.AddUpload(ctx, indexpkg.UploadRecord{
		Path:   "objects/retry.bin",
		Offset: 0,
		Length: 512,
		Status: indexpkg.UploadStatusQueued,
	})
	if err != nil {
		t.Fatalf("AddUpload failed: %v", err)
	}

	retryErr := temporaryError{err: errors.New("transient failure")}
	backend := &stubBackend{
		responses: []error{retryErr, retryErr, nil},
	}
	sleeper := &stubSleeper{}
	logger := &captureLogger{}
	chunkData := map[string][]byte{
		record.Path: bytes.Repeat([]byte{'b'}, 1024),
	}
	provider := newStubChunkProvider(chunkData)
	metrics := &stubMetrics{}

	cfg := Config{
		MaxConcurrentUploads: 2,
		MaxAttempts:          4,
		BaseRetryDelay:       10 * time.Millisecond,
		MaxRetryDelay:        80 * time.Millisecond,
		PollInterval:         5 * time.Millisecond,
	}

	uploader, err := New(cfg, idx, backend, WithSleeper(sleeper), WithLogger(logger), WithChunkProvider(provider), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("New uploader failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = uploader.Run(ctx)
		close(done)
	}()

	waitForStatus(t, idx, record.ID, indexpkg.UploadStatusComplete, 800*time.Millisecond)

	cancel()
	<-done

	durations := sleeper.Durations()
	if len(durations) != 2 {
		t.Fatalf("expected 2 sleep durations, got %d", len(durations))
	}

	if durations[0] != cfg.BaseRetryDelay {
		t.Fatalf("expected first backoff %v, got %v", cfg.BaseRetryDelay, durations[0])
	}
	if durations[1] != cfg.BaseRetryDelay*2 {
		t.Fatalf("expected second backoff %v, got %v", cfg.BaseRetryDelay*2, durations[1])
	}
	if len(backend.payloads) != 3 {
		t.Fatalf("expected 3 payloads, got %d", len(backend.payloads))
	}
	expectedChunk := chunkData[record.Path][:int(record.Length)]
	for i, payload := range backend.payloads {
		if !bytes.Equal(payload, expectedChunk) {
			t.Fatalf("payload %d mismatch", i)
		}
	}
	if provider.OpenCount(record.Path) != 3 {
		t.Fatalf("expected provider to open 3 times, got %d", provider.OpenCount(record.Path))
	}

	snap := metrics.Snapshot()
	if snap.queued != 3 || snap.started != 3 || snap.retried != 2 || snap.completed != 1 || snap.failed != 0 {
		t.Fatalf("unexpected metrics snapshot %+v", snap)
	}
}

func TestUploaderRespectsMaxConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idx := newTestIndex(t)

	records := make([]indexpkg.UploadRecord, 0, 2)
	chunkData := make(map[string][]byte)
	for i := 0; i < 2; i++ {
		rec, err := idx.AddUpload(ctx, indexpkg.UploadRecord{
			Path:   filepath.Join("objects", fmt.Sprintf("file-%d", i)),
			Offset: int64(i) * 1024,
			Length: 256,
			Status: indexpkg.UploadStatusQueued,
		})
		if err != nil {
			t.Fatalf("AddUpload failed: %v", err)
		}
		records = append(records, rec)
		totalSize := int(rec.Offset) + int(rec.Length)
		chunkData[rec.Path] = bytes.Repeat([]byte{byte('c' + i)}, totalSize)
	}

	var inFlight int32
	backend := &stubBackend{
		hook: func() {
			n := atomic.AddInt32(&inFlight, 1)
			if n > 1 {
				t.Fatalf("expected max concurrency 1, got %d", n)
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
		},
	}
	backend.responses = []error{nil, nil}
	provider := newStubChunkProvider(chunkData)
	metrics := &stubMetrics{}

	cfg := Config{
		MaxConcurrentUploads: 1,
		MaxAttempts:          3,
		BaseRetryDelay:       5 * time.Millisecond,
		MaxRetryDelay:        20 * time.Millisecond,
		PollInterval:         5 * time.Millisecond,
	}

	uploader, err := New(cfg, idx, backend, WithSleeper(&stubSleeper{}), WithLogger(&captureLogger{}), WithChunkProvider(provider), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("New uploader failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = uploader.Run(ctx)
		close(done)
	}()

	for _, rec := range records {
		waitForStatus(t, idx, rec.ID, indexpkg.UploadStatusComplete, time.Second)
	}

	cancel()
	<-done

	if len(backend.payloads) != len(records) {
		t.Fatalf("expected %d payloads, got %d", len(records), len(backend.payloads))
	}
	for i, call := range backend.calls {
		recPath := call.Path
		rec := findRecordByPath(t, records, recPath)
		start := int(rec.Offset)
		end := start + int(rec.Length)
		expected := chunkData[recPath][start:end]
		if !bytes.Equal(backend.payloads[i], expected) {
			t.Fatalf("payload mismatch for %s", recPath)
		}
		if provider.OpenCount(recPath) != 1 {
			t.Fatalf("expected single open for %s, got %d", recPath, provider.OpenCount(recPath))
		}
	}

	snap := metrics.Snapshot()
	if snap.queued != len(records) || snap.started != len(records) || snap.completed != len(records) || snap.failed != 0 || snap.retried != 0 {
		t.Fatalf("unexpected metrics snapshot %+v", snap)
	}
}

func TestUploaderResumesInProgressEntries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idx := newTestIndex(t)

	record, err := idx.AddUpload(ctx, indexpkg.UploadRecord{
		Path:   "objects/resume.bin",
		Offset: 0,
		Length: 1024,
		Status: indexpkg.UploadStatusQueued,
	})
	if err != nil {
		t.Fatalf("AddUpload failed: %v", err)
	}

	if _, err := idx.UpdateUploadStatus(ctx, record.ID, indexpkg.UploadStatusInProgress, ""); err != nil {
		t.Fatalf("UpdateUploadStatus failed: %v", err)
	}

	backend := &stubBackend{responses: []error{nil}}
	chunkData := map[string][]byte{
		record.Path: bytes.Repeat([]byte{'d'}, int(record.Length)),
	}
	provider := newStubChunkProvider(chunkData)
	metrics := &stubMetrics{}

	uploader, err := New(Config{
		MaxConcurrentUploads: 1,
		MaxAttempts:          3,
		BaseRetryDelay:       5 * time.Millisecond,
		MaxRetryDelay:        50 * time.Millisecond,
		PollInterval:         5 * time.Millisecond,
	}, idx, backend, WithSleeper(&stubSleeper{}), WithLogger(&captureLogger{}), WithChunkProvider(provider), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("New uploader failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = uploader.Run(ctx)
		close(done)
	}()

	waitForStatus(t, idx, record.ID, indexpkg.UploadStatusComplete, 500*time.Millisecond)

	cancel()
	<-done

	if len(backend.payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(backend.payloads))
	}
	if !bytes.Equal(backend.payloads[0], chunkData[record.Path]) {
		t.Fatalf("payload mismatch for resume path")
	}
	if provider.OpenCount(record.Path) != 1 {
		t.Fatalf("expected single open for resumed record, got %d", provider.OpenCount(record.Path))
	}

	snap := metrics.Snapshot()
	if snap.queued != 1 || snap.started != 1 || snap.completed != 1 || snap.failed != 0 || snap.retried != 0 {
		t.Fatalf("unexpected metrics snapshot %+v", snap)
	}
}

func TestUploaderMarksETagMismatchAsFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idx := newTestIndex(t)

	record, err := idx.AddUpload(ctx, indexpkg.UploadRecord{
		Path:   "objects/etag.bin",
		Offset: 0,
		Length: 2048,
		Status: indexpkg.UploadStatusQueued,
	})
	if err != nil {
		t.Fatalf("AddUpload failed: %v", err)
	}

	backend := &stubBackend{
		responses: []error{ErrETagMismatch},
	}
	logger := &captureLogger{}
	sleeper := &stubSleeper{}
	chunkData := map[string][]byte{
		record.Path: bytes.Repeat([]byte{'e'}, int(record.Length)),
	}
	provider := newStubChunkProvider(chunkData)
	metrics := &stubMetrics{}

	uploader, err := New(Config{
		MaxConcurrentUploads: 1,
		MaxAttempts:          2,
		BaseRetryDelay:       5 * time.Millisecond,
		MaxRetryDelay:        50 * time.Millisecond,
		PollInterval:         5 * time.Millisecond,
	}, idx, backend, WithSleeper(sleeper), WithLogger(logger), WithChunkProvider(provider), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("New uploader failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = uploader.Run(ctx)
		close(done)
	}()

	rec := waitForStatus(t, idx, record.ID, indexpkg.UploadStatusFailed, 500*time.Millisecond)

	cancel()
	<-done

	if rec.LastError == "" {
		t.Fatalf("expected last error to be recorded")
	}
	if len(logger.warnings) == 0 {
		t.Fatalf("expected warning log for etag mismatch")
	}
	if len(sleeper.calls) != 0 {
		t.Fatalf("expected no backoff sleep on etag mismatch")
	}
	if len(backend.payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(backend.payloads))
	}
	if !bytes.Equal(backend.payloads[0], chunkData[record.Path]) {
		t.Fatalf("payload mismatch for etag test")
	}
	if provider.OpenCount(record.Path) != 1 {
		t.Fatalf("expected single provider open for etag test, got %d", provider.OpenCount(record.Path))
	}

	snap := metrics.Snapshot()
	if snap.queued != 1 || snap.started != 1 || snap.completed != 0 || snap.failed != 1 || snap.retried != 0 {
		t.Fatalf("unexpected metrics snapshot %+v", snap)
	}
	if len(snap.reasons) != 1 || snap.reasons[0] != metricReasonETagMismatch {
		t.Fatalf("expected reason %q, got %+v", metricReasonETagMismatch, snap.reasons)
	}
}

// --- Test helpers ---

type stubBackend struct {
	mu        sync.Mutex
	responses []error
	calls     []indexpkg.UploadRecord
	payloads  [][]byte
	hook      func()
}

func (s *stubBackend) Upload(ctx context.Context, record indexpkg.UploadRecord, data ReadSeekCloser) error {
	s.mu.Lock()
	s.calls = append(s.calls, record)
	var err error
	if len(s.responses) > 0 {
		err = s.responses[0]
		s.responses = s.responses[1:]
	}
	hook := s.hook
	s.mu.Unlock()

	if hook != nil {
		hook()
	}

	payload, readErr := io.ReadAll(data)
	if readErr != nil {
		return readErr
	}

	s.mu.Lock()
	s.payloads = append(s.payloads, payload)
	s.mu.Unlock()

	return err
}

type stubSleeper struct {
	mu    sync.Mutex
	calls []time.Duration
}

func (s *stubSleeper) Sleep(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, d)
}

func (s *stubSleeper) Durations() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]time.Duration, len(s.calls))
	copy(out, s.calls)
	return out
}

type captureLogger struct {
	mu       sync.Mutex
	infos    []string
	warnings []string
	errors   []string
}

func (l *captureLogger) Debugf(format string, args ...any) {}

func (l *captureLogger) Infof(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, format)
}

func (l *captureLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, format)
}

func (l *captureLogger) Errorf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, format)
}

type stubChunkProvider struct {
	mu    sync.Mutex
	data  map[string][]byte
	opens map[string]int
}

func newStubChunkProvider(data map[string][]byte) *stubChunkProvider {
	return &stubChunkProvider{
		data:  data,
		opens: make(map[string]int),
	}
}

func (s *stubChunkProvider) OpenChunk(ctx context.Context, record indexpkg.UploadRecord) (ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	payload, ok := s.data[record.Path]
	if !ok {
		return nil, fmt.Errorf("no chunk data for path %s", record.Path)
	}
	if record.Offset < 0 || record.Offset > int64(len(payload)) {
		return nil, fmt.Errorf("offset %d out of range for %s", record.Offset, record.Path)
	}
	end := record.Offset + record.Length
	if record.Length <= 0 || end > int64(len(payload)) {
		end = int64(len(payload))
	}
	if end < record.Offset {
		return nil, fmt.Errorf("invalid chunk range for %s", record.Path)
	}

	start := int(record.Offset)
	length := int(end - record.Offset)
	slice := make([]byte, length)
	copy(slice, payload[start:start+length])
	s.opens[record.Path]++

	return &memoryChunk{Reader: bytes.NewReader(slice)}, nil
}

func (s *stubChunkProvider) OpenCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opens[path]
}

type memoryChunk struct {
	*bytes.Reader
}

func (m *memoryChunk) Close() error { return nil }

type stubMetrics struct {
	mu          sync.Mutex
	queued      int
	started     int
	retried     int
	completed   int
	failed      int
	failReasons []string
}

func (m *stubMetrics) RecordQueued(indexpkg.UploadRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queued++
}

func (m *stubMetrics) RecordStarted(indexpkg.UploadRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started++
}

func (m *stubMetrics) RecordRetried(indexpkg.UploadRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retried++
}

func (m *stubMetrics) RecordCompleted(indexpkg.UploadRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed++
}

func (m *stubMetrics) RecordFailed(_ indexpkg.UploadRecord, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed++
	m.failReasons = append(m.failReasons, reason)
}

type metricsSnapshot struct {
	queued    int
	started   int
	retried   int
	completed int
	failed    int
	reasons   []string
}

func (m *stubMetrics) Snapshot() metricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	reasons := make([]string, len(m.failReasons))
	copy(reasons, m.failReasons)
	return metricsSnapshot{
		queued:    m.queued,
		started:   m.started,
		retried:   m.retried,
		completed: m.completed,
		failed:    m.failed,
		reasons:   reasons,
	}
}

type temporaryError struct {
	err error
}

func (e temporaryError) Error() string { return e.err.Error() }

func (e temporaryError) Temporary() bool { return true }

func newTestIndex(t *testing.T) indexpkg.CacheIndex {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")
	idx, err := bboltpkg.Open(path, bboltpkg.Options{})
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	return idx
}

func waitForStatus(t *testing.T, idx indexpkg.CacheIndex, id string, status indexpkg.UploadStatus, timeout time.Duration) indexpkg.UploadRecord {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last indexpkg.UploadRecord
	for time.Now().Before(deadline) {
		last = findUpload(t, idx, id)
		if last.Status == status {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for status %s (last status %s)", status, last.Status)
	return last
}

func findUpload(t *testing.T, idx indexpkg.CacheIndex, id string) indexpkg.UploadRecord {
	t.Helper()

	uploads, err := idx.ListUploads(context.Background())
	if err != nil {
		t.Fatalf("ListUploads failed: %v", err)
	}
	for _, rec := range uploads {
		if rec.ID == id {
			return rec
		}
	}
	t.Fatalf("upload id %s not found", id)
	return indexpkg.UploadRecord{}
}

func findRecordByPath(t *testing.T, records []indexpkg.UploadRecord, path string) indexpkg.UploadRecord {
	t.Helper()
	for _, rec := range records {
		if rec.Path == path {
			return rec
		}
	}
	t.Fatalf("record with path %s not found", path)
	return indexpkg.UploadRecord{}
}
