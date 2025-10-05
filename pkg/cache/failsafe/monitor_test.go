package failsafe_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/valandreev/tigrisfs/pkg/cache/cleaner"
	"github.com/valandreev/tigrisfs/pkg/cache/failsafe"
)

type stubUploader struct {
	mu           sync.Mutex
	pausedCalls  int
	resumedCalls int
	failPause    error
	failResume   error
}

func (s *stubUploader) PauseUploads(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pausedCalls++
	return s.failPause
}

func (s *stubUploader) ResumeUploads(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumedCalls++
	return s.failResume
}

type stubCleaner struct {
	mu        sync.Mutex
	triggers  []cleaner.Trigger
	reports   []cleaner.Report
	err       error
	blockChan chan struct{}
}

func (s *stubCleaner) RunOnce(ctx context.Context, trigger cleaner.Trigger) (cleaner.Report, error) {
	if s.blockChan != nil {
		select {
		case <-ctx.Done():
			return cleaner.Report{}, ctx.Err()
		case <-s.blockChan:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.triggers = append(s.triggers, trigger)
	var report cleaner.Report
	if len(s.reports) > 0 {
		report = s.reports[0]
		s.reports = s.reports[1:]
	}
	return report, s.err
}

func TestMonitorHandleENOSPCTriggersCleanerAndResumes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	uploader := &stubUploader{}
	c := &stubCleaner{}

	monitor, err := failsafe.NewMonitor(c, uploader)
	if err != nil {
		t.Fatalf("NewMonitor returned error: %v", err)
	}

	if err := monitor.HandleENOSPC(ctx); err != nil {
		t.Fatalf("HandleENOSPC returned error: %v", err)
	}

	if uploader.pausedCalls != 1 {
		t.Fatalf("expected PauseUploads called once, got %d", uploader.pausedCalls)
	}
	if uploader.resumedCalls != 1 {
		t.Fatalf("expected ResumeUploads called once, got %d", uploader.resumedCalls)
	}

	if len(c.triggers) != 1 {
		t.Fatalf("expected cleaner triggered once, got %d", len(c.triggers))
	}
	if c.triggers[0].Reason != cleaner.TriggerReasonENOSPC {
		t.Fatalf("expected trigger reason ENOSPC, got %s", c.triggers[0].Reason)
	}
}

func TestMonitorHandleENOSPCFatalError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	uploader := &stubUploader{}
	c := &stubCleaner{err: cleaner.ErrFatalCondition}

	monitor, err := failsafe.NewMonitor(c, uploader)
	if err != nil {
		t.Fatalf("NewMonitor returned error: %v", err)
	}

	err = monitor.HandleENOSPC(ctx)
	if !errors.Is(err, failsafe.ErrRecoveryFailed) {
		t.Fatalf("expected ErrRecoveryFailed, got %v", err)
	}

	if uploader.resumedCalls != 0 {
		t.Fatalf("expected resume not called on fatal error, got %d", uploader.resumedCalls)
	}
}

func TestMonitorRejectsConcurrentRecovery(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	uploader := &stubUploader{}
	block := make(chan struct{})
	c := &stubCleaner{blockChan: block}

	monitor, err := failsafe.NewMonitor(c, uploader)
	if err != nil {
		t.Fatalf("NewMonitor returned error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- monitor.HandleENOSPC(ctx)
	}()

	// allow goroutine to start and pause uploader
	waitUntil(func() bool {
		uploader.mu.Lock()
		defer uploader.mu.Unlock()
		return uploader.pausedCalls == 1
	}, t)

	if err := monitor.HandleENOSPC(ctx); !errors.Is(err, failsafe.ErrRecoveryInProgress) {
		t.Fatalf("expected ErrRecoveryInProgress, got %v", err)
	}

	close(block)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected first recovery to succeed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("first recovery did not complete in time")
	}
}

func waitUntil(cond func() bool, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met before deadline")
}
