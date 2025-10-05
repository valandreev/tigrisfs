package cache_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigrisdata/tigrisfs/pkg/cache"
)

func TestLoadConfigCreatesTemplateWhenMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	cfg, err := cache.LoadConfig(configPath)
	if !errors.Is(err, cache.ErrConfigMissing) {
		t.Fatalf("expected ErrConfigMissing, got %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config when missing, got %#v", cfg)
	}

	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("expected template to be created, read failed: %v", readErr)
	}
	if !strings.Contains(string(data), "cache_size_gb") {
		t.Fatalf("template content does not contain expected default, got:\n%s", string(data))
	}
}

func TestLoadConfigFailsValidation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	yaml := `version: 1
cache_size_gb: -1
chunk_mb: -8
upload:
  connect_timeout_sec: 5
  retry_interval_sec: 5
  max_retry_sec: 100
  max_concurrent_uploads: 2
fail_safe:
  enable: true
  disk_min_free_percent: 15
`
	if err := os.WriteFile(configPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := cache.LoadConfig(configPath)
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on validation failure, got %#v", cfg)
	}
	var vErr cache.ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(vErr.Issues) == 0 {
		t.Fatalf("expected validation issues to be populated")
	}
}

func TestLoadConfigParsesValidFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	yaml := `version: 1
cache_size_gb: 24
chunk_mb: 16
clean_interval_min: 15
upload:
  connect_timeout_sec: 7
  retry_interval_sec: 11
  max_retry_sec: 200
  max_concurrent_uploads: 3
fail_safe:
  enable: true
  disk_min_free_percent: 12
`
	if err := os.WriteFile(configPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := cache.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected config instance")
	}
	if cfg.CacheSizeGB != 24 {
		t.Fatalf("expected cache_size_gb 24, got %d", cfg.CacheSizeGB)
	}
	if cfg.ChunkMB != 16 {
		t.Fatalf("expected chunk_mb 16, got %d", cfg.ChunkMB)
	}
	if cfg.CleanIntervalMin != 15 {
		t.Fatalf("expected clean_interval_min 15, got %d", cfg.CleanIntervalMin)
	}
	if cfg.Upload.MaxConcurrentUploads != 3 {
		t.Fatalf("expected max_concurrent_uploads 3, got %d", cfg.Upload.MaxConcurrentUploads)
	}
	if !cfg.FailSafe.Enable {
		t.Fatalf("expected fail_safe.enable true")
	}
	if cfg.FailSafe.DiskMinFreePercent != 12 {
		t.Fatalf("expected fail_safe.disk_min_free_percent 12, got %d", cfg.FailSafe.DiskMinFreePercent)
	}
	if cfg.CleanIntervalMin == 0 {
		t.Fatalf("expected clean_interval_min default to be non-zero")
	}
}
