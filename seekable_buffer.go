package seeker_fs

// This file defines an alternative to bytes.Buffer that supports the Seek
// method for writers.

import (
	"fmt"
	"io"
)

// This type implements an in-memory io.Reader, io.Writer, and io.Seeker.
type SeekableBuffer struct {
	// This will grow as needed, based on either the farthest write or seek
	// offset. Writing past the end of this will increase its size. Seeking
	// past the end will add zeros to the necessary size.
	Data []byte
	// The current read or write offset in the file. It's an error to read past
	// the end of the data, but writing pas
	Offset int64
}

func NewSeekableBuffer() *SeekableBuffer {
	return &SeekableBuffer{
		Data:   make([]byte, 0, 4096),
		Offset: 0,
	}
}

// Used internally to expand b.Data to the given size. Panics if the new size
// is smaller than the current size.
func (b *SeekableBuffer) expandToSize(size int64) {
	if size < int64(len(b.Data)) {
		panic("Trying to reduce buffer size")
	}
	// Hopefully we can usually do this, given our extra allocation
	if size <= int64(cap(b.Data)) {
		b.Data = b.Data[:size]
	}
	// Allocate an arbitrary amount of extra space, to hopefully reduce
	// reallocations.
	extraSize := 2 * (size - int64(len(b.Data)))
	// Limit the "bonus" allocation size to 1GB.
	extraSizeLimit := int64(1 << 30)
	if extraSize >= extraSizeLimit {
		extraSize = extraSizeLimit
	}
	newBuffer := make([]byte, size, size+extraSize)
	copy(newBuffer, b.Data)
	b.Data = newBuffer
}

// Sets the next read or write offset to the specified offset, returning the
// new offset. Expands the underlying buffer if the new offset is greater than
// its current size. Returns an error without changing the current offset if an
// error occurs.
func (b *SeekableBuffer) Seek(offset int64, whence int) (int64, error) {
	baseOffset := int64(0)
	switch whence {
	case io.SeekStart:
		baseOffset = 0
	case io.SeekEnd:
		baseOffset = int64(len(b.Data))
	case io.SeekCurrent:
		baseOffset = b.Offset
	default:
		return b.Offset, fmt.Errorf("Invalid \"whence\" for seek: %d", whence)
	}
	newOffset := baseOffset + offset
	// A negative offset is illegal
	if newOffset < 0 {
		return b.Offset, fmt.Errorf("Can't seek to negative offset: %d",
			newOffset)
	}
	// Anything less than len(data) doesn't require expansion.
	if newOffset <= int64(len(b.Data)) {
		b.Offset = newOffset
		return newOffset, nil
	}
	// Expand the size to be exactly equal to the new offset.
	b.expandToSize(newOffset)
	b.Offset = newOffset
	return newOffset, nil
}

// Provides the normal io.Reader interface.
func (b *SeekableBuffer) Read(dst []byte) (int, error) {
	start := b.Offset
	if start >= int64(len(b.Data)) {
		return 0, io.EOF
	}
	limit := start + int64(len(dst))
	if limit > int64(len(b.Data)) {
		limit = int64(len(b.Data))
	}
	copy(dst, b.Data[start:limit])
	b.Offset = limit
	return int(limit - start), nil
}

// Provides the normal io.Writer interface.
func (b *SeekableBuffer) Write(data []byte) (int, error) {
	start := b.Offset
	limit := start + int64(len(data))
	if limit > int64(len(b.Data)) {
		b.expandToSize(limit)
	}
	copy(b.Data[start:limit], data)
	return len(data), nil
}
