// Copyright 2015 - 2017 Ka-Hing Cheung
// Copyright 2021 Yandex LLC
// Copyright 2024 Tigris Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Tests for a mounted UNIX (but not Windows) FUSE FS

//go:build !windows

package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/jacobsa/fuse/fuseops"
	"github.com/pkg/xattr"
	bench_embed "github.com/valandreev/tigrisfs/bench"
	test_embed "github.com/valandreev/tigrisfs/test"
	"golang.org/x/sys/unix"
	. "gopkg.in/check.v1"
)

func (s *GoofysTest) mountCommon(t *C, mountPoint string, sameProc bool) {
	err := os.MkdirAll(mountPoint, 0o700)
	if err == syscall.EEXIST {
		err = nil
	}
	t.Assert(err, IsNil)

	if !hasEnv("SAME_PROCESS_MOUNT") && !sameProc {

		region := ""
		if os.Getenv("REGION") != "" {
			region = " --region \"" + os.Getenv("REGION") + "\""
		}
		exe := os.Getenv("TIGRISFS_BINARY")
		if exe == "" {
			exe = "../tigrisfs"
		}
		c := exec.Command("/bin/bash", "-c",
			exe+" --debug_fuse --debug_s3 --log-format=console --no-log-color"+
				" --stat-cache-ttl "+s.fs.flags.StatCacheTTL.String()+
				" --log-file \"mount_"+t.TestName()+".log\""+
				" --endpoint \""+s.fs.flags.Endpoint+"\""+
				region+
				" "+s.fs.bucket+" "+mountPoint)
		testLog.Debug().Str("cmd", c.String()).Msg("mounting")
		err = c.Run()
		t.Assert(err, IsNil)

	} else {
		s.fs.flags.MountPoint = mountPoint
		s.mfs, err = mountFuseFS(s.fs)
		t.Assert(err, IsNil)
	}
}

func (s *GoofysTest) umount(t *C, mountPoint string) {
	var err error
	for i := 0; i < 10; i++ {
		err = TryUnmount(mountPoint)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
		} else {
			break
		}
	}
	t.Assert(err, IsNil)

	testLog.E(os.Remove(mountPoint))
}

func FsyncDir(dir string) error {
	fh, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = fh.Sync()
	if err != nil {
		testLog.E(fh.Close())
		return err
	}
	return fh.Close()
}

func IsAccessDenied(err error) bool {
	return err == syscall.EACCES
}

func (s *GoofysTest) SetUpSuite(t *C) {
	s.tmp = os.Getenv("TMPDIR")
	if s.tmp == "" {
		s.tmp = "/tmp"
	}
	testLog.E(os.WriteFile(s.tmp+"/fuse-test.sh", []byte(test_embed.FuseTestSh), 0o755))
	testLog.E(os.WriteFile(s.tmp+"/bench.sh", []byte(bench_embed.BenchSh), 0o755))
}

func (s *GoofysTest) runFuseTest(t *C, mountPoint string, umount bool, cmdArgs ...string) {
	s.mount(t, mountPoint)

	if umount {
		defer s.umount(t, mountPoint)
	}

	// if command starts with ./ or ../ then we are executing a
	// relative path and cannot do chdir
	chdir := cmdArgs[0][0] != '.'

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, "FAST=true")
	cmd.Env = append(cmd.Env, "LANG=C")
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	cmd.Env = append(cmd.Env, "CLEANUP=false")

	if false {
		lg := testLog.With().Logger()

		cmd.Stdout = lg
		cmd.Stderr = lg
	}

	if chdir {
		oldCwd, err := os.Getwd()
		t.Assert(err, IsNil)

		err = os.Chdir(mountPoint)
		t.Assert(err, IsNil)

		defer func() {
			testLog.E(os.Chdir(oldCwd))
		}()
	}

	err := cmd.Run()
	t.Assert(err, IsNil)
}

func (s *GoofysTest) TestFuse(t *C) {
	mountPoint := s.tmp + "/mnt" + s.fs.bucket

	s.runFuseTest(t, mountPoint, true, s.tmp+"/fuse-test.sh", mountPoint)
}

func (s *GoofysTest) TestFuseWithTTL(t *C) {
	s.fs.flags.StatCacheTTL = 60 * 1000 * 1000 * 1000
	mountPoint := s.tmp + "/mnt" + s.fs.bucket

	s.runFuseTest(t, mountPoint, true, s.tmp+"/fuse-test.sh", mountPoint)
}

func (s *GoofysTest) TestBenchLs(t *C) {
	s.fs.flags.StatCacheTTL = 1 * time.Minute
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.setUpTestTimeout(t, 20*time.Minute)
	s.runFuseTest(t, mountPoint, true, s.tmp+"/bench.sh", mountPoint, "ls")
}

func (s *GoofysTest) TestBenchCreate(t *C) {
	s.fs.flags.StatCacheTTL = 1 * time.Minute
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.runFuseTest(t, mountPoint, true, s.tmp+"/bench.sh", mountPoint, "create")
}

func (s *GoofysTest) TestBenchCreateParallel(t *C) {
	s.fs.flags.StatCacheTTL = 1 * time.Minute
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.runFuseTest(t, mountPoint, true, s.tmp+"/bench.sh", mountPoint, "create_parallel")
}

func (s *GoofysTest) TestBenchIO(t *C) {
	s.fs.flags.StatCacheTTL = 1 * time.Minute
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.runFuseTest(t, mountPoint, true, s.tmp+"/bench.sh", mountPoint, "io")
}

func (s *GoofysTest) TestBenchFindTree(t *C) {
	s.fs.flags.StatCacheTTL = 1 * time.Minute
	mountPoint := s.tmp + "/mnt" + s.fs.bucket

	s.runFuseTest(t, mountPoint, true, s.tmp+"/bench.sh", mountPoint, "find")
}

func (s *GoofysTest) TestIssue231(t *C) {
	if isTravis() {
		t.Skip("disable in travis, not sure if it has enough memory")
	}
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.runFuseTest(t, mountPoint, true, s.tmp+"/bench.sh", mountPoint, "issue231")
}

func (s *GoofysTest) TestFuseWithPrefix(t *C) {
	mountPoint := s.tmp + "/mnt" + s.fs.bucket

	s.fs.Shutdown()
	s.fs, _ = NewGoofys(context.Background(), s.fs.bucket+":testprefix", s.fs.flags)

	s.runFuseTest(t, mountPoint, true, s.tmp+"/fuse-test.sh", mountPoint)
}

func (s *GoofysTest) TestClientForkExec(t *C) {
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.mount(t, mountPoint)
	defer s.umount(t, mountPoint)
	file := mountPoint + "/TestClientForkExec"

	// Create new file.
	fh, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0o600)
	t.Assert(err, IsNil)
	defer func() { // Defer close file if it's not already closed.
		if fh != nil {
			testLog.E(fh.Close())
		}
	}()
	// Write to file.
	_, err = fh.WriteString("1.1;")
	t.Assert(err, IsNil)
	// The `Command` is run via fork+exec.
	// So all the file descriptors are copied over to the child process.
	// The child process 'closes' the files before exiting. This should
	// not result in goofys failing file operations invoked from the test.
	someCmd := exec.Command("echo", "hello")
	err = someCmd.Run()
	t.Assert(err, IsNil)
	// One more write.
	_, err = fh.WriteString("1.2;")
	t.Assert(err, IsNil)
	// Close file.
	err = fh.Close()
	t.Assert(err, IsNil)
	fh = nil
	// Check file content.
	content, err := os.ReadFile(file)
	t.Assert(err, IsNil)
	t.Assert(string(content), Equals, "1.1;1.2;")

	// Repeat the same excercise, but now with an existing file.
	fh, err = os.OpenFile(file, os.O_RDWR, 0o600)
	// Write to file.
	_, err = fh.WriteString("2.1;")
	// fork+exec.
	someCmd = exec.Command("echo", "hello")
	err = someCmd.Run()
	t.Assert(err, IsNil)
	// One more write.
	_, err = fh.WriteString("2.2;")
	t.Assert(err, IsNil)
	// Close file.
	err = fh.Close()
	t.Assert(err, IsNil)
	fh = nil
	// Verify that the file is updated as per the new write.
	content, err = os.ReadFile(file)
	t.Assert(err, IsNil)
	t.Assert(string(content), Equals, "2.1;2.2;")
}

func (s *GoofysTest) TestXAttrFuse(t *C) {
	if _, ok := s.cloud.(*ADLv1); ok {
		t.Skip("ADLv1 doesn't support metadata")
	}

	_, checkETag := s.cloud.Delegate().(*S3Backend)
	xattrPrefix := s.cloud.Capabilities().Name + "."

	// fuseLog.Level = logrus.DebugLevel
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.mount(t, mountPoint)
	defer s.umount(t, mountPoint)

	// STANDARD storage-class may be present or not
	expectedXattrs1 := xattrPrefix + "etag\x00" +
		xattrPrefix + "storage-class\x00" +
		"user.name\x00"
	expectedXattrs2 := xattrPrefix + "etag\x00" +
		"user.name\x00"

	var buf [1024]byte

	// error if size is too small (but not zero)
	_, err := unix.Listxattr(mountPoint+"/file1", buf[:1])
	t.Assert(err, Equals, unix.ERANGE)

	// 0 len buffer means interogate the size of buffer
	nbytes, err := unix.Listxattr(mountPoint+"/file1", nil)
	t.Assert(err, Equals, nil)
	if nbytes != len(expectedXattrs2) {
		t.Assert(nbytes, Equals, len(expectedXattrs1))
	}

	nbytes, err = unix.Listxattr(mountPoint+"/file1", buf[:nbytes])
	t.Assert(err, IsNil)
	if nbytes == len(expectedXattrs2) {
		t.Assert(string(buf[:nbytes]), Equals, expectedXattrs2)
	} else {
		t.Assert(string(buf[:nbytes]), Equals, expectedXattrs1)
	}

	_, err = unix.Getxattr(mountPoint+"/file1", "user.name", buf[:1])
	t.Assert(err, Equals, unix.ERANGE)

	nbytes, err = unix.Getxattr(mountPoint+"/file1", "user.name", nil)
	t.Assert(err, IsNil)
	t.Assert(nbytes, Equals, 9)

	nbytes, err = unix.Getxattr(mountPoint+"/file1", "user.name", buf[:nbytes])
	t.Assert(err, IsNil)
	t.Assert(nbytes, Equals, 9)
	t.Assert(string(buf[:nbytes]), Equals, "file1+/#\x00")

	if !s.cloud.Capabilities().DirBlob {
		// dir1 has no xattrs
		nbytes, err = unix.Listxattr(mountPoint+"/dir1", nil)
		t.Assert(err, IsNil)
		t.Assert(nbytes, Equals, 0)

		nbytes, err = unix.Listxattr(mountPoint+"/dir1", buf[:1])
		t.Assert(err, IsNil)
		t.Assert(nbytes, Equals, 0)
	}

	if checkETag {
		_, err = unix.Getxattr(mountPoint+"/file1", "s3.etag", buf[:1])
		t.Assert(err, Equals, unix.ERANGE)

		nbytes, err = unix.Getxattr(mountPoint+"/file1", "s3.etag", nil)
		t.Assert(err, IsNil)
		// 32 bytes md5 plus quotes
		t.Assert(nbytes, Equals, 34)

		nbytes, err = unix.Getxattr(mountPoint+"/file1", "s3.etag", buf[:nbytes])
		t.Assert(err, IsNil)
		t.Assert(nbytes, Equals, 34)
		t.Assert(string(buf[:nbytes]), Equals,
			"\"826e8142e6baabe8af779f5f490cf5f5\"")
	}
}

func (s *GoofysTest) TestPythonCopyTree(t *C) {
	s.clearPrefix(t, s.cloud, "dir5")

	mountPoint := s.tmp + "/mnt" + s.fs.bucket

	s.runFuseTest(t, mountPoint, true, "python3", "-c",
		"import shutil; shutil.copytree('dir2', 'dir5')",
		mountPoint)
}

func (s *GoofysTest) TestCreateRenameBeforeCloseFuse(t *C) {
	if s.azurite {
		// Azurite returns 400 when copy source doesn't exist
		// https://github.com/Azure/Azurite/issues/219
		// so our code to ignore ENOENT fails
		t.Skip("https://github.com/Azure/Azurite/issues/219")
	}

	mountPoint := s.tmp + "/mnt" + s.fs.bucket

	s.mount(t, mountPoint)
	defer s.umount(t, mountPoint)

	from := mountPoint + "/newfile"
	to := mountPoint + "/newfile2"

	fh, err := os.Create(from)
	t.Assert(err, IsNil)
	defer func() {
		// close the file if the test failed so we can unmount
		if fh != nil {
			testLog.E(fh.Close())
		}
	}()

	_, err = fh.WriteString("hello world")
	t.Assert(err, IsNil)

	err = os.Rename(from, to)
	t.Assert(err, IsNil)

	err = fh.Close()
	t.Assert(err, IsNil)
	fh = nil

	_, err = os.Stat(from)
	t.Assert(err, NotNil)
	pathErr, ok := err.(*os.PathError)
	t.Assert(ok, Equals, true)
	t.Assert(pathErr.Err, Equals, syscall.ENOENT)

	content, err := os.ReadFile(to)
	t.Assert(err, IsNil)
	t.Assert(string(content), Equals, "hello world")
}

func (s *GoofysTest) TestRenameBeforeCloseFuse(t *C) {
	mountPoint := s.tmp + "/mnt" + s.fs.bucket

	s.mount(t, mountPoint)
	defer s.umount(t, mountPoint)

	from := mountPoint + "/newfile"
	to := mountPoint + "/newfile2"

	err := os.WriteFile(from, []byte(""), 0o600)
	t.Assert(err, IsNil)

	fh, err := os.OpenFile(from, os.O_WRONLY, 0o600)
	t.Assert(err, IsNil)
	defer func() {
		// close the file if the test failed so we can unmount
		if fh != nil {
			testLog.E(fh.Close())
		}
	}()

	_, err = fh.WriteString("hello world")
	t.Assert(err, IsNil)

	err = os.Rename(from, to)
	t.Assert(err, IsNil)

	err = fh.Close()
	t.Assert(err, IsNil)
	fh = nil

	_, err = os.Stat(from)
	t.Assert(err, NotNil)
	pathErr, ok := err.(*os.PathError)
	t.Assert(ok, Equals, true)
	t.Assert(pathErr.Err, Equals, syscall.ENOENT)

	content, err := os.ReadFile(to)
	t.Assert(err, IsNil)
	t.Assert(string(content), Equals, "hello world")
}

func containsFile(dir, wantedFile string) bool {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, f := range files {
		if f.Name() == wantedFile {
			return true
		}
	}
	return false
}

// Notification tests:
// 1. Lookup and read a file, modify it out of band, refresh and check that
//    it returns the new size and data
// 2. Lookup and read a file, remove it out of band, refresh and check that
//    it does not exist and does not return an entry in unknown state
// 3. List a non-root directory, add a file in it, refresh, list it again
//    and check that it has the new file
// 4. List a non-root directory, modify a file in it, refresh dir, list it again
//    and check that the file is updated
// 5. List a non-root directory, remove a file in it, refresh dir, list it again
//    and check that the file does not exists
// 6-10. Same as 1-5, but with the root directory

// 3, 1, 2
func (s *GoofysTest) TestNotifyRefreshFile(t *C) {
	s.testNotifyRefresh(t, false, false)
}

// 3, 4, 5
func (s *GoofysTest) TestNotifyRefreshDir(t *C) {
	s.testNotifyRefresh(t, false, true)
}

// 8, 6, 7
func (s *GoofysTest) TestNotifyRefreshSubdir(t *C) {
	s.testNotifyRefresh(t, true, false)
}

// 8, 9, 10
func (s *GoofysTest) TestNotifyRefreshSubfile(t *C) {
	s.testNotifyRefresh(t, true, true)
}

func (s *GoofysTest) testNotifyRefresh(t *C, testInSubdir bool, testRefreshDir bool) {
	mountPoint := s.tmp + "/mnt" + s.fs.bucket
	s.mount(t, mountPoint)
	defer s.umount(t, mountPoint)

	testdir := mountPoint
	subdir := ""
	if testInSubdir {
		testdir += "/dir1"
		subdir = "dir1/"
	}
	refreshFile := testdir
	if !testRefreshDir {
		refreshFile += "/testnotify"
	}

	t.Assert(containsFile(testdir, "testnotify"), Equals, false)

	// Create file
	_, err := s.cloud.PutBlob(&PutBlobInput{
		Key:  subdir + "testnotify",
		Body: bytes.NewReader([]byte("foo")),
		Size: PUInt64(3),
	})
	t.Assert(err, IsNil)

	t.Assert(containsFile(testdir, "testnotify"), Equals, false)

	// Force-refresh
	err = xattr.Set(testdir, ".invalidate", []byte(""))
	t.Assert(err, IsNil)

	t.Assert(containsFile(testdir, "testnotify"), Equals, true)

	buf, err := os.ReadFile(testdir + "/testnotify")
	t.Assert(err, IsNil)
	t.Assert(string(buf), Equals, "foo")

	// Update file
	_, err = s.cloud.PutBlob(&PutBlobInput{
		Key:  subdir + "testnotify",
		Body: bytes.NewReader([]byte("baur")),
		Size: PUInt64(4),
	})
	t.Assert(err, IsNil)

	buf, err = os.ReadFile(testdir + "/testnotify")
	t.Assert(err, IsNil)
	t.Assert(string(buf), Equals, "foo")

	// Force-refresh
	err = xattr.Set(refreshFile, ".invalidate", []byte(""))
	t.Assert(err, IsNil)
	time.Sleep(500 * time.Millisecond)

	buf, err = os.ReadFile(testdir + "/testnotify")
	t.Assert(err, IsNil)
	t.Assert(string(buf), Equals, "baur")

	// Delete file
	_, err = s.cloud.DeleteBlob(&DeleteBlobInput{
		Key: subdir + "testnotify",
	})
	t.Assert(err, IsNil)

	buf, err = os.ReadFile(testdir + "/testnotify")
	t.Assert(err, IsNil)
	t.Assert(string(buf), Equals, "baur")

	// Force-refresh
	err = xattr.Set(refreshFile, ".invalidate", []byte(""))
	t.Assert(err, IsNil)

	// Refresh is done asynchronously (it needs kernel locks), so wait a bit
	for i := 0; i < 50; i++ {
		var f *os.File
		f, err = os.Open(testdir + "/testnotify")
		if err != nil {
			break
		}
		t.Assert(f.Close(), IsNil)
		time.Sleep(50 * time.Millisecond)
	}
	t.Assert(os.IsNotExist(err), Equals, true)

	t.Assert(containsFile(testdir, "testnotify"), Equals, false)
}

func (s *GoofysTest) TestNestedMountUnmountSimple(t *C) {
	t.Skip("Test for the strange 'child mount' feature, unusable from cmdline")
	childBucket := "goofys-test-" + RandStringBytesMaskImprSrc(16)
	childCloud := s.newBackend(t, childBucket, true)

	parFileContent := "parent"
	childFileContent := "child"
	parEnv := map[string]*string{
		"childmnt/x/in_child_and_par": &parFileContent,
		"childmnt/x/in_par_only":      &parFileContent,
		"nonchildmnt/something":       &parFileContent,
	}
	childEnv := map[string]*string{
		"x/in_child_only":    &childFileContent,
		"x/in_child_and_par": &childFileContent,
	}
	s.setupBlobs(s.cloud, t, parEnv)
	s.setupBlobs(childCloud, t, childEnv)

	rootMountPath := s.tmp + "/fusetesting/" + RandStringBytesMaskImprSrc(16)
	s.mountInside(t, rootMountPath)
	defer s.umount(t, rootMountPath)
	// Files under /tmp/fusetesting/ should all be from goofys root.
	verifyFileData(t, rootMountPath, "childmnt/x/in_par_only", &parFileContent)
	verifyFileData(t, rootMountPath, "childmnt/x/in_child_and_par", &parFileContent)
	verifyFileData(t, rootMountPath, "nonchildmnt/something", &parFileContent)
	verifyFileData(t, rootMountPath, "childmnt/x/in_child_only", nil)

	childMount := &Mount{"childmnt", childCloud, "", false}
	s.fs.Mount(childMount)
	// Now files under /tmp/fusetesting/childmnt should be from childBucket
	verifyFileData(t, rootMountPath, "childmnt/x/in_par_only", nil)
	verifyFileData(t, rootMountPath, "childmnt/x/in_child_and_par", &childFileContent)
	verifyFileData(t, rootMountPath, "childmnt/x/in_child_only", &childFileContent)
	// /tmp/fusetesting/nonchildmnt should be from parent bucket.
	verifyFileData(t, rootMountPath, "nonchildmnt/something", &parFileContent)

	s.fs.Unmount(childMount.name)
	// Child is unmounted. So files under /tmp/fusetesting/ should all be from goofys root.
	verifyFileData(t, rootMountPath, "childmnt/x/in_par_only", &parFileContent)
	verifyFileData(t, rootMountPath, "childmnt/x/in_child_and_par", &parFileContent)
	verifyFileData(t, rootMountPath, "nonchildmnt/something", &parFileContent)
	verifyFileData(t, rootMountPath, "childmnt/x/in_child_only", nil)
}

func (s *GoofysTest) TestUnmountBucketWithChild(t *C) {
	t.Skip("Test for the strange 'child mount' feature, unusable from cmdline")

	// This bucket will be mounted at ${goofysroot}/c
	cBucket := "goofys-test-" + RandStringBytesMaskImprSrc(16)
	cCloud := s.newBackend(t, cBucket, true)

	// This bucket will be mounted at ${goofysroot}/c/c
	ccBucket := "goofys-test-" + RandStringBytesMaskImprSrc(16)
	ccCloud := s.newBackend(t, ccBucket, true)

	pFileContent := "parent"
	cFileContent := "child"
	ccFileContent := "childchild"
	pEnv := map[string]*string{
		"c/c/x/foo": &pFileContent,
	}
	cEnv := map[string]*string{
		"c/x/foo": &cFileContent,
	}
	ccEnv := map[string]*string{
		"x/foo": &ccFileContent,
	}

	s.setupBlobs(s.cloud, t, pEnv)
	s.setupBlobs(cCloud, t, cEnv)
	s.setupBlobs(ccCloud, t, ccEnv)

	rootMountPath := s.tmp + "/fusetesting/" + RandStringBytesMaskImprSrc(16)
	s.mountInside(t, rootMountPath)
	defer s.umount(t, rootMountPath)
	// c/c/foo should come from root mount.
	verifyFileData(t, rootMountPath, "c/c/x/foo", &pFileContent)

	cMount := &Mount{"c", cCloud, "", false}
	s.fs.Mount(cMount)
	// c/c/foo should come from "c" mount.
	verifyFileData(t, rootMountPath, "c/c/x/foo", &cFileContent)

	ccMount := &Mount{"c/c", ccCloud, "", false}
	s.fs.Mount(ccMount)
	// c/c/foo should come from "c/c" mount.
	verifyFileData(t, rootMountPath, "c/c/x/foo", &ccFileContent)

	s.fs.Unmount(cMount.name)
	// c/c/foo should still come from "c/c" mount.
	verifyFileData(t, rootMountPath, "c/c/x/foo", &ccFileContent)
}

// Specific to "lowlevel" fuse, so also checked here
func (s *GoofysTest) TestConcurrentRefDeref(t *C) {
	fsint := NewGoofysFuse(s.fs)
	root := s.getRoot(t)

	lookupOp := fuseops.LookUpInodeOp{
		Parent: root.Id,
		Name:   "file1",
	}

	for i := 0; i < 20; i++ {
		err := fsint.LookUpInode(nil, &lookupOp)
		t.Assert(err, IsNil)
		t.Assert(lookupOp.Entry.Child, Not(Equals), 0)

		var wg sync.WaitGroup

		// The idea of this test is just that lookup->forget->lookup shouldn't crash with "Unknown inode: xxx"
		wg.Add(2)
		go func(op fuseops.LookUpInodeOp) {
			// we want to yield to the forget goroutine so that it's run first
			// to trigger this bug
			if i%2 == 0 {
				runtime.Gosched()
			}
			testLog.E(fsint.LookUpInode(nil, &op))
			wg.Done()
		}(lookupOp)
		go func(id fuseops.InodeID) {
			testLog.E(fsint.ForgetInode(nil, &fuseops.ForgetInodeOp{
				Inode: id,
				N:     1,
			}))
			wg.Done()
		}(lookupOp.Entry.Child)

		wg.Wait()

		testLog.E(fsint.ForgetInode(nil, &fuseops.ForgetInodeOp{
			Inode: lookupOp.Entry.Child,
			N:     1,
		}))
	}
}

func (s *GoofysTest) TestDirMTime(t *C) {
	s.fs.flags.StatCacheTTL = 1 * time.Minute
	// enable cheap to ensure GET dir/ will come back before LIST dir/
	s.fs.flags.Cheap = true

	root := s.getRoot(t)
	t.Assert(time.Time{}.Before(root.Attributes.Mtime), Equals, true)

	dir1, err := s.fs.LookupPath("dir1")
	t.Assert(err, IsNil)

	attr1 := dir1.GetAttributes()
	m1 := attr1.Mtime

	time.Sleep(2 * time.Second)

	dir2, err := dir1.MkDir("dir2")
	t.Assert(err, IsNil)

	attr2 := dir2.GetAttributes()
	m2 := attr2.Mtime
	t.Assert(m1.Add(2*time.Second).Before(m2), Equals, true)

	// dir1 didn't have an explicit mtime, so it should update now
	// that we did a mkdir inside it
	attr1 = dir1.GetAttributes()
	m1 = attr1.Mtime
	t.Assert(m1, Equals, m2)

	time.Sleep(2 * time.Second)

	// different dir2
	dir2, err = s.fs.LookupPath("dir2")
	t.Assert(err, IsNil)

	attr2 = dir2.GetAttributes()
	m2 = attr2.Mtime

	// this fails because we are listing dir/, which means we
	// don't actually see the dir blob dir2/dir3/ (it's returned
	// as common prefix), so we can't get dir3's mtime
	if false {
		// dir2/dir3/ exists and has mtime
		s.readDirIntoCache(t, dir2.Id)
		dir3, err := s.fs.LookupPath("dir2/dir3")
		t.Assert(err, IsNil)

		attr3 := dir3.GetAttributes()
		// setupDefaultEnv is before mounting
		t.Assert(attr3.Mtime.Before(m2), Equals, true)
	}

	time.Sleep(time.Second)

	params := &PutBlobInput{
		Key:  "dir2/newfile",
		Body: bytes.NewReader([]byte("foo")),
		Size: PUInt64(3),
	}
	_, err = s.cloud.PutBlob(params)
	t.Assert(err, IsNil)

	// dir2 could be already preloaded due to optimisations, it may have older mtime
	// FIXME: (maybe) update parent directory modification times when flushing files inside them
	s.fs.flags.StatCacheTTL = 1 * time.Second
	s.readDirIntoCache(t, dir2.Id)
	s.fs.flags.StatCacheTTL = 1 * time.Minute

	newfile, err := dir2.LookUp("newfile", false)
	t.Assert(err, IsNil)

	attr2New := dir2.GetAttributes()
	// mtime should reflect that of the latest object
	// GCS can return nano second resolution so truncate to second for compare
	t.Assert(attr2New.Mtime.Unix(), Equals, newfile.Attributes.Mtime.Unix())
	t.Assert(m2.Before(attr2New.Mtime), Equals, true)
}
