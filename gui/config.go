package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const defaultProfileName = "Default"

// ConnectionProfile contains mount and endpoint settings for one environment.
type ConnectionProfile struct {
	Name         string   `json:"name"`
	Endpoint     string   `json:"endpoint"`
	AccessKey    string   `json:"access_key"`
	SecretKey    string   `json:"secret_key"`
	SkipSSL      bool     `json:"skip_ssl"`
	MountRoot    string   `json:"mount_root"`
	CachePath    string   `json:"cache_path"`
	CacheSize    int      `json:"cache_size"` // GB
	MemoryMB     int      `json:"memory_mb"`  // MB
	Writeback    bool     `json:"writeback"`
	AutoMount    []string `json:"auto_mount"`
	UnifiedMount bool     `json:"unified_mount,omitempty"`

	// Advanced mount tuning.
	EntryLimit          int  `json:"entry_limit,omitempty"`
	MaxFlushers         int  `json:"max_flushers,omitempty"`
	ReadAheadKB         int  `json:"read_ahead_kb,omitempty"`
	StatCacheTTLSeconds int  `json:"stat_cache_ttl_seconds,omitempty"`
	HTTPTimeoutSeconds  int  `json:"http_timeout_seconds,omitempty"`
	RetryIntervalSec    int  `json:"retry_interval_seconds,omitempty"`
	Cheap               bool `json:"cheap,omitempty"`
	NoPreloadDir        bool `json:"no_preload_dir,omitempty"`
	ExplicitDir         bool `json:"explicit_dir,omitempty"`
	IgnoreFsync         bool `json:"ignore_fsync,omitempty"`
	FsyncOnClose        bool `json:"fsync_on_close,omitempty"`
	DisableXattr        bool `json:"disable_xattr,omitempty"`
}

// AppConfig stores all persisted GUI settings.
// The legacy flat fields are retained for backwards compatibility/migration.
type AppConfig struct {
	Profiles      []ConnectionProfile `json:"profiles"`
	ActiveProfile string              `json:"active_profile"`

	Endpoint  string   `json:"endpoint,omitempty"`
	AccessKey string   `json:"access_key,omitempty"`
	SecretKey string   `json:"secret_key,omitempty"`
	SkipSSL   bool     `json:"skip_ssl,omitempty"`
	MountRoot string   `json:"mount_root,omitempty"`
	CachePath string   `json:"cache_path,omitempty"`
	CacheSize int      `json:"cache_size,omitempty"`
	MemoryMB  int      `json:"memory_mb,omitempty"`
	Writeback bool     `json:"writeback,omitempty"`
	AutoMount []string `json:"auto_mount,omitempty"`
}

func defaultProfile(name string) ConnectionProfile {
	return ConnectionProfile{
		Name:         name,
		SkipSSL:      false,
		MountRoot:    defaultMountRoot(),
		CachePath:    "",
		CacheSize:    100,
		MemoryMB:     1000,
		Writeback:    false,
		AutoMount:    nil,
		UnifiedMount: false,

		EntryLimit:          100000,
		MaxFlushers:         16,
		ReadAheadKB:         5 * 1024,
		StatCacheTTLSeconds: 30,
		HTTPTimeoutSeconds:  30,
		RetryIntervalSec:    30,
		Cheap:               false,
		NoPreloadDir:        false,
		ExplicitDir:         false,
		IgnoreFsync:         false,
		FsyncOnClose:        false,
		DisableXattr:        false,
	}
}

func defaultConfig() *AppConfig {
	return &AppConfig{
		Profiles:      []ConnectionProfile{defaultProfile(defaultProfileName)},
		ActiveProfile: defaultProfileName,
	}
}

func configDir() string {
	dir := platformConfigDir()
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

func normalizeProfileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultProfileName
	}
	return name
}

func uniqueProfileName(name string, used map[string]bool) string {
	base := normalizeProfileName(name)
	candidate := base
	i := 2
	for used[candidate] {
		candidate = base + " " + strconv.Itoa(i)
		i++
	}
	used[candidate] = true
	return candidate
}

func normalizeAutoMount(buckets []string) []string {
	if len(buckets) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(buckets))
	out := make([]string, 0, len(buckets))
	for _, b := range buckets {
		b = strings.TrimSpace(b)
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

func (p *ConnectionProfile) applyDefaults(defaultName string) {
	if p.Name == "" {
		p.Name = defaultName
	}
	p.Name = normalizeProfileName(p.Name)
	if p.MountRoot == "" {
		p.MountRoot = defaultMountRoot()
	}
	if p.CacheSize <= 0 {
		p.CacheSize = 100
	}
	if p.MemoryMB < 100 {
		p.MemoryMB = 1000
	}
	if p.EntryLimit <= 0 {
		p.EntryLimit = 100000
	}
	if p.MaxFlushers <= 0 {
		p.MaxFlushers = 16
	}
	if p.ReadAheadKB <= 0 {
		p.ReadAheadKB = 5 * 1024
	}
	if p.StatCacheTTLSeconds <= 0 {
		p.StatCacheTTLSeconds = 30
	}
	if p.HTTPTimeoutSeconds <= 0 {
		p.HTTPTimeoutSeconds = 30
	}
	if p.RetryIntervalSec <= 0 {
		p.RetryIntervalSec = 30
	}
	p.AutoMount = normalizeAutoMount(p.AutoMount)
}

func (c *AppConfig) migrateAndNormalize() {
	usedNames := make(map[string]bool)

	if len(c.Profiles) == 0 {
		p := defaultProfile(defaultProfileName)
		if c.Endpoint != "" {
			p.Endpoint = c.Endpoint
		}
		if c.AccessKey != "" {
			p.AccessKey = c.AccessKey
		}
		if c.SecretKey != "" {
			p.SecretKey = c.SecretKey
		}
		if c.MountRoot != "" {
			p.MountRoot = c.MountRoot
		}
		if c.CachePath != "" {
			p.CachePath = c.CachePath
		}
		if c.CacheSize > 0 {
			p.CacheSize = c.CacheSize
		}
		if c.MemoryMB >= 100 {
			p.MemoryMB = c.MemoryMB
		}
		p.SkipSSL = c.SkipSSL
		p.Writeback = c.Writeback
		p.AutoMount = normalizeAutoMount(c.AutoMount)
		c.Profiles = []ConnectionProfile{p}
	} else {
		for i := range c.Profiles {
			c.Profiles[i].applyDefaults(defaultProfileName)
			c.Profiles[i].Name = uniqueProfileName(c.Profiles[i].Name, usedNames)
		}
	}

	if len(c.Profiles) == 0 {
		p := defaultProfile(defaultProfileName)
		c.Profiles = []ConnectionProfile{p}
	}

	if c.ActiveProfile == "" {
		c.ActiveProfile = c.Profiles[0].Name
	}
	c.ActiveProfile = normalizeProfileName(c.ActiveProfile)
	if c.profileIndexByName(c.ActiveProfile) == -1 {
		c.ActiveProfile = c.Profiles[0].Name
	}
}

func loadConfig() *AppConfig {
	c := defaultConfig()
	data, err := os.ReadFile(configPath())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, c)
	c.migrateAndNormalize()
	if migrateCredentialsToKeychain(c) {
		if err := saveConfig(c); err != nil {
			guiLog.Warnf("credential migration completed with warnings: %v", err)
		}
	}
	return c
}

func (c *AppConfig) profileIndexByName(name string) int {
	for i := range c.Profiles {
		if c.Profiles[i].Name == name {
			return i
		}
	}
	return -1
}

func (c *AppConfig) ActiveProfileRef() *ConnectionProfile {
	idx := c.profileIndexByName(c.ActiveProfile)
	if idx == -1 {
		return nil
	}
	return &c.Profiles[idx]
}

func (c *AppConfig) ActiveProfileCopy() ConnectionProfile {
	p := c.ActiveProfileRef()
	if p == nil {
		def := defaultProfile(defaultProfileName)
		attachStoredCredentials(&def)
		return def
	}
	cp := *p
	cp.AutoMount = append([]string(nil), p.AutoMount...)
	attachStoredCredentials(&cp)
	return cp
}

func (c *AppConfig) SetActiveProfile(name string) bool {
	name = normalizeProfileName(name)
	if c.profileIndexByName(name) == -1 {
		return false
	}
	c.ActiveProfile = name
	return true
}

func (c *AppConfig) ProfileNames() []string {
	out := make([]string, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

func (c *AppConfig) UpsertProfile(profile ConnectionProfile) {
	profile.applyDefaults(profile.Name)
	idx := c.profileIndexByName(profile.Name)
	if idx == -1 {
		c.Profiles = append(c.Profiles, profile)
	} else {
		c.Profiles[idx] = profile
	}
	if c.ActiveProfile == "" {
		c.ActiveProfile = profile.Name
	}
}

func (c *AppConfig) DeleteProfile(name string) bool {
	if len(c.Profiles) <= 1 {
		return false
	}
	idx := c.profileIndexByName(name)
	if idx == -1 {
		return false
	}
	c.Profiles = append(c.Profiles[:idx], c.Profiles[idx+1:]...)
	if c.ActiveProfile == name {
		c.ActiveProfile = c.Profiles[0].Name
	}
	return true
}

func (c *AppConfig) syncLegacyFromActive() {
	p := c.ActiveProfileRef()
	if p == nil {
		return
	}
	c.Endpoint = p.Endpoint
	c.AccessKey = ""
	c.SecretKey = ""
	c.SkipSSL = p.SkipSSL
	c.MountRoot = p.MountRoot
	c.CachePath = p.CachePath
	c.CacheSize = p.CacheSize
	c.MemoryMB = p.MemoryMB
	c.Writeback = p.Writeback
	c.AutoMount = append([]string(nil), p.AutoMount...)
}

func (c *AppConfig) sanitizedForDisk() *AppConfig {
	cp := *c
	cp.Profiles = make([]ConnectionProfile, len(c.Profiles))
	for i := range c.Profiles {
		p := c.Profiles[i]
		p.AccessKey = ""
		p.SecretKey = ""
		cp.Profiles[i] = p
	}
	cp.AccessKey = ""
	cp.SecretKey = ""
	return &cp
}

func migrateCredentialsToKeychain(c *AppConfig) bool {
	changed := false

	if c.AccessKey != "" || c.SecretKey != "" {
		if p := c.ActiveProfileRef(); p != nil {
			if p.AccessKey == "" {
				p.AccessKey = c.AccessKey
			}
			if p.SecretKey == "" {
				p.SecretKey = c.SecretKey
			}
		}
		c.AccessKey = ""
		c.SecretKey = ""
		changed = true
	}

	for i := range c.Profiles {
		p := &c.Profiles[i]
		if p.AccessKey == "" && p.SecretKey == "" {
			continue
		}
		if err := saveProfileCredentials(p.Name, p.AccessKey, p.SecretKey); err != nil {
			guiLog.Warnf("failed to migrate credentials for profile %q to keychain: %v", p.Name, err)
			continue
		}
		p.AccessKey = ""
		p.SecretKey = ""
		changed = true
	}

	return changed
}

func persistCredentialsToKeychain(c *AppConfig) error {
	if c.AccessKey != "" || c.SecretKey != "" {
		if p := c.ActiveProfileRef(); p != nil {
			if p.AccessKey == "" {
				p.AccessKey = c.AccessKey
			}
			if p.SecretKey == "" {
				p.SecretKey = c.SecretKey
			}
		}
		c.AccessKey = ""
		c.SecretKey = ""
	}

	for i := range c.Profiles {
		p := &c.Profiles[i]
		if p.AccessKey == "" && p.SecretKey == "" {
			continue
		}
		if err := saveProfileCredentials(p.Name, p.AccessKey, p.SecretKey); err != nil {
			return err
		}
		p.AccessKey = ""
		p.SecretKey = ""
	}

	return nil
}

func saveConfig(c *AppConfig) error {
	c.migrateAndNormalize()
	persistErr := persistCredentialsToKeychain(c)
	if persistErr != nil {
		guiLog.Warnf("credentials were not fully persisted to keychain: %v", persistErr)
	}
	c.syncLegacyFromActive()

	data, err := json.MarshalIndent(c.sanitizedForDisk(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath(), data, 0o600); err != nil {
		return err
	}
	return persistErr
}
