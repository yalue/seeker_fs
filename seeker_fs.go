// The seeker_fs library implements go1.16's fs interface in a flat binary
// format. Create a new SeekerFS by passing an existing fs.FS to
// CreateSeekerFS, and open an existing packed FS by passing an io.ReadSeeker
// to LoadSeekerFS.
package seeker_fs

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"time"
)

// Inteneded to satisfy Go's io/fs.FS interface, and be writable to a flat
// contiguous buffer in memory.
type SeekerFS struct {
	// The underlying data stream containing our FS. Offset 0 *must* be a File
	// instance, containing a directory definition.
	data io.ReadSeeker
	// The "root" file of this FS. Useful when implementing the Sub() function.
	topFile *File
	// A mutex preventing concurrent access to the underlying stream; without
	// this, one reader may seek while another reader is trying to read. Must
	// be a pointer so that Sub() can return a new SeekerFS that shares a lock
	// for the underlying ReadSeeker.
	lock *sync.Mutex
}

// Holds the size of our *File struct, used for calculating byte offsets into
// the data stream in a couple places. Set during init().
var fileStructSize uint64

func init() {
	tmp := binary.Size(File{})
	// We know the struct must be at least 8 bytes, so anything 8 or under must
	// be an error.
	if tmp <= 8 {
		msg := fmt.Sprintf("Internal error: couldn't determine size of File "+
			"struct. Got incorrect result: %d bytes.", tmp)
		panic(msg)
	}
	fileStructSize = uint64(tmp)
}

// Convenience function to lock the FS's mutex.
func (f *SeekerFS) acquireLock() {
	f.lock.Lock()
}

// Unlocks the FS's mutex.
func (f *SeekerFS) releaseLock() {
	f.lock.Unlock()
}

// Seeks to the given absolute location in the data stream. Returns an error if
// one occurs. Assumes that f.lock is held.
func (f *SeekerFS) seek(location uint64) error {
	_, e := f.data.Seek(int64(location), io.SeekStart)
	return e
}

// Tries to read len(data) bytes into the data slice, starting at the given
// absolute location. Returns an error if one occurs. Assumes f.lock is held,
// and may change the current offset in f.data.
func (f *SeekerFS) readAtOffset(data []byte, location uint64) error {
	e := f.seek(location)
	if e != nil {
		return fmt.Errorf("Failed seeking to offset %d: %s", location, e)
	}
	_, e = io.ReadFull(f.data, data)
	if e != nil {
		return fmt.Errorf("Failed reading %d bytes at %d: %s", len(data),
			location, e)
	}
	return nil
}

// Returns a new SeekerFS based on the given underlying data stream. Returns an
// error if one occurs. Note that some errors (i.e. with an incorrectly
// formatted data stream) may not appear until files are read or opened. Must
// have a File struct at the start of the data stream.
func NewSeekerFS(data io.ReadSeeker) (*SeekerFS, error) {
	var topFile File
	e := binary.Read(data, binary.LittleEndian, &topFile)
	if e != nil {
		return nil, fmt.Errorf("Couldn't read an initial file entry at the "+
			"data start: %s", e)
	}
	e = (&topFile).Validate()
	if e != nil {
		return nil, fmt.Errorf("Invalid file entry at the data start: %s", e)
	}
	if !(&topFile).IsDir() {
		return nil, fmt.Errorf("The top file entry wasn't a directory")
	}
	return &SeekerFS{
		data:    data,
		topFile: &topFile,
		lock:    &sync.Mutex{},
	}, nil
}

// Holds a SeekerFS-format file or directory. All offsets are absolute (from
// the start of the SeekerFS data stream).
type File struct {
	// Must be the eight bytes "1337FILE"
	Magic [8]byte
	// The fs.FileMode bits, stored in a uint32
	Mode uint64
	// The first 8 bytes of the file's name. If NameSize is less than 8, then
	// the remaining bytes will be filled with 0.
	ShortName [8]byte
	// The offset of this file's name in the SeekerFS data stream.
	NameOffset uint64
	// The length of this file's name, in bytes. If this is 8 or less, then
	// NameOffset may be 0 and ShortName must contain the entire name.
	NameSize uint64
	// The offset of this file's data in the SeekerFS data stream. Or, if this
	// file is a directory, it will be the offset of its first File entry.
	// (Directory entries are always stored sequentially, and must be sorted by
	// name.)
	DataOffset uint64
	// The size, in bytes, of the file. Or, if the file is a directory, this
	// will contain the number of directory entries. Directories must not
	// contain more than 0x7fffffff entries.
	Size uint64
	// A 64-bit unix timestamp, for the modification time if available.
	ModTime uint64
}

// Returns true if and only if the File is a directory.
func (f *File) IsDir() bool {
	return fs.FileMode(f.Mode).IsDir()
}

// Returns the file's short name, postfixed with "..." if it was abbreviated.
// Won't fail, and should be reasonably fast, so useful for debugging.
func (f *File) GetShortName() string {
	if f.NameSize <= 8 {
		return string(f.ShortName[0:f.NameSize])
	}
	return string(f.ShortName[:]) + "..."
}

func (f *File) String() string {
	return f.GetShortName()
}

// Does some simple checks on the file's structure, to make sure basic rules
// are met. Returns nil if everything seems OK.
func (f *File) Validate() error {
	if string(f.Magic[:]) != "1337FILE" {
		return fmt.Errorf("Incorrect magic identifier")
	}
	if f.IsDir() && f.Size > 0x7fffffff {
		return fmt.Errorf("Contains too many directory entries")
	}
	return nil
}

// Satisfies the fs.File interface. Also satisfies the ReadDirFile interface,
// though ReadDir will return an error if called on a non-directory. Also
// satisfies the ReadSeeker interface, so SeekerFS's can hold additional
// SeekerFS's in their files.
type SeekerFSFile struct {
	// Points to the SeekerFS holding this file.
	p *SeekerFS
	// The metadata for the file itself.
	f *File
	// The current read offset into this file, or index of the next directory
	// entry to return by ReadDir (however, ReadDir can't seek backwards).
	readOffset uint64
}

// Satisfies the fs.FileInfo interface for a SeekerFSFile, as well as the
// DirEntry interface.
type SeekerFSFileInfo struct {
	FileName    string
	FileSize    uint64
	FileMode    fs.FileMode
	FileModTime uint64
}

func (n *SeekerFSFileInfo) Name() string {
	return n.FileName
}

func (n *SeekerFSFileInfo) Size() int64 {
	return int64(n.FileSize)
}

func (n *SeekerFSFileInfo) Mode() fs.FileMode {
	return n.FileMode
}

func (n *SeekerFSFileInfo) ModTime() time.Time {
	return time.Unix(int64(n.FileModTime), 0)
}

func (n *SeekerFSFileInfo) IsDir() bool {
	return n.FileMode.IsDir()
}

// We make this return a self-reference, so a SeekerFSFileInfo can also be used
// when listing directory entries.
func (n *SeekerFSFileInfo) Info() (fs.FileInfo, error) {
	return n, nil
}

// Also included so that this satisfies the DirEntry interface.
func (n *SeekerFSFileInfo) Type() fs.FileMode {
	return n.FileMode.Type()
}

func (n *SeekerFSFileInfo) Sys() interface{} {
	return nil
}

// Takes a lower-level file struct and a reference to the SeekerFS containing
// it, and returns the file's full name. Returns an error if one occurs.
func getFileName(f *File, p *SeekerFS) (string, error) {
	length := f.NameSize
	// "Fast path" for short names.
	if length <= 8 {
		return string(f.ShortName[0:length]), nil
	}
	// Otherwise we need to read the name from the SeekerFS' data stream.
	name := make([]byte, length)
	p.acquireLock()
	e := p.readAtOffset(name, f.NameOffset)
	p.releaseLock()
	if e != nil {
		return "", e
	}
	return string(name), nil
}

// Returns a SeekerFSFileInfo struct, which satisfies both fs.FileInfo and
// fs.DirEntry interfaces. Returns an error if one occurs.
func getFileInfo(f *File, p *SeekerFS) (*SeekerFSFileInfo, error) {
	name, e := getFileName(f, p)
	if e != nil {
		return nil, fmt.Errorf("Failed reading file name: %s", e)
	}
	return &SeekerFSFileInfo{
		FileName:    name,
		FileSize:    f.Size,
		FileMode:    fs.FileMode(f.Mode),
		FileModTime: f.ModTime,
	}, nil
}

func (f *SeekerFSFile) Stat() (fs.FileInfo, error) {
	return getFileInfo(f.f, f.p)
}

// Returns true if f is a directory.
func (f *SeekerFSFile) IsDir() bool {
	return f.f.IsDir()
}

func (f *SeekerFSFile) Close() error {
	f.p = nil
	f.f = nil
	f.readOffset = 0
	return nil
}

func (f *SeekerFSFile) Seek(offset int64, whence int) (int64, error) {
	if f.IsDir() {
		return 0, fmt.Errorf("Can't seek in a directory")
	}
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekEnd:
		newOffset = int64(f.f.Size) + offset
	case io.SeekCurrent:
		newOffset = int64(f.readOffset) + offset
	default:
		f.readOffset = 0
		return 0, fmt.Errorf("Invalid \"whence\" argument")
	}
	if newOffset < 0 {
		return 0, fmt.Errorf("Invalid new offset: %d", newOffset)
	}
	f.readOffset = uint64(newOffset)
	return newOffset, nil
}

// Reads the next chunk of data from the file into the given buffer. May return
// an io.EOF error if the whole file has already been read.
func (f *SeekerFSFile) Read(data []byte) (int, error) {
	if f.IsDir() {
		return 0, fmt.Errorf("File is a directory")
	}
	fileSize := f.f.Size
	if f.readOffset >= f.f.Size {
		return 0, io.EOF
	}

	// Make sure we don't go past the end of the file.
	bytesToRead := uint64(len(data))
	endOffset := f.readOffset + bytesToRead
	if endOffset > fileSize {
		endOffset = fileSize
		bytesToRead = fileSize - f.readOffset
	}

	// Actually read the data.
	e := f.p.readAtOffset(data[0:bytesToRead], f.f.DataOffset+f.readOffset)
	if e != nil {
		// We shouldn't just pass on an EOF error here, as it would be an error
		// for the underlying ReadSeeker rather than an error with our FS.
		return 0, fmt.Errorf("Failed obtaining file data: %s", e)
	}
	f.readOffset += bytesToRead
	return int(bytesToRead), nil
}

// Used to support the ReadDirFile interface. Returns a list of up to n
// directory entries if this file is a directory, otherwise returns an error.
// Returns all directory entries if n <= 0.
func (f *SeekerFSFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if !f.IsDir() {
		return nil, fmt.Errorf("Can't read dir entries in a regular file")
	}
	if f.readOffset >= f.f.Size {
		return nil, io.EOF
	}
	startEntry := f.readOffset
	var endEntry uint64
	if n <= 0 {
		endEntry = f.f.Size
	} else {
		endEntry = startEntry + uint64(n)
	}
	if endEntry > f.f.Size {
		endEntry = f.f.Size
	}
	rawEntries := make([]File, endEntry-startEntry)
	startOffset := f.f.DataOffset + startEntry*fileStructSize

	// Finally, read the data. We'll need to take the lock here so that other
	// readers won't seek to a different location while we do the read.
	f.p.acquireLock()
	e := f.p.seek(startOffset)
	if e != nil {
		f.p.releaseLock()
		return nil, fmt.Errorf("Failed seeking to dir entry in data stream: "+
			"%s", e)
	}
	e = binary.Read(f.p.data, binary.LittleEndian, rawEntries)
	f.p.releaseLock()
	if e != nil {
		return nil, fmt.Errorf("Failed reading dir entries in data stream: %s",
			e)
	}

	// Finally, convert each File struct to a SeekerFSFileInfo struct, which
	// satisfies the DirEntry interface.
	toReturn := make([]fs.DirEntry, len(rawEntries))
	for i := range rawEntries {
		toReturn[i], e = getFileInfo(&(rawEntries[i]), f.p)
		if e != nil {
			return nil, fmt.Errorf("Failed getting info for file %d/%d: %s",
				i+1, len(rawEntries), e)
		}
	}

	f.readOffset = endEntry
	return toReturn, nil
}

// Returns the low-level File struct corresponding to the entry at index n in
// directory f. Returns an error if f isn't a directory, if n isn't a valid
// index, or if any other error occurs.
func getDirEntry(f *File, p *SeekerFS, n int) (*File, error) {
	if !f.IsDir() {
		return nil, fmt.Errorf("File %s isn't a directory", f)
	}
	if n < 0 {
		return nil, fmt.Errorf("Invalid dir entry index: %d", n)
	}
	if uint64(n) >= f.Size {
		return nil, fmt.Errorf("%s contains %d entries, can't read index %d",
			f, f.Size, n)
	}

	// Done sanity checking, now read the struct.
	offset := f.DataOffset + uint64(n)*fileStructSize
	p.acquireLock()
	e := p.seek(offset)
	if e != nil {
		p.releaseLock()
		return nil, fmt.Errorf("Couldn't seek to entry %d of %s: %s", n, f, e)
	}
	toReturn := File{}
	e = binary.Read(p.data, binary.LittleEndian, &toReturn)
	p.releaseLock()
	if e != nil {
		return nil, fmt.Errorf("Error reading entry %d of %s: %s", n, f, e)
	}
	return &toReturn, nil
}

// Returns 0 if f's name equals toCheck. Uses string.Compare(a, b), where a is
// f's name, and b is toCheck.
func compareFileName(f *File, p *SeekerFS, toCheck string) (int, error) {
	// Optimized comparison if toCheck fits an entire ShortName.
	if (len(toCheck) <= 8) && (f.NameSize <= 8) {
		return strings.Compare(f.GetShortName(), toCheck), nil
	}
	// If we have strictly fewer than 8 chars, comparing against the short name
	// is good enough.
	if len(toCheck) < 8 {
		// We know that the ShortName has more than 8 bytes, because if it
		// didn't, we already would have returned due to the previous if block.
		return strings.Compare(string(f.ShortName[0:8]), toCheck), nil
	}
	// Likewise, if our ShortName is strictly less than 8 bytes, then comparing
	// it against toCheck is good enough.
	if f.NameSize < 8 {
		return strings.Compare(f.GetShortName(), toCheck), nil
	}
	// At this point, we know that both toCheck and our ShortName are at least
	// 8 bytes, but we can still see if those first 8 bytes differ.
	shortResult := strings.Compare(string(f.ShortName[0:8]), toCheck)
	if shortResult != 0 {
		return shortResult, nil
	}

	// Finally, we know the two strings are longer than 8 bytes, and the first
	// 8 bytes are identical, so we'll need to do a longer more expensive
	// comparison.
	longName, e := getFileName(f, p)
	if e != nil {
		return 1, fmt.Errorf("Error getting file %s full name: %w", f, e)
	}
	return strings.Compare(longName, toCheck), nil
}

// Returns the low-level File struct with the given name in directory f.
// Returns an error if f isn't a directory, or ErrNotExist if f doesn't contain
// a file with the given name. Note that the name is only a base name, and not
// a full path.
func getNamedDirEntry(f *File, p *SeekerFS, name string) (*File, error) {
	if !f.IsDir() {
		return nil, fmt.Errorf("File %s isn't a directory", f)
	}
	if f.Size == 0 {
		// The directory is empty.
		return nil, fs.ErrNotExist
	}

	// Do a binary search on the directory entries. NOTE: we already require
	// directories to contain at most 0x7fffffff entries, so casting to an int
	// should never overflow.
	beginIndex := 0
	endIndex := int(f.Size)
	var currentEntry *File
	var compareResult int
	var currentIndex int
	var e error
	for beginIndex <= endIndex {
		currentIndex = (beginIndex + endIndex) >> 1
		currentEntry, e = getDirEntry(f, p, currentIndex)
		if e != nil {
			return nil, fmt.Errorf("Error reading entry at index %d: %w",
				currentIndex, e)
		}
		compareResult, e = compareFileName(currentEntry, p, name)
		if e != nil {
			return nil, fmt.Errorf("Error comparing %s's name to %s: %w",
				currentEntry, name, e)
		}
		if compareResult == 0 {
			// The names are equal
			return currentEntry, nil
		}
		if compareResult < 0 {
			// currentEntry's name is less than name
			endIndex = currentIndex - 1
		} else {
			// currentEntry's name is greater than name
			beginIndex = currentIndex + 1
		}
	}
	return nil, fs.ErrNotExist
}

// Resolves a file path in FS p, rooted at the given topDir. Returns a
// fs.PathError for most invalid errors.
func resolveFilePath(topDir *File, p *SeekerFS, path string) (*File, error) {
	var e error
	if !fs.ValidPath(path) {
		return nil, fs.ErrInvalid
	}
	// "." is a shortcut for the root dir. No other path components are allowed
	// to be named "." (ValidPath checks for this).
	if path == "." {
		return topDir, nil
	}
	// The FS interface requires "/" to be the path separator. ValidPath also
	// validates the path doesn't start or end with "/", or contain an empty
	// element (i.e. "//").
	components := strings.Split(path, "/")

	// Resolve each path component in turn. getNamedDirEntry will ensure that
	// every element before the last is a directory.
	currentFile := topDir
	for _, name := range components {
		currentFile, e = getNamedDirEntry(currentFile, p, name)
		if e != nil {
			return nil, fmt.Errorf("Failed resolving %s in path %s: %w", name,
				path, e)
		}
	}
	return currentFile, nil
}

// The primary function required to satisfy the fs.FS interface.
func (p *SeekerFS) Open(path string) (fs.File, error) {
	f, e := resolveFilePath(p.topFile, p, path)
	if e != nil {
		return nil, &fs.PathError{"open", path, e}
	}
	return &SeekerFSFile{
		p:          p,
		f:          f,
		readOffset: 0,
	}, nil
}

// Implement the fs.SubFS interface, since we can implement it fairly
// efficiently.
func (p *SeekerFS) Sub(path string) (fs.FS, error) {
	f, e := resolveFilePath(p.topFile, p, path)
	if e != nil {
		return nil, &fs.PathError{"sub", path, e}
	}
	if !f.IsDir() {
		return nil, fmt.Errorf("File %s is not a directory", path)
	}
	// The FS shares the underlying data stream (and therefore must also share
	// the mutex), but simply has a different top-level file.
	return &SeekerFS{
		data:    p.data,
		topFile: f,
		lock:    p.lock,
	}, nil
}

// Copies the entire contents of the arbitrary filesystem f into a new
// SeekerFS, writing the SeekerFS's bytes to the output data stream. Returns an
// error if any occurs.
func CreateSeekerFS(f fs.FS, output io.Writer) error {
	// TODO (next): Implement function for converting an arbitrary fs.FS into a
	// SeekerFS.
	return fmt.Errorf("Not yet implemented!")
}
