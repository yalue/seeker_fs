package seeker_fs

import (
	"io"
	"os"
	"testing"
	"testing/fstest"
)

func TestSeekerFS(t *testing.T) {
	dirFS := os.DirFS("test_data/test_dir")
	data := NewSeekableBuffer()
	e := CreateSeekerFS(dirFS, data)
	if e != nil {
		t.Logf("Failed creating seeker FS: %s\n", e)
		t.FailNow()
	}
	sfs, e := LoadSeekerFS(data)
	if e != nil {
		t.Logf("Failed reading seeker FS: %s\n", e)
		t.FailNow()
	}
	expectedFiles := []string{
		"test1.txt",
		"test2.txt",
		"a",
		"b/c",
		"b/c/test1.txt",
		"b/c/test2.txt",
		"b/c/hi.png",
	}
	e = fstest.TestFS(dirFS, expectedFiles...)
	if e != nil {
		t.Logf("Sanity-check failed; didn't get expected files in original "+
			"dir: %s\n", e)
		t.Fail()
	}
	e = fstest.TestFS(sfs, expectedFiles...)
	if e != nil {
		t.Logf("TestFS failed: %s\n", e)
		t.FailNow()
	}
	_, e = sfs.Open("b/c/test4.txt")
	if e == nil {
		t.Logf("Didn't get expected error when opening b/c/test4.txt\n")
		t.FailNow()
	}
	t.Logf("Got expected error when opening nonexistent file: %s\n", e)
	f, e := sfs.Open("b/c/test2.txt")
	if e != nil {
		t.Logf("Failed opening b/c/test2.txt: %s\n", e)
		t.FailNow()
	}
	expectedContents := "test2"
	// The +3 is used here to make sure we don't over-read the file.
	fileContents := make([]byte, len(expectedContents)+3)
	n, e := f.Read(fileContents)
	// We don't return io.EOF until the *next* read.
	if e != nil {
		t.Logf("Failed reading b/c/test2.txt: %s\n", e)
		t.FailNow()
	}
	if n != len(expectedContents) {
		t.Logf("Didn't get expected length when reading file contents. "+
			"Expected %d, got %d.\n", len(fileContents), n)
		t.FailNow()
	}
	if string(fileContents[0:n]) != expectedContents {
		t.Logf("Didn't get expected contents of b/c/test2.txt. Expected %s, "+
			"got %v.\n", expectedContents, string(fileContents[0:n]))
		t.FailNow()
	}
	_, e = f.Read(fileContents)
	if e != io.EOF {
		t.Logf("Didn't get expected EOF error when reading file that's " +
			"already done.\n")
		t.FailNow()
	}
	t.Logf("Got expected error when reading file that's already read: %s\n", e)
	f.Close()
}
