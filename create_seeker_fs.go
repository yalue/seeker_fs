package seeker_fs

// This file contains code related to creating a new seeker_fs from a different
// FS.
import (
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"sort"
)

// Holds a file that needs to have its *data* appended to the output stream.
// After appending the data, we also update its header.
type fileToProcess struct {
	// The file with the data to be written to the output stream. We'll write
	// its name and data to the output stream.  If its a directory, we'll add
	// its entries to the queue of files to process, too. Will be closed after
	// processing.
	toProcess fs.File
	// The path to this file. Will be "." for the root directory, the rest
	// of the files will *not* include the leading ".".
	path string
	// The offset in the output stream reserved for the file's header.
	fileHeaderOffset int64
	// The depth of this file. (The number of directories past root that
	// must be traversed to reach it.)
	depth int
}

// Used to specify limits on the creation of a SeekerFS.
type CreateFSSettings struct {
	// The maximum depth to which directories are traversed. Unlimited if <= 0.
	MaxDepth int
	// The maximum number of total bytes to write to the output. Unlimited if
	// <= 0. This limit is on the maximum offset to which the WriteSeeker will
	// be written; data overwriting earlier offsets without expanding the size
	// of the buffer do not count towards this limit.
	MaxOutputSize int64
	// The maximum total number of files and directories to write to the
	// output. Unlimited if <= 0.
	MaxTotalEntries int64
	// If non-nil, creating the SeekerFS will result in human-readable status
	// messages to this.
	StatusLog io.Writer
}

// A simple type to wrap our depth-first traversal.
type outputQueue struct {
	// A queue (well rather, a stack) of files that need to have their data
	// written to the output.
	unprocessed []fileToProcess
	// The FS we're copying. We need to preserve this so we can open files
	// beyond the first.
	inputFS fs.FS
	// The output data stream.
	output io.WriteSeeker
	// Specifies limits on the amount of data to write, etc.
	settings *CreateFSSettings
	// The number of files and directories that have been enqueued so far,
	// including those that have already been processed.
	totalFilesWritten int64
}

func (q *outputQueue) LogStatus(format string, args ...interface{}) {
	if q.settings.StatusLog == nil {
		return
	}
	fmt.Fprintf(q.settings.StatusLog, format, args...)
}

// Returns the current offset in the output data stream, or an error if it
// can't be determined.
func (q *outputQueue) currentOffset() (int64, error) {
	toReturn, e := q.output.Seek(0, io.SeekCurrent)
	if e != nil {
		return 0, fmt.Errorf("Couldn't determine offset in output data: %s", e)
	}
	return toReturn, nil
}

// Seeks to the end of the output data stream, for outputting new data. Returns
// the current offset of the end of the stream.
func (q *outputQueue) seekToEnd() (int64, error) {
	newOffset, e := q.output.Seek(0, io.SeekEnd)
	if e != nil {
		return 0, fmt.Errorf("Couldn't seek to end of output data: %w", e)
	}
	return newOffset, e
}

// Checks q's settings to see if writing data up to the given end offset
// violates the maximum number of bytes written. Returns a suitable error if
// so. Otherwise, returns nil.
func (q *outputQueue) checkWriteLimit(newEnd int64) error {
	limit := q.settings.MaxOutputSize
	if limit <= 0 {
		return nil
	}
	if newEnd > limit {
		return fmt.Errorf("Output size limit (%d bytes) exceeded: trying to "+
			"write %d bytes", limit, newEnd)
	}
	return nil
}

// Writes the arbitrary toWrite object at the end of the output stream, and
// returns the offset where they were written (the stream's size before the new
// data was written).
func (q *outputQueue) writeDataAndGetLocation(toWrite interface{}) (int64,
	error) {
	toReturn, e := q.seekToEnd()
	if e != nil {
		return 0, e
	}
	e = q.checkWriteLimit(toReturn + int64(binary.Size(toWrite)))
	if e != nil {
		return 0, e
	}
	e = binary.Write(q.output, binary.LittleEndian, toWrite)
	if e != nil {
		return 0, fmt.Errorf("Failed writing content: %w", e)
	}
	return toReturn, nil
}

// Writes the given arbitrary object at the given offset in the output stream.
func (q *outputQueue) writeDataAtLocation(toWrite interface{},
	offset int64) error {
	_, e := q.output.Seek(offset, io.SeekStart)
	if e != nil {
		return fmt.Errorf("Couldn't seek to offset %d: %w", offset, e)
	}
	e = q.checkWriteLimit(offset + int64(binary.Size(toWrite)))
	if e != nil {
		return e
	}
	e = binary.Write(q.output, binary.LittleEndian, toWrite)
	if e != nil {
		return fmt.Errorf("Failed writing content at offset %d: %w", offset, e)
	}
	return nil
}

// Reserves space for the file's header in the output stream (by writing the
// correct number of zeros), and enqueues the file in the list of files with
// content to be written.
func (q *outputQueue) reserveHeaderAndEnqueue(f fs.File, path string,
	depth int) error {
	// Check the limit on the number of files to write.
	fileLimit := q.settings.MaxTotalEntries
	if (fileLimit > 0) && (q.totalFilesWritten >= fileLimit) {
		return fmt.Errorf("Exceeded limit of %d total files", fileLimit)
	}
	q.totalFilesWritten++
	depthLimit := q.settings.MaxDepth
	if (depthLimit > 0) && (depth > depthLimit) {
		return fmt.Errorf("Exceeded directory depth limit of %d", depthLimit)
	}
	// Write an empty header to the end of the stream.
	headerOffset, e := q.writeDataAndGetLocation(File{})
	if e != nil {
		return fmt.Errorf("Failed reserving space for file %s header: %w",
			path, e)
	}
	toEnqueue := fileToProcess{
		toProcess:        f,
		path:             path,
		fileHeaderOffset: headerOffset,
		depth:            depth,
	}
	q.unprocessed = append(q.unprocessed, toEnqueue)
	return nil
}

// Converts the given fs.File into a seeker_fs.File struct, without NameOffset,
// DataOffset, or Size being set.
func getSeekerFSHeader(info fs.FileInfo) *File {
	var toReturn File
	copy(toReturn.Magic[:], []byte("1337FILE"))
	toReturn.Mode = uint64(info.Mode())
	name := info.Name()
	copy(toReturn.ShortName[0:8], []byte(name))
	toReturn.NameSize = uint64(len(name))
	toReturn.ModTime = uint64(info.ModTime().Unix())
	return &toReturn
}

// Requires the queueEntry to be a regular file; writes its name and content to
// the output stream, followed by writing its header.
func (q *outputQueue) writeFileContent(queueEntry *fileToProcess,
	stat fs.FileInfo) error {
	var e error
	var nameOffset, dataOffset int64
	f := queueEntry.toProcess
	name := stat.Name()
	fullPath := queueEntry.path

	// Only write names longer than 8 bytes, as they otherwise fit in the
	// ShortName field of the File struct.
	if len(name) > 8 {
		nameOffset, e = q.writeDataAndGetLocation([]byte(name))
		if e != nil {
			return fmt.Errorf("Failed writing name of %s: %w", fullPath, e)
		}
	}

	// Write the file's content to the output stream. We'll use io.CopyN here,
	// to let the io package take care of intermediate buffering.
	size := stat.Size()
	if size > 0 {
		dataOffset, e = q.seekToEnd()
		if e != nil {
			return fmt.Errorf("Failed seeking to data location: %w", e)
		}
		e = q.checkWriteLimit(dataOffset + size)
		if e != nil {
			return e
		}
		_, e = io.CopyN(q.output, f, size)
		if e != nil {
			return fmt.Errorf("Failed writing content of %s: %w", fullPath, e)
		}
	}

	// We have the info we need, so now write the header at its reserved spot.
	header := getSeekerFSHeader(stat)
	header.NameOffset = uint64(nameOffset)
	header.Size = uint64(size)
	header.DataOffset = uint64(dataOffset)
	e = q.writeDataAtLocation(header, queueEntry.fileHeaderOffset)
	if e != nil {
		return fmt.Errorf("Failed updating header for %s: %w", fullPath, e)
	}
	return nil
}

// Implements sort.Interface to sort entries by name, as the SeekerFS requires
// directory entries to be sorted alphabetically.
type dirEntrySlice []fs.DirEntry

func (s dirEntrySlice) Len() int {
	return len(s)
}

func (s dirEntrySlice) Less(a, b int) bool {
	return s[a].Name() < s[b].Name()
}

func (s dirEntrySlice) Swap(a, b int) {
	s[a], s[b] = s[b], s[a]
}

// Requires the queueEntry to be for a directory, and that the directory to
// implement ReadDirFile. Takes a FileInfo object for convenience. Reserves
// space and enqueues the directory's children for later processing, then
// updates the directory's File header.
func (q *outputQueue) writeDirContent(queueEntry *fileToProcess,
	stat fs.FileInfo) error {
	fullPath := queueEntry.path
	dir, ok := queueEntry.toProcess.(fs.ReadDirFile)
	if !ok {
		return fmt.Errorf("Directory %s doesn't implement ReadDirFile",
			fullPath)
	}
	name := stat.Name()

	var nameOffset int64
	var e error
	// As with regular files, we only need to write dir names if they won't
	// fit in the ShortName field.
	if len(name) > 8 {
		nameOffset, e = q.writeDataAndGetLocation([]byte(name))
		if e != nil {
			return fmt.Errorf("Failed writing name of dir %s: %w", fullPath, e)
		}
	}

	entries, e := dir.ReadDir(-1)
	if e != nil {
		return fmt.Errorf("Failed reading files in dir %s: %w", fullPath, e)
	}

	// If the directory contained no files, write its header and return early.
	if len(entries) == 0 {
		header := getSeekerFSHeader(stat)
		header.NameOffset = uint64(nameOffset)
		e = q.writeDataAtLocation(header, queueEntry.fileHeaderOffset)
		if e != nil {
			return fmt.Errorf("Failed writing header for empty dir %s: %w",
				fullPath, e)
		}
		return nil
	}

	// Get the offset before we start writing the headers.
	dataOffset, e := q.seekToEnd()
	if e != nil {
		return fmt.Errorf("Failed getting offset of dir %s contents: %w",
			fullPath, e)
	}

	// Open and enqueue all of the directory entries, in their sorted order.
	sort.Sort(dirEntrySlice(entries))
	for _, dirEntry := range entries {
		// Don't include a leading "./" in paths in the root directory.
		var newPath string
		if fullPath == "." {
			newPath = dirEntry.Name()
		} else {
			newPath = fullPath + "/" + dirEntry.Name()
		}
		newFile, e := q.inputFS.Open(newPath)
		if e != nil {
			return fmt.Errorf("Failed opening %s: %w", newPath, e)
		}
		e = q.reserveHeaderAndEnqueue(newFile, newPath, queueEntry.depth+1)
		if e != nil {
			return fmt.Errorf("Failed enqueueing %s: %w", newPath, e)
		}
	}

	// Finally, update the header for this directory.
	header := getSeekerFSHeader(stat)
	header.NameOffset = uint64(nameOffset)
	header.DataOffset = uint64(dataOffset)
	header.Size = uint64(len(entries))
	e = q.writeDataAtLocation(header, queueEntry.fileHeaderOffset)
	if e != nil {
		return fmt.Errorf("Failed updating header for dir %s: %w", fullPath, e)
	}
	return nil
}

// Removes one file from the top of the stack, writes its data to the output,
// and, if it's a directory, adds its children to the queue to process. Closes
// the file before returning.
func (q *outputQueue) processNextFile() error {
	if len(q.unprocessed) == 0 {
		return fmt.Errorf("No files are left to process")
	}
	// Pop an item from the end of the queue.
	toProcess := q.unprocessed[len(q.unprocessed)-1]
	q.unprocessed = q.unprocessed[0 : len(q.unprocessed)-1]

	// Error or not, we're done with this file after this function.
	f := toProcess.toProcess
	defer f.Close()

	// Handle the file differently based on if it's a regular file or a
	// directory.
	stat, e := f.Stat()
	if e != nil {
		return fmt.Errorf("Stat() failed for file %s: %w", toProcess.path, e)
	}
	if !stat.IsDir() {
		e = q.writeFileContent(&toProcess, stat)
		if e != nil {
			return fmt.Errorf("Failed writing content for file %s: %w",
				toProcess.path, e)
		}
		q.LogStatus("Wrote %s OK (%d bytes).\n", toProcess.path, stat.Size())
		return nil
	}
	e = q.writeDirContent(&toProcess, stat)
	if e != nil {
		return fmt.Errorf("Failed writing content for directory %s: %w",
			toProcess.path, e)
	}
	q.LogStatus("Wrote directory content for %s OK.\n", toProcess.path)
	return nil
}

// Copies the entire contents of the arbitrary filesystem f into a new
// SeekerFS, writing the SeekerFS's bytes to the output data stream. Returns an
// error if any occurs. May be memory intensive, as it may potentially need to
// buffer many directory entries before writing them to the output stream. The
// settings struct enables setting limits on how many files or bytes to
// process. Set the settings argument to nil to use default options. Returns an
// error (likely with a partially-written output) if any limits are exceeded.
func CreateSeekerFS(f fs.FS, output io.WriteSeeker,
	settings *CreateFSSettings) error {
	rootFile, e := f.Open(".")
	if e != nil {
		return fmt.Errorf("Error opening root file: %w", e)
	}
	// If no settings were provided, simply use the default zero values.
	if settings == nil {
		settings = &CreateFSSettings{}
	}
	queue := outputQueue{
		unprocessed: make([]fileToProcess, 0, 1000),
		inputFS:     f,
		output:      output,
		settings:    settings,
	}

	// Start the encoding by enqueuing the root directory.
	e = (&queue).reserveHeaderAndEnqueue(rootFile, ".", 0)
	if e != nil {
		return fmt.Errorf("Error enqueuing root directory for processing: %w",
			e)
	}

	// This is just a basic depth-first loop until everything is written.
	for len(queue.unprocessed) != 0 {
		e = (&queue).processNextFile()
		if e != nil {
			return fmt.Errorf("Error writing file to output: %w", e)
		}
	}
	return nil
}
