Implementation plan (TDD-first) — feature/persistent-cache

Goal: implement a modular, test-driven persistent write-back cache for TigrisFS under `pkg/cache`.

- High-level rules:
- Tests first: for every new public behavior, write unit tests (and where appropriate integration tests) before changing production code.
- Small steps: each commit implements a single, test-covered capability (A: tests → B: minimal implementation → C: refactor → D: commit).
- English-only public comments and tests.
- Pluggable CacheIndex: design a small interface so swapping bbolt to another store (e.g. badger) requires minimal effort.
 
Confirmed decisions (from product & dev):
- Default cache directory: `~/.tigrisfs/cache/<diskID>` (confirmed).
- Uploads journal location: store upload entries in the same `index.db` under `uploads` bucket to keep atomicity (confirmed).
- Windows sparse files: implement Windows fallback behavior (attempt sparse support; if unavailable, use regular file with telemetry). Development and tests should assume Windows as initial platform (see note below).

Top-level layout (files/dirs to add)

pkg/cache/
  ├── cache.go            # public entry points (Cache manager), orchestration
  ├── config.go           # config loader/validator (reads ~/.tigrisfs/config.yaml)
  ├── index/              # cache index implementations & interface
  │    ├── index.go       # CacheIndex interface + common types
  │    └── bbolt/         # bbolt implementation
  │         ├── bbolt.go  # bbolt-backed implementation
  │         └── bbolt_test.go
  ├── files/              # cache file handling (sparse files, ranges)
  │    ├── file_store.go
  │    └── file_store_test.go
  ├── uploader/           # background uploader
  │    ├── uploader.go
  │    └── uploader_test.go
  ├── cleaner/            # eviction & fail-safe logic
  │    ├── cleaner.go
  │    └── cleaner_test.go
  └── uploads.db          # (example path only) journal managed by index or separate DB

Step-by-step plan (TDD steps)

Phase A — scaffolding, config, and interfaces
1) ✅ Add config loader
	- Tests: `config_test.go`
	  * missing config → template generated, loader returns specific ErrConfigMissing
	  * invalid fields → validation errors
	  * valid config → parsed struct matches defaults
	- Implement `pkg/cache/config.go`.

2) ✅ Define `CacheIndex` interface and types
	- Create `pkg/cache/index/index.go` with:
	  * types: FileMeta {Path, ETag, Size, Chunks []ChunkMeta, MtimeRemote, AtimeLocal}
	  * ChunkMeta {Offset, Length, DirtyFlag}
	  * methods: Get(path) -> (FileMeta, err), Put(path, FileMeta), Delete(path), ListLRU(limit), AddUpload(uploadEntry), ListUploads(), UpdateUploadStatus(id, status), BeginTx/Commit (or provide atomic Update)
	- Tests: `index/interface_test.go` that runs against a mock in-memory index (table-driven) to validate semantics.
	- Rationale: keep interface tiny and cover required behaviors only.

3) ✅ Add bbolt index implementation (minimal)
	- Tests: `index/bbolt/bbolt_test.go` that runs the same `index/interface_test.go` suite against bbolt implementation.
	- Implementation must provide a migration path via `stats.schema_version`.
	- Keep implementation internal to `index/bbolt`; use only interface in other packages.

Phase B — local file storage (sparse files and chunk writes)
4) ✅ Implement `file_store` (sparse file wrapper)
	- Tests: `file_store_test.go`
	  * create new file container, write at offset, read back
	  * partial writes and reads, boundaries
	  * atomic write flow: write temp → pwrite → fsync → rename/commit
	- API: OpenContainer(path) -> Container; Container.WriteAt([]byte, offset), ReadAt(...), Truncate(0), Fsync(), Close()
	- Note: on Windows/macOS ensure sparse behaviour is abstracted (use fallbacks if sparse unsupported). Tests should run with in-memory temporary files.

5) Integration test: create one FileMeta, write ranges via file_store, persist meta to index, read them back (integration_unit_01).

Phase C — uploads journal & uploader
6) Uploads journal in the index
	- Add uploadEntry type to index: {id, path, offset, len, status, attempts, lastError}
	- Tests: index can add/list/update uploads; persisted across index reopen (bbolt).

7) Implement `Uploader` with pluggable backend client (use existing storage backend API)
	- Tests: `uploader_test.go` using a fake storage backend that simulates success/fail/ETag-change.
	- Behavior to test:
	  * picks queued uploads and sets status -> in-progress -> success
	  * retries with exponential backoff on transient errors
	  * respects `max_concurrent_uploads`
	  * on restart resumes queued entries
	  * if ETag mismatch detected, uploader marks entry invalid and emits analytic/log

Phase D — eviction and fail-safe
8) Implement `Cleaner` test-first
	- Tests: `cleaner_test.go`
	  * given total size > cache_size_gb, cleaner evicts chunks by LRU until below threshold
	  * on ENOSPC event cleaner performs soft-LRU immediately
	  * on insufficient free space after soft-LRU, cleaner signals fatal condition
	- API: Cleaner.RunOnce(), Cleaner.RunBackground(ctx)

9) Fail-safe monitor
	- Tests: simulate ENOSPC during writer/upload; verify uploader paused and cleaner triggered, and that a clear error is raised if recovery fails.

Phase E — integration and mounting
10) Expose `pkg/cache.CacheManager` as a safe API for main mount code
	 - Methods: Start(), Stop(), Open(path) -> file handle wrapper (that uses file_store and index), Write/Read, SyncRange
	 - Tests: integration tests that mount (same-process) a small in-memory backend (s3proxy or fake) and exercise read/write lifecycle.

11) Integrate with main mount flow behind a feature flag `--local-cache` or `--no-local-cache` default ON
	 - Minimal change in `core` to call `CacheManager` for reads/writes when available.
	 - Add tests in core that exercise both cached and non-cached paths (use SAME_PROCESS_MOUNT=1 test harness).

Phase F — migrations & pluggability
12) Implement schema migration tests
	 - Add tests that simulate older `schema_version` values and assert migration path creates missing buckets and upgrades version.

13) Provide a second-index adapter stub for `badger` (or other) to verify pluggability
	 - Tests: run `index/interface_test.go` against the stub; ensure only adapter swap is required to switch stores.

Phase G — polish, logging, metrics, CI
14) Metrics & logging
	 - Add Prometheus-compatible counters and simple text metrics per PRD.
	 - Tests: unit tests for metric increments where feasible.

15) CI & integration tests
	 - Add GitHub Actions job that runs unit tests; integration tests using SAME_PROCESS_MOUNT=1 and s3proxy if available.

Concrete test list (map step -> tests & files)
- config: `pkg/cache/config_test.go` (template, validation)
- index interface: `pkg/cache/index/interface_test.go` (table-driven)
- index bbolt: `pkg/cache/index/bbolt/bbolt_test.go`
- file_store: `pkg/cache/files/file_store_test.go`
- uploads journal: `pkg/cache/index/...` tests to cover uploads persistence
- uploader: `pkg/cache/uploader/uploader_test.go` (fake backend)
- cleaner: `pkg/cache/cleaner/cleaner_test.go`
- integration: `pkg/cache/integration_test.go` (small end-to-end)

Design notes and constraints
- Keep `CacheIndex` minimal and orthogonal. Avoid exposing DB internals in other packages.
- All public comments and test names must be in English.
- Writes must be atomic at range granularity: temp buffer → pwrite → fsync → index update.
- Pluggability: index constructors should be registered via factory (e.g., `index.New("bbolt", opts)`); adapters must implement same `CacheIndex` interface.

Questions (before coding)
1. Preferred location for on-disk cache directory by default — use `~/.tigrisfs/cache/<diskID>` (PRD) — confirm.
2. Should uploads journal be a separate bbolt bucket or a separate DB file `uploads.db`? PRD shows both; recommendation: store uploads in the same `index.db` under `uploads` bucket to keep atomicity.
3. For Windows sparse file support — do we accept a fallback (regular file) or prefer a Windows-specific sparse API? I recommend fallback + telemetry.

-- Answers provided by product/developer:
1. Default cache dir: confirmed `~/.tigrisfs/cache/<diskID>`.
2. Uploads journal: confirmed store under `index.db` in `uploads` bucket.
3. Windows sparse: accept fallback to regular files if sparse API is unavailable; attempt to use native sparse APIs first.

Development and testing platform note
- Initial development and test harnessing will be performed on Windows. All code must remain cross-platform, but early tests (unit and integration) will target Windows APIs and path behavior. When possible tests should also run on Linux in CI.

Test bucket note (works locally)
- The following test command and profile are verified to work for local testing against the test bucket endpoint:

```powershell
.\tigrisfs.exe --profile datasilo --memory-limit 1024 --endpoint http://192.168.1.20:9000 test H:
```

Use it for manual integration tests while developing uploader/reads/writes.

Now I will replace the existing `DEVPLAN.md` with this plan. After that I can create initial test scaffolding if you want.