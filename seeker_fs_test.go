package seeker_fs

import (
	"github.com/yalue/byte_utils"
	"io"
	"os"
	"testing"
	"testing/fstest"
	"time"
)

func NewSeekableBuffer() *byte_utils.SeekableBuffer {
	return byte_utils.NewSeekableBuffer()
}

func TestSeekerFS(t *testing.T) {
	dirFS := os.DirFS("test_data/test_dir")
	data := NewSeekableBuffer()
	e := CreateSeekerFS(dirFS, data, nil)
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

// Returns a MapFile for the simple MapFS. The file's content is set to the
// given string.
func newMapFile(content string) *fstest.MapFile {
	return &fstest.MapFile{
		Data:    []byte(content),
		Mode:    0777,
		ModTime: time.Unix(0, 0),
		Sys:     nil,
	}
}

func TestCreationLimits(t *testing.T) {
	baseFS := fstest.MapFS(make(map[string]*fstest.MapFile))
	baseFS["file1"] = newMapFile("hi")
	baseFS["file2"] = newMapFile("hi 2")
	settings := CreateFSSettings{
		MaxDepth:        0,
		MaxOutputSize:   0,
		MaxTotalEntries: 1,
	}

	// First, see if the limit on total file count works.
	data := NewSeekableBuffer()
	e := CreateSeekerFS(baseFS, data, &settings)
	if e == nil {
		t.Logf("Didn't get expected error when violating limit on number of " +
			"files in the FS.\n")
		t.FailNow()
	}
	t.Logf("Got expected error when violating file count limit: %s\n", e)
	settings.MaxTotalEntries = 8
	data = NewSeekableBuffer()
	e = CreateSeekerFS(baseFS, data, &settings)
	if e != nil {
		t.Logf("Failed creating FS with a limit on file count: %s\n", e)
		t.FailNow()
	}

	// Next, check the limit on output size.
	settings.MaxTotalEntries = 0
	settings.MaxOutputSize = 5000
	content := ""
	for i := 0; i < 10000; i++ {
		content += "A"
	}
	baseFS["file3"] = newMapFile(content)
	data = NewSeekableBuffer()
	e = CreateSeekerFS(baseFS, data, &settings)
	if e == nil {
		t.Logf("Didn't get expected error when violating size limit.\n")
		t.FailNow()
	}
	t.Logf("Got expected error when violating the size limit: %s\n", e)
	settings.MaxOutputSize = 20000
	data = NewSeekableBuffer()
	e = CreateSeekerFS(baseFS, data, &settings)
	if e != nil {
		t.Logf("Failed creating FS with a limit on total size: %s\n", e)
		t.FailNow()
	}

	// Last, check the limit on max depth
	settings.MaxOutputSize = 0
	settings.MaxDepth = 6
	baseFS["a/b/c/d/e/f/g/h/i/j/k/l/file4"] = newMapFile("Wow!")
	data = NewSeekableBuffer()
	e = CreateSeekerFS(baseFS, data, &settings)
	if e == nil {
		t.Logf("Didn't get expected error when violating depth limit.\n")
		t.FailNow()
	}
	t.Logf("Got expected error when violating the depth limit: %s\n", e)
	settings.MaxDepth = 20
	data = NewSeekableBuffer()
	e = CreateSeekerFS(baseFS, data, &settings)
	if e != nil {
		t.Logf("Failed creating FS with a limit on directory depth: %s\n", e)
		t.FailNow()
	}
}
