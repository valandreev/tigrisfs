# 🧩 PRD: TigrisFS Persistent Write-back Cache

## 1. Goals

Create a reliable, persistent data-and-metadata cache for TigrisFS that provides:

* persistence across restarts (cache survives client restart);
* write-back semantics (fast local writes, background flush to cloud);
* correct behavior during flaky networks and failures;
* SSD fill control and automatic eviction;
* minimal dependencies and full cross-platform support (Linux/macOS/Windows);
* simple YAML configuration file, no CLI flags or environment variables required.

---

## 2. Core idea

Each TigrisFS client has a local cache directory. All downloaded file segments are stored in sparse files; every object has its own container file that only contains the actually downloaded ranges. Cache state (ETag, size, list of ranges, dirty ranges queued for upload, etc.) is stored in an embedded bbolt database. The bbolt DB and cache files together form a persistent write-back layer.

---

## 3. Architecture

### 3.1. Components

| Component                        | Purpose                                                                      |
| -------------------------------- | ---------------------------------------------------------------------------- |
| **CacheIndex (bbolt-based)**     | Stores metadata about objects, ranges, dirty data, LRU marks and stats      |
| **CacheFiles (cache directory)** | Physical container files, one per object                                   |
| **Uploader**                     | Asynchronously uploads dirty ranges to the bucket                           |
| **Cleaner**                      | Enforces overall cache size and SSD fill rules (e.g. 90% threshold)         |
| **ConfigLoader**                 | Loads and validates the config; creates a template on first run            |
| **Fail-safe monitor**            | Reacts to ENOSPC and other critical errors; triggers soft-eviction         |

---

## 4. Configuration

### 4.1. `~/.tigrisfs/config.yaml` format

```yaml
version: 1

# Cache directory (if empty — ~/.tigrisfs/cache/<diskID>)
cache_dir: ""

# Maximum total cache size (GB)
cache_size_gb: 10

# Chunk size (MB) for range caching (NVMe optimal: 16, SATA: 8)
chunk_mb: 8

# Background cleaning interval (minutes)
clean_interval_min: 30

# Upload timeouts and behavior
upload:
  connect_timeout_sec: 10
  retry_interval_sec: 15
  max_retry_sec: 300
  max_concurrent_uploads: 4

# Failure behavior
fail_safe:
  enable: true
  disk_min_free_percent: 10
```

### 4.2. Start behavior

* If the config file does **not** exist — generate a template (comments + defaults) and exit with message: "Fill in config.yaml and restart".
* If the file exists but the cache directory is inaccessible (permission or disk errors) — print an error and exit.
* If everything is valid — load the config and lock it for use.

---

## 5. Cache format and index

### 5.1. Cache directory layout

```
~/.tigrisfs/cache/
 ├── datasilo2-colorgrade-top-my-work-bucket/
 │    ├── index.db               (bbolt)
 │    ├── uploads.db             (upload journal)
 │    ├── objects/
 │    │    ├── path/to/file1.bin
 │    │    └── project/shot42.mov
 │    └── .lock
```

### 5.2. bbolt schema

| Bucket    | Purpose                                                                                 |
| --------- | --------------------------------------------------------------------------------------- |
| `files`   | `<path>` → JSON `{etag, size, complete, chunks[], mtime_remote, atime_local}`         |
| `uploads` | active dirty ranges and their statuses                                                 |
| `stats`   | internal values (schema_version, total_size, free_space, error counters, etc.)        |

The schema version (`schema_version`) is stored in `stats`. On startup, if the stored version differs from the current application schema version, the client runs a migration that:

* creates any missing buckets;
* renames/removes deprecated fields as needed;
* updates `schema_version`.

This prevents cache breakage between client versions.

---

## 6. Cache behavior

### 6.1. Read

1. When a file is opened, TigrisFS reads ETag and size.
2. If the index entry matches, read ranges that exist on disk.
3. Missing ranges are fetched from S3 and written into the cache file at the correct offsets, index is updated.
4. Operations are atomic: write into a temporary buffer → `pwrite` → `fsync` → `Update()` in bbolt.

### 6.2. Write (write-back)

1. On application write, the block is stored in the local cache file and marked dirty.
2. An `uploads` entry is created with offset/len/status=queued.
3. The application receives success immediately after `fsync` of the local range.
4. Background **Uploader** flushes queued ranges:
   * on success → mark range as clean and update index;
   * on failure → retry with exponential backoff (from `retry_interval_sec` up to `max_retry_sec`);
   * on restart → reload `uploads` and resume pending uploads.
5. If the object ETag changes before upload completes, the dirty ranges for that file are invalidated (manual reconciliation required).

### 6.3. Deletion

* On ETag change or deletion, the index entry is removed and the local cache file is truncated to 0.

---

## 7. Size control and eviction

### 7.1. Thresholds and 90% rule

* **Primary limit**: `cache_size_gb` (default 10 GB).
* **Secondary limit**: disk free space must stay above `fail_safe.disk_min_free_percent` (default 10%). When either limit is violated, eviction runs.

### 7.2. LRU eviction algorithm

1. Scan `files` in the index ordered by `atime_local`.
2. Remove segments (range-level granularity) of the oldest objects until limits are restored.
3. If free space is still below the safety threshold, perform a full purge of unused diskIDs (buckets with no access > 7 days).
4. After eviction, update `stats.total_size` and persist changes.

### 7.3. Fail-safe on ENOSPC

If a write or upload encounters `ENOSPC`, the `FailSafeMonitor`:

* pauses new uploads;
* performs an immediate soft-LRU purge;
* if the condition persists, prints a clear user message and exits to avoid data corruption.

---

## 8. Chunk size

| SSD type  | Recommendation | Rationale                                                                 |
| --------- | -------------- | ------------------------------------------------------------------------- |
| NVMe PCIe | 16 MB          | high parallelism, minimize IOPS overhead                                  |
| SATA SSD  | **8 MB (default)** | smaller queue length, faster seeks for sequential range reads            |

Smaller chunks increase overhead; larger chunks risk over-allocation for small random reads. 8–16 MB is optimal for common workloads (video, project files).

---

## 9. Timeouts and retries

| Parameter                       | Description                                  | Default |
| ------------------------------- | -------------------------------------------- | ------- |
| `upload.connect_timeout_sec`    | connection timeout for uploads               | 10 s    |
| `upload.retry_interval_sec`     | base delay between retries                   | 15 s    |
| `upload.max_retry_sec`          | maximum delay for exponential backoff        | 300 s   |
| `upload.max_concurrent_uploads` | maximum concurrent uploads                   | 4       |

Uploader must log each failure and the total time to eventual completion after connectivity is restored.

---

## 10. Startup and restart behavior

1. Validate config file presence and correctness.
2. Validate cache directory permissions and free space.
3. Open index and migrate if `schema_version` differs; otherwise open directly.
4. Load `uploads` queue.
5. Start background services:
   * `Uploader` (flush dirty ranges);
   * `Cleaner` (monitor and evict to maintain size);
   * `FailSafeMonitor` (react to ENOSPC).

---

## 11. Example operational flow

1. User opens `/project/shot42.mov`.
2. Read of 2–3 GB range → file exists but that segment is absent; the segment is downloaded from S3, written to disk, and the index is updated.
3. A day later the same range is read → cache hit, read from SSD.
4. User writes 100 MB of new data → written to cache and application gets immediate OK.
5. Network disconnects → Uploader sleeps; queued uploads remain in `uploads`.
6. On network restore → Uploader resumes flushing.
7. If cache exceeds 10 GB → `Cleaner` evicts least-recently-used segments.

---

## 12. Extensibility (future)

The architecture provides:

* a `CacheIndex` interface (bbolt local variant; PostgreSQL for a shared/networked mode in future);
* easy swap of backends without rewriting core logic;
* possibility to add a shared-office cache (shared node) using PostgreSQL + Redis without client changes;
* `stats/schema_version` — migration mechanism that preserves cache across upgrades.

---

## 13. Metrics and diagnostics

| Metric                    | Units  | Notes                                 |
| ------------------------- | ------ | ------------------------------------- |
| `cache_size_bytes`        | bytes  | current cache usage                   |
| `cache_hit_ratio`         | %      | hit / (hit + miss)                    |
| `dirty_queue_length`      | count  | number of active upload ranges        |
| `uploader_failures_total` | count  | cumulative upload errors              |
| `free_space_percent`      | %      | remaining free space on SSD           |

Format: text or Prometheus (via build-time option).

---

## 14. Edge cases and testing

| Test                     | Validation                                              |
| ------------------------ | -------------------------------------------------------- |
| Client restart           | cache and pending uploads are recovered                   |
| Network loss             | dirty ranges are preserved; uploads resume later         |
| ENOSPC                  | soft-eviction performed and clean shutdown if unresolved  |
| ETag mismatch            | invalidation and full file reload required                |
| Config errors            | process refuses to start and prints a human-readable error |
| Schema version change    | migration occurs without data loss                        |

---

## 15. Non-functional requirements

* Performance: local segment read ≤ 2 ms on NVMe, ≤ 8 ms on SATA.
* Filesystem: support sparse files (ext4, NTFS, APFS).
* Config is read only on startup (restart required to apply changes).
* Minimal dependencies: `bbolt` and Go standard library.
* Cross-platform compatibility: UTF-8 paths, CRLF-independent files.
* Safe shutdown: fsync and close index before exit.

---

### ✅ Outcome

After implementing this PRD, TigrisFS will:

* persist cache across sessions;
* write to cloud asynchronously and safely;
* self-manage disk usage and evict when needed;
* be easily configurable without CLI flags;
* be ready to evolve into a shared/networked cache (Postgres+Redis).
