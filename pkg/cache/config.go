package cache

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultVersion            = 1
	defaultCacheSizeGB        = 10
	defaultChunkMB            = 8
	defaultCleanIntervalMin   = 30
	defaultUploadConnectSec   = 10
	defaultUploadRetrySec     = 15
	defaultUploadMaxRetrySec  = 300
	defaultUploadMaxParallel  = 4
	defaultDiskMinFreePercent = 10
)

var ErrConfigMissing = errors.New("cache config missing")

// ValidationError aggregates config validation issues.
type ValidationError struct {
	Issues []string
}

func (v ValidationError) Error() string {
	if len(v.Issues) == 0 {
		return "config validation failed"
	}
	if len(v.Issues) == 1 {
		return v.Issues[0]
	}
	return fmt.Sprintf("config validation failed: %s", v.Issues)
}

// Config describes on-disk cache behaviour.
type Config struct {
	Version          int            `yaml:"version"`
	CacheDir         string         `yaml:"cache_dir"`
	CacheSizeGB      int            `yaml:"cache_size_gb"`
	ChunkMB          int            `yaml:"chunk_mb"`
	CleanIntervalMin int            `yaml:"clean_interval_min"`
	Upload           UploadConfig   `yaml:"upload"`
	FailSafe         FailSafeConfig `yaml:"fail_safe"`
}

// UploadConfig captures write-back uploader tuning.
type UploadConfig struct {
	ConnectTimeoutSec    int `yaml:"connect_timeout_sec"`
	RetryIntervalSec     int `yaml:"retry_interval_sec"`
	MaxRetrySec          int `yaml:"max_retry_sec"`
	MaxConcurrentUploads int `yaml:"max_concurrent_uploads"`
}

// FailSafeConfig configures ENOSPC protection.
type FailSafeConfig struct {
	Enable             bool `yaml:"enable"`
	DiskMinFreePercent int  `yaml:"disk_min_free_percent"`
}

// LoadConfig reads config from the provided path. When the file does not exist
// it writes a template and returns ErrConfigMissing to prompt the user to edit
// the newly created file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if writeErr := writeTemplate(path); writeErr != nil {
				return nil, writeErr
			}
			return nil, ErrConfigMissing
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse cache config: %w", err)
	}

	cfg.applyDefaults()
	if vErr := cfg.validate(); len(vErr.Issues) > 0 {
		return nil, vErr
	}

	return &cfg, nil
}

// EffectiveCacheDir resolves the cache directory using the provided diskID and
// the user-configured CacheDir. If CacheDir is empty it falls back to
// ~/.tigrisfs/cache/<diskID>.
func (c Config) EffectiveCacheDir(homeDir, diskID string) string {
	if c.CacheDir != "" {
		return c.CacheDir
	}
	base := filepath.Join(homeDir, ".tigrisfs", "cache")
	if diskID == "" {
		return base
	}
	return filepath.Join(base, diskID)
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = defaultVersion
	}
	if c.CacheSizeGB == 0 {
		c.CacheSizeGB = defaultCacheSizeGB
	}
	if c.ChunkMB == 0 {
		c.ChunkMB = defaultChunkMB
	}
	if c.CleanIntervalMin == 0 {
		c.CleanIntervalMin = defaultCleanIntervalMin
	}
	if c.Upload.ConnectTimeoutSec == 0 {
		c.Upload.ConnectTimeoutSec = defaultUploadConnectSec
	}
	if c.Upload.RetryIntervalSec == 0 {
		c.Upload.RetryIntervalSec = defaultUploadRetrySec
	}
	if c.Upload.MaxRetrySec == 0 {
		c.Upload.MaxRetrySec = defaultUploadMaxRetrySec
	}
	if c.Upload.MaxConcurrentUploads == 0 {
		c.Upload.MaxConcurrentUploads = defaultUploadMaxParallel
	}
	if c.FailSafe.DiskMinFreePercent == 0 {
		c.FailSafe.DiskMinFreePercent = defaultDiskMinFreePercent
	}
}

func (c Config) validate() ValidationError {
	issues := make([]string, 0)

	if c.Version != defaultVersion {
		issues = append(issues, "version must be 1")
	}
	if c.CacheSizeGB <= 0 {
		issues = append(issues, "cache_size_gb must be > 0")
	}
	if c.ChunkMB <= 0 {
		issues = append(issues, "chunk_mb must be > 0")
	}
	if c.CleanIntervalMin <= 0 {
		issues = append(issues, "clean_interval_min must be > 0")
	}
	if c.Upload.ConnectTimeoutSec <= 0 {
		issues = append(issues, "upload.connect_timeout_sec must be > 0")
	}
	if c.Upload.RetryIntervalSec <= 0 {
		issues = append(issues, "upload.retry_interval_sec must be > 0")
	}
	if c.Upload.MaxRetrySec <= 0 {
		issues = append(issues, "upload.max_retry_sec must be > 0")
	}
	if c.Upload.MaxConcurrentUploads <= 0 {
		issues = append(issues, "upload.max_concurrent_uploads must be > 0")
	}
	if c.FailSafe.DiskMinFreePercent <= 0 || c.FailSafe.DiskMinFreePercent > 100 {
		issues = append(issues, "fail_safe.disk_min_free_percent must be in (0,100]")
	}

	return ValidationError{Issues: issues}
}

func writeTemplate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	tpl := bytes.NewBufferString("# TigrisFS persistent cache configuration\n")
	tpl.WriteString("version: 1\n")
	tpl.WriteString("# cache_dir: \n")
	tpl.WriteString("cache_size_gb: 10\n")
	tpl.WriteString("chunk_mb: 8\n")
	tpl.WriteString("clean_interval_min: 30\n")
	tpl.WriteString("upload:\n")
	tpl.WriteString("  connect_timeout_sec: 10\n")
	tpl.WriteString("  retry_interval_sec: 15\n")
	tpl.WriteString("  max_retry_sec: 300\n")
	tpl.WriteString("  max_concurrent_uploads: 4\n")
	tpl.WriteString("fail_safe:\n")
	tpl.WriteString("  enable: true\n")
	tpl.WriteString("  disk_min_free_percent: 10\n")

	if err := os.WriteFile(path, tpl.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write config template: %w", err)
	}
	return nil
}
