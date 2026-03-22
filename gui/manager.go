package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tigrisdata/tigrisfs/core"
	"github.com/tigrisdata/tigrisfs/core/cfg"
	"github.com/tigrisdata/tigrisfs/log"
)

var guiLog = log.GetLogger("gui")

const (
	mountModeSingle  = "single"
	mountModeUnified = "unified"
	unifiedMountTag  = "__UNIFIED__"
)

// MountInfo tracks a single active mount.
type MountInfo struct {
	Key         string
	ProfileName string
	Bucket      string
	Mode        string
	Buckets     []string
	Endpoint    string
	MountPoint  string
	CachePath   string
	Status      string // "mounted", "unmounting", "error"
	MountedAt   time.Time
	fs          *core.Goofys
	mfs         core.MountedFS
	cancel      context.CancelFunc
}

type mountTransferSample struct {
	ReadBytes  int64
	WriteBytes int64
	At         time.Time
}

type IntegrationMountStatus struct {
	Key           string   `json:"key"`
	ProfileName   string   `json:"profile_name"`
	Bucket        string   `json:"bucket"`
	Mode          string   `json:"mode"`
	Buckets       []string `json:"buckets,omitempty"`
	Endpoint      string   `json:"endpoint"`
	MountPoint    string   `json:"mount_point"`
	Status        string   `json:"status"`
	DownloadBps   float64  `json:"download_bps"`
	UploadBps     float64  `json:"upload_bps"`
	TotalDownload uint64   `json:"total_download_bytes"`
	TotalUpload   uint64   `json:"total_upload_bytes"`
}

type IntegrationPathStatus struct {
	Path          string   `json:"path"`
	Mounted       bool     `json:"mounted"`
	ProfileName   string   `json:"profile_name,omitempty"`
	Bucket        string   `json:"bucket,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	Buckets       []string `json:"buckets,omitempty"`
	MountPoint    string   `json:"mount_point,omitempty"`
	RelativePath  string   `json:"relative_path,omitempty"`
	Exists        bool     `json:"exists"`
	IsDir         bool     `json:"is_dir"`
	Pinned        bool     `json:"pinned"`
	Size          uint64   `json:"size"`
	CachedBytes   uint64   `json:"cached_bytes"`
	FullyCached   bool     `json:"fully_cached"`
	Loading       bool     `json:"loading"`
	Dirty         bool     `json:"dirty"`
	DownloadBps   float64  `json:"download_bps"`
	UploadBps     float64  `json:"upload_bps"`
	TotalDownload uint64   `json:"total_download_bytes"`
	TotalUpload   uint64   `json:"total_upload_bytes"`
	Error         string   `json:"error,omitempty"`
}

// MountManager handles mount/unmount lifecycle for multiple profiles and buckets.
type MountManager struct {
	mu              sync.Mutex
	mounts          map[string]*MountInfo // keyed by profile+bucket
	transferByMount map[string]mountTransferSample
}

func NewMountManager() *MountManager {
	return &MountManager{
		mounts:          make(map[string]*MountInfo),
		transferByMount: make(map[string]mountTransferSample),
	}
}

func mountKey(profileName, bucket string) string {
	return profileName + "\x00" + bucket
}

func sanitizeHost(host string) string {
	host = strings.ReplaceAll(host, ":", "-")
	return host
}

func shortenedEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err == nil && u.Host != "" {
		return sanitizeHost(u.Host)
	}
	host := endpoint
	if strings.Contains(host, "://") {
		parts := strings.Split(host, "://")
		host = parts[len(parts)-1]
	}
	host = strings.Split(host, "/")[0]
	if host == "" {
		host = "s3.amazonaws.com"
	}
	return sanitizeHost(host)
}

func resolveDiskCachePath(profile ConnectionProfile, bucket string) string {
	if strings.TrimSpace(profile.CachePath) == "" {
		return ""
	}
	endpoint := profile.Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	return filepath.Join(profile.CachePath, bucket, fmt.Sprintf("%s-%s", shortenedEndpoint(endpoint), bucket))
}

func normalizeBucketList(buckets []string) []string {
	out := make([]string, 0, len(buckets))
	seen := make(map[string]bool, len(buckets))
	for _, bucket := range buckets {
		b := strings.TrimSpace(bucket)
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

func buildFlagsForProfile(profile ConnectionProfile, mountPoint string) (*cfg.FlagStorage, error) {
	flags := cfg.DefaultFlags()
	flags.Endpoint = profile.Endpoint
	flags.NoVerifySSL = profile.SkipSSL
	flags.Foreground = true
	flags.MountPoint = mountPoint
	flags.MountPointArg = mountPoint
	flags.MemoryLimit = uint64(profile.MemoryMB) * 1024 * 1024
	flags.Writeback = profile.Writeback
	flags.CacheSize = profile.CacheSize
	flags.EntryLimit = profile.EntryLimit
	flags.MaxFlushers = int64(profile.MaxFlushers)
	flags.ReadAheadKB = uint64(profile.ReadAheadKB)
	flags.StatCacheTTL = time.Duration(profile.StatCacheTTLSeconds) * time.Second
	flags.HTTPTimeout = time.Duration(profile.HTTPTimeoutSeconds) * time.Second
	flags.RetryInterval = time.Duration(profile.RetryIntervalSec) * time.Second
	flags.Cheap = profile.Cheap
	flags.NoPreloadDir = profile.NoPreloadDir
	flags.ExplicitDir = profile.ExplicitDir
	flags.IgnoreFsync = profile.IgnoreFsync
	flags.FsyncOnClose = profile.FsyncOnClose
	flags.DisableXattr = profile.DisableXattr

	s3cfg, ok := flags.Backend.(*cfg.S3Config)
	if !ok {
		return nil, fmt.Errorf("unexpected backend type")
	}
	s3cfg.AccessKey = profile.AccessKey
	s3cfg.SecretKey = profile.SecretKey

	return flags, nil
}

func buildBackendForBucket(profile ConnectionProfile, bucket string) (core.StorageBackend, error) {
	flags := cfg.DefaultFlags()
	flags.Endpoint = profile.Endpoint
	flags.NoVerifySSL = profile.SkipSSL
	flags.HTTPTimeout = time.Duration(profile.HTTPTimeoutSeconds) * time.Second
	flags.RetryInterval = time.Duration(profile.RetryIntervalSec) * time.Second

	s3cfg, ok := flags.Backend.(*cfg.S3Config)
	if !ok {
		return nil, fmt.Errorf("unexpected backend type")
	}
	s3cfg.AccessKey = profile.AccessKey
	s3cfg.SecretKey = profile.SecretKey

	cloud, err := core.NewBackend(bucket, flags)
	if err != nil {
		return nil, err
	}
	spec, err := core.ParseBucketSpec(bucket)
	if err != nil {
		return nil, err
	}
	randomInitObject := spec.Prefix + core.RandStringBytesMaskImprSrc(32)
	if err := cloud.Init(randomInitObject); err != nil {
		return nil, err
	}
	_, _ = cloud.MultipartExpire(&core.MultipartExpireInput{})
	return cloud, nil
}

func (m *MountManager) profileHasMounted(profileName string) bool {
	for _, info := range m.mounts {
		if info.ProfileName == profileName {
			return true
		}
	}
	return false
}

// Mount mounts a single bucket under the specified profile.
func (m *MountManager) Mount(profile ConnectionProfile, bucket string) error {
	profile.applyDefaults(profile.Name)
	if profile.Name == "" {
		return fmt.Errorf("profile name is empty")
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("bucket name is empty")
	}
	guiLog.Infof("Mount request: profile=%s bucket=%s", profile.Name, bucket)

	key := mountKey(profile.Name, bucket)
	m.mu.Lock()
	if _, exists := m.mounts[key]; exists {
		m.mu.Unlock()
		return fmt.Errorf("bucket %q is already mounted for profile %q", bucket, profile.Name)
	}
	if _, unifiedExists := m.mounts[mountKey(profile.Name, unifiedMountTag)]; unifiedExists {
		m.mu.Unlock()
		return fmt.Errorf("profile %q has an active unified namespace mount; unmount it first", profile.Name)
	}
	m.mu.Unlock()

	mountPoint := filepath.Join(profile.MountRoot, bucket)
	if mountPoint == "" {
		return fmt.Errorf("mount point cannot be empty")
	}

	m.mu.Lock()
	for _, info := range m.mounts {
		if info.MountPoint == mountPoint {
			m.mu.Unlock()
			return fmt.Errorf("mount point %q is already in use by %s/%s", mountPoint, info.ProfileName, info.Bucket)
		}
	}
	m.mu.Unlock()

	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}

	flags, err := buildFlagsForProfile(profile, mountPoint)
	if err != nil {
		return err
	}

	cachePath := ""
	if profile.CachePath != "" {
		flags.CachePath = filepath.Join(profile.CachePath, bucket)
		cachePath = resolveDiskCachePath(profile, bucket)
	}

	ctx, cancel := context.WithCancel(context.Background())

	fs, mfs, err := mountBucket(ctx, bucket, flags)
	if err != nil {
		cancel()
		guiLog.Errorf("Mount failed: profile=%s bucket=%s error=%v", profile.Name, bucket, err)
		return fmt.Errorf("mount %q: %w", bucket, err)
	}

	info := &MountInfo{
		Key:         key,
		ProfileName: profile.Name,
		Bucket:      bucket,
		Mode:        mountModeSingle,
		Buckets:     []string{bucket},
		Endpoint:    profile.Endpoint,
		MountPoint:  mountPoint,
		CachePath:   cachePath,
		Status:      "mounted",
		MountedAt:   time.Now(),
		fs:          fs,
		mfs:         mfs,
		cancel:      cancel,
	}

	m.mu.Lock()
	m.mounts[key] = info
	m.mu.Unlock()
	guiLog.Infof("Mounted: profile=%s bucket=%s mount_point=%s", profile.Name, bucket, mountPoint)

	// Wait for unmount in background and clean up.
	go func(mountID string, in *MountInfo) {
		_ = in.mfs.Join(ctx)
		in.fs.SyncTree(nil)
		_ = in.fs.SaveCache()
		in.cancel()

		m.mu.Lock()
		delete(m.mounts, mountID)
		delete(m.transferByMount, mountID)
		m.mu.Unlock()
		guiLog.Infof("Mount session ended: profile=%s bucket=%s", in.ProfileName, in.Bucket)
	}(key, info)

	return nil
}

// MountUnified mounts multiple buckets as top-level subdirectories inside a single mountpoint.
func (m *MountManager) MountUnified(profile ConnectionProfile, buckets []string) error {
	profile.applyDefaults(profile.Name)
	if profile.Name == "" {
		return fmt.Errorf("profile name is empty")
	}
	buckets = normalizeBucketList(buckets)
	if len(buckets) == 0 {
		return fmt.Errorf("at least one bucket is required")
	}

	key := mountKey(profile.Name, unifiedMountTag)
	m.mu.Lock()
	if _, exists := m.mounts[key]; exists {
		m.mu.Unlock()
		return fmt.Errorf("profile %q already has a unified namespace mount", profile.Name)
	}
	if m.profileHasMounted(profile.Name) {
		m.mu.Unlock()
		return fmt.Errorf("profile %q already has active mounts; unmount them first", profile.Name)
	}
	m.mu.Unlock()

	mountPoint := filepath.Clean(profile.MountRoot)
	if mountPoint == "" || mountPoint == "." {
		return fmt.Errorf("mount point cannot be empty")
	}

	m.mu.Lock()
	for _, info := range m.mounts {
		if normalizePathForCompare(info.MountPoint) == normalizePathForCompare(mountPoint) {
			m.mu.Unlock()
			return fmt.Errorf("mount point %q is already in use by %s/%s", mountPoint, info.ProfileName, info.Bucket)
		}
	}
	m.mu.Unlock()

	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}

	flags, err := buildFlagsForProfile(profile, mountPoint)
	if err != nil {
		return err
	}

	cachePath := ""
	if strings.TrimSpace(profile.CachePath) != "" {
		cacheSegment := fmt.Sprintf("_unified_%s", strings.ReplaceAll(profile.Name, string(os.PathSeparator), "_"))
		flags.CachePath = filepath.Join(profile.CachePath, cacheSegment)
		cachePath = flags.CachePath
	}

	anchorBucket := buckets[0]
	anchorPrefix := "__tigrisfs_unified_root__/" + core.RandStringBytesMaskImprSrc(12)
	anchorSpec := anchorBucket + ":" + anchorPrefix

	ctx, cancel := context.WithCancel(context.Background())
	fs, mfs, err := mountBucket(ctx, anchorSpec, flags)
	if err != nil {
		cancel()
		guiLog.Errorf("Unified mount failed: profile=%s anchor=%s error=%v", profile.Name, anchorBucket, err)
		return fmt.Errorf("mount unified namespace: %w", err)
	}

	for _, bucket := range buckets {
		cloud, cloudErr := buildBackendForBucket(profile, bucket)
		if cloudErr != nil {
			_ = mfs.Unmount()
			cancel()
			return fmt.Errorf("prepare bucket %q: %w", bucket, cloudErr)
		}
		fs.MountBackend(bucket, cloud, "")
	}

	info := &MountInfo{
		Key:         key,
		ProfileName: profile.Name,
		Bucket:      unifiedMountTag,
		Mode:        mountModeUnified,
		Buckets:     append([]string(nil), buckets...),
		Endpoint:    profile.Endpoint,
		MountPoint:  mountPoint,
		CachePath:   cachePath,
		Status:      "mounted",
		MountedAt:   time.Now(),
		fs:          fs,
		mfs:         mfs,
		cancel:      cancel,
	}

	m.mu.Lock()
	m.mounts[key] = info
	m.mu.Unlock()
	guiLog.Infof("Mounted unified namespace: profile=%s buckets=%v mount_point=%s", profile.Name, buckets, mountPoint)

	go func(mountID string, in *MountInfo) {
		_ = in.mfs.Join(ctx)
		in.fs.SyncTree(nil)
		_ = in.fs.SaveCache()
		in.cancel()

		m.mu.Lock()
		delete(m.mounts, mountID)
		delete(m.transferByMount, mountID)
		m.mu.Unlock()
		guiLog.Infof("Unified mount session ended: profile=%s buckets=%v", in.ProfileName, in.Buckets)
	}(key, info)

	return nil
}

// Unmount gracefully unmounts a single profile+bucket mount.
func (m *MountManager) Unmount(profileName, bucket string) error {
	key := mountKey(profileName, bucket)

	m.mu.Lock()
	info, exists := m.mounts[key]
	if exists {
		info.Status = "unmounting"
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("bucket %q is not mounted for profile %q", bucket, profileName)
	}
	guiLog.Infof("Unmount request: profile=%s bucket=%s", profileName, bucket)

	if err := info.mfs.Unmount(); err != nil {
		m.mu.Lock()
		if cur, ok := m.mounts[key]; ok {
			cur.Status = "mounted"
		}
		m.mu.Unlock()
		guiLog.Errorf("Unmount failed: profile=%s bucket=%s error=%v", profileName, bucket, err)
		return fmt.Errorf("unmount %q: %w", bucket, err)
	}
	info.cancel()
	guiLog.Infof("Unmount initiated: profile=%s bucket=%s", profileName, bucket)
	return nil
}

// UnmountAll unmounts all active mounts.
func (m *MountManager) UnmountAll() {
	mounts := m.ActiveMounts()
	for _, mount := range mounts {
		_ = m.Unmount(mount.ProfileName, mount.Bucket)
	}
}

// ActiveMounts returns a snapshot of all active mounts sorted by profile+bucket.
func (m *MountManager) ActiveMounts() []MountInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]MountInfo, 0, len(m.mounts))
	for _, info := range m.mounts {
		cp := *info
		cp.Buckets = append([]string(nil), info.Buckets...)
		result = append(result, cp)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ProfileName == result[j].ProfileName {
			return result[i].Bucket < result[j].Bucket
		}
		return result[i].ProfileName < result[j].ProfileName
	})
	return result
}

// MountByProfileBucket gets mount details for a profile+bucket pair.
func (m *MountManager) MountByProfileBucket(profileName, bucket string) (MountInfo, bool) {
	key := mountKey(profileName, bucket)
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.mounts[key]
	if !ok {
		return MountInfo{}, false
	}
	cp := *info
	cp.Buckets = append([]string(nil), info.Buckets...)
	return cp, true
}

func (m *MountManager) UnifiedMountByProfile(profileName string) (MountInfo, bool) {
	return m.MountByProfileBucket(profileName, unifiedMountTag)
}

// IsMounted checks if a specific profile+bucket pair is mounted.
func (m *MountManager) IsMounted(profileName, bucket string) bool {
	key := mountKey(profileName, bucket)
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.mounts[key]
	return exists
}

func (m *MountManager) IsUnifiedMounted(profileName string) bool {
	return m.IsMounted(profileName, unifiedMountTag)
}

func normalizePathForCompare(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func pathWithinMount(path, mountPoint string) bool {
	p := normalizePathForCompare(path)
	m := normalizePathForCompare(mountPoint)
	if p == m {
		return true
	}
	return strings.HasPrefix(p, m+string(os.PathSeparator))
}

func (m *MountManager) resolveMountForAbsolutePath(path string) (*MountInfo, string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return nil, "", fmt.Errorf("path is empty")
	}

	var selected *MountInfo
	maxLen := -1

	m.mu.Lock()
	for _, info := range m.mounts {
		if pathWithinMount(path, info.MountPoint) {
			l := len(normalizePathForCompare(info.MountPoint))
			if l > maxLen {
				cp := *info
				selected = &cp
				maxLen = l
			}
		}
	}
	m.mu.Unlock()

	if selected == nil {
		return nil, "", fmt.Errorf("path is not inside an active TigrisFS mount")
	}
	if selected.fs == nil {
		return nil, "", fmt.Errorf("mount session is unavailable")
	}

	rel, err := filepath.Rel(filepath.Clean(selected.MountPoint), path)
	if err != nil {
		return nil, "", fmt.Errorf("resolve path: %w", err)
	}
	if rel == "." {
		rel = ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, "", fmt.Errorf("path escapes mount root")
	}

	return selected, filepath.ToSlash(rel), nil
}

func (m *MountManager) PinAbsolutePath(path string, recursive bool) (core.PinResult, error) {
	info, rel, err := m.resolveMountForAbsolutePath(path)
	if err != nil {
		return core.PinResult{}, err
	}
	guiLog.Infof("Pin request: profile=%s bucket=%s path=%s recursive=%v", info.ProfileName, info.Bucket, rel, recursive)
	return info.fs.PinPath(rel, recursive)
}

func (m *MountManager) UnpinAbsolutePath(path string, recursive bool) (core.PinResult, error) {
	info, rel, err := m.resolveMountForAbsolutePath(path)
	if err != nil {
		return core.PinResult{}, err
	}
	guiLog.Infof("Unpin request: profile=%s bucket=%s path=%s recursive=%v", info.ProfileName, info.Bucket, rel, recursive)
	return info.fs.UnpinPath(rel, recursive)
}

func (m *MountManager) sampleTransferForMount(key string, fs *core.Goofys) (float64, float64, uint64, uint64) {
	if fs == nil {
		return 0, 0, 0, 0
	}
	snapshot := fs.TransferStatsSnapshot()
	now := time.Now()
	totalDown := uint64(0)
	totalUp := uint64(0)
	if snapshot.ReadBytes > 0 {
		totalDown = uint64(snapshot.ReadBytes)
	}
	if snapshot.WriteBytes > 0 {
		totalUp = uint64(snapshot.WriteBytes)
	}

	m.mu.Lock()
	prev := m.transferByMount[key]
	m.transferByMount[key] = mountTransferSample{
		ReadBytes:  snapshot.ReadBytes,
		WriteBytes: snapshot.WriteBytes,
		At:         now,
	}
	m.mu.Unlock()

	if prev.At.IsZero() {
		return 0, 0, totalDown, totalUp
	}
	deltaSeconds := now.Sub(prev.At).Seconds()
	if deltaSeconds <= 0 {
		return 0, 0, totalDown, totalUp
	}

	downBps := float64(snapshot.ReadBytes-prev.ReadBytes) / deltaSeconds
	upBps := float64(snapshot.WriteBytes-prev.WriteBytes) / deltaSeconds
	if downBps < 0 {
		downBps = 0
	}
	if upBps < 0 {
		upBps = 0
	}
	return downBps, upBps, totalDown, totalUp
}

func (m *MountManager) IntegrationMountSnapshot() []IntegrationMountStatus {
	active := m.ActiveMounts()
	out := make([]IntegrationMountStatus, 0, len(active))
	for _, info := range active {
		downBps, upBps, totalDown, totalUp := m.sampleTransferForMount(info.Key, info.fs)
		out = append(out, IntegrationMountStatus{
			Key:           info.Key,
			ProfileName:   info.ProfileName,
			Bucket:        info.Bucket,
			Mode:          info.Mode,
			Buckets:       append([]string(nil), info.Buckets...),
			Endpoint:      info.Endpoint,
			MountPoint:    info.MountPoint,
			Status:        info.Status,
			DownloadBps:   downBps,
			UploadBps:     upBps,
			TotalDownload: totalDown,
			TotalUpload:   totalUp,
		})
	}
	return out
}

func cleanAbsolutePath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." {
		return ""
	}
	return path
}

func (m *MountManager) PathStatusAbsolute(path string) IntegrationPathStatus {
	status := IntegrationPathStatus{
		Path: cleanAbsolutePath(path),
	}
	if status.Path == "" {
		status.Error = "path is empty"
		return status
	}

	info, rel, err := m.resolveMountForAbsolutePath(status.Path)
	if err != nil {
		// Non-mounted paths are expected for shell overlay probing.
		return status
	}

	status.Mounted = true
	status.ProfileName = info.ProfileName
	status.Bucket = info.Bucket
	status.Mode = info.Mode
	status.Buckets = append([]string(nil), info.Buckets...)
	status.MountPoint = info.MountPoint
	status.RelativePath = rel

	pathState, stateErr := info.fs.PathPinState(rel)
	if stateErr != nil {
		if errors.Is(stateErr, syscall.ENOENT) || errors.Is(stateErr, syscall.ESTALE) {
			return status
		}
		status.Error = stateErr.Error()
		return status
	}

	status.Exists = pathState.Exists
	status.IsDir = pathState.IsDir
	status.Pinned = pathState.Pinned
	status.Size = pathState.Size
	status.CachedBytes = pathState.CachedBytes
	status.FullyCached = pathState.FullyCached
	status.Loading = pathState.Loading
	status.Dirty = pathState.Dirty
	status.DownloadBps, status.UploadBps, status.TotalDownload, status.TotalUpload = m.sampleTransferForMount(info.Key, info.fs)

	return status
}

func (m *MountManager) PathStatusesAbsolute(paths []string) []IntegrationPathStatus {
	if len(paths) == 0 {
		return nil
	}
	out := make([]IntegrationPathStatus, 0, len(paths))
	for _, p := range paths {
		out = append(out, m.PathStatusAbsolute(p))
	}
	return out
}

func (m *MountManager) UnmountByAbsolutePath(path string) error {
	info, _, err := m.resolveMountForAbsolutePath(path)
	if err != nil {
		return err
	}
	return m.Unmount(info.ProfileName, info.Bucket)
}
