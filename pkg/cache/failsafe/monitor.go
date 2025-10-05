package failsafe

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/valandreev/tigrisfs/log"
	"github.com/valandreev/tigrisfs/pkg/cache/cleaner"
)

// ErrRecoveryFailed indicates the cleaner could not reclaim sufficient space and manual intervention is required.
var ErrRecoveryFailed = errors.New("cache failsafe: recovery failed")

// ErrRecoveryInProgress signals that a recovery sequence is already underway.
var ErrRecoveryInProgress = errors.New("cache failsafe: recovery in progress")

// Logger defines the logging surface used by the monitor.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Cleaner executes cache eviction when instructed by the monitor.
type Cleaner interface {
	RunOnce(ctx context.Context, trigger cleaner.Trigger) (cleaner.Report, error)
}

// UploaderController controls the uploader concurrency during recovery.
type UploaderController interface {
	PauseUploads(ctx context.Context) error
	ResumeUploads(ctx context.Context) error
}

// Option customises monitor construction.
type Option func(*Monitor)

// WithLogger replaces the default logger.
func WithLogger(logger Logger) Option {
	return func(m *Monitor) {
		m.logger = logger
	}
}

// Monitor coordinates ENOSPC recovery by pausing uploads and invoking the cleaner.
type Monitor struct {
	cleaner  Cleaner
	uploader UploaderController
	logger   Logger

	mu         sync.Mutex
	recovering bool
}

// NewMonitor constructs a Monitor instance.
func NewMonitor(cleaner Cleaner, uploader UploaderController, opts ...Option) (*Monitor, error) {
	if cleaner == nil {
		return nil, errors.New("cache failsafe: cleaner is required")
	}
	if uploader == nil {
		return nil, errors.New("cache failsafe: uploader controller is required")
	}

	m := &Monitor{
		cleaner:  cleaner,
		uploader: uploader,
		logger:   defaultLogger(),
	}

	for _, opt := range opts {
		opt(m)
	}

	if m.logger == nil {
		m.logger = defaultLogger()
	}

	return m, nil
}

// HandleENOSPC attempts to recover from an ENOSPC event by pausing uploads and running the cleaner.
func (m *Monitor) HandleENOSPC(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if !m.beginRecovery() {
		return ErrRecoveryInProgress
	}
	defer m.endRecovery()

	if err := m.uploader.PauseUploads(ctx); err != nil {
		return fmt.Errorf("cache failsafe: pause uploads: %w", err)
	}

	resumeUploads := true
	report, err := m.cleaner.RunOnce(ctx, cleaner.Trigger{Reason: cleaner.TriggerReasonENOSPC})
	if err != nil {
		if errors.Is(err, cleaner.ErrFatalCondition) {
			resumeUploads = false
			return fmt.Errorf("%w: %v", ErrRecoveryFailed, err)
		}

		if resumeUploads {
			if resumeErr := m.uploader.ResumeUploads(ctx); resumeErr != nil {
				m.logger.Warnf("failsafe: resume uploads after error failed: %v", resumeErr)
			}
		}
		return fmt.Errorf("cache failsafe: cleaner run: %w", err)
	}

	m.logger.Infof("failsafe: ENOSPC recovery completed, freed %d bytes", report.BytesFreed)

	if resumeUploads {
		if err := m.uploader.ResumeUploads(ctx); err != nil {
			return fmt.Errorf("cache failsafe: resume uploads: %w", err)
		}
	}

	return nil
}

func (m *Monitor) beginRecovery() bool {
	m.mu.Lock()
	if m.recovering {
		m.mu.Unlock()
		return false
	}
	m.recovering = true
	m.mu.Unlock()
	return true
}

func (m *Monitor) endRecovery() {
	m.mu.Lock()
	m.recovering = false
	m.mu.Unlock()
}

func defaultLogger() Logger {
	return logHandleAdapter{handle: log.GetLogger("cache-failsafe")}
}

type logHandleAdapter struct {
	handle *log.LogHandle
}

func (l logHandleAdapter) Debugf(format string, args ...any) {
	if l.handle != nil {
		l.handle.Debug().Msgf(format, args...)
	}
}

func (l logHandleAdapter) Infof(format string, args ...any) {
	if l.handle != nil {
		l.handle.Info().Msgf(format, args...)
	}
}

func (l logHandleAdapter) Warnf(format string, args ...any) {
	if l.handle != nil {
		l.handle.Warn().Msgf(format, args...)
	}
}

func (l logHandleAdapter) Errorf(format string, args ...any) {
	if l.handle != nil {
		l.handle.Error().Msgf(format, args...)
	}
}
