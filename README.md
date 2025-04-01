[![unittests](https://github.com/tigrisdata/tigrisfs/actions/workflows/test.yaml/badge.svg)]()
[![xfstests](https://github.com/tigrisdata/tigrisfs/actions/workflows/xfstests.yaml/badge.svg)]()
[![cluster-test](https://github.com/tigrisdata/tigrisfs/actions/workflows/cluster_test.yaml/badge.svg)]()


TigrisFS is a high-performance FUSE-based file system for S3-compatible object storage written in Go.

# Overview

TigrisFS allows you to mount an S3 or compatible object store bucket as a local file system.

TigrisFS is based on [GeeseFS](https://github.com/yandex-cloud/geesefs), which is a fork of Goofys.
GeeseFS focused on solving performance problems which FUSE file systems based on S3 typically have,
especially with small files and metadata operations.
It solves these problems by using aggressive parallelism and asynchrony.

The goal of TigrisFS is to further improve on performance and reliability especially in distributed cluster setup.

In first release we worked on improving reliability of the code base:
 * Removed bundled, outdated AWS SDK with critical vulnerabilities.
 * Upgraded dependencies to fix security vulnerabilities.
 * Enabled race detector in tests and fixed all detected race conditions.
 * Enabled unit, xfs and cluster tests on every PR and push to main.

# Installation

## Prebuilt DEB and RPM packages 

* Download package from latest release: [DEB](https://github.com/tigrisdata/tigrisfs/releases/latest/download/tigrisfs-linux-amd64.deb), [RPM](https://github.com/tigrisdata/tigrisfs/releases/latest/download/tigrisfs-linux-amd64.rpm).
* Install it with `dpkg -i tigrisfs-linux-amd64.deb` or `rpm -i tigrisfs-linux-amd64.rpm`.
* TigrisFS can use standard AWS credentials file and environment variables. They will be picked up automatically if you have them configured.
  Alternatively, you can configure AWS credentials in `/etc/tigrisfs/defaults
* Now you can mount your S3 bucket with `systemctl --user start tigrisfs@<bucket>`.
* The mount will be available at `$HOME/mnt/tigrisfs/<bucket>`.

# License

Licensed under the Apache License, Version 2.0

See `LICENSE` and `AUTHORS`