package seeker_fs

import (
	"io"
	"testing"
)

func TestSeekableBuffer(t *testing.T) {
	b := NewSeekableBuffer()
	tmp := make([]byte, 10)
	_, e := b.Read(tmp)
	if e == nil {
		t.Logf("Didn't get expected error for reading empty buffer.\n")
		t.Fail()
	} else {
		t.Logf("Got expected error for reading empty buffer: %s\n", e)
	}

	// Test that seeking past the buffer end expands the buffer without an
	// error.
	offset, e := b.Seek(10, io.SeekCurrent)
	if e != nil {
		t.Logf("Error seeking to offset 10: %s\n", e)
		t.FailNow()
	}
	if offset != 10 {
		t.Logf("Expected an offset of 10 after seeking: %s\n", e)
		t.FailNow()
	}

	// Test writing 10 bytes of data, expanding the data.
	for i := range tmp {
		tmp[i] = byte(i)
	}
	n, e := b.Write(tmp)
	if e != nil {
		t.Logf("Error writing data to buffer: %s\n", e)
		t.FailNow()
	}
	if n != len(tmp) {
		t.Logf("Wrote %d bytes, expected to write %d.\n", n, len(tmp))
		t.Fail()
	}

	// Make sure that:
	//  - The early part of the buffer is still 0s.
	//  - The data was written starting at the correct offset.
	_, e = b.Seek(8, io.SeekStart)
	if e != nil {
		t.Logf("Failed seeking to offset 5: %s\n", e)
		t.FailNow()
	}
	_, e = b.Read(tmp[0:5])
	if e != nil {
		t.Logf("Failed reading 5 bytes at offset 5: %s\n", e)
		t.FailNow()
	}
	expectedData := []byte{0, 0, 0, 1, 2}
	for i := range expectedData {
		if tmp[i] != expectedData[i] {
			t.Logf("Didn't get expected read contents: %v vs %v\n",
				tmp, expectedData)
			t.FailNow()
		}
	}

	// Make sure that we fail setting the offset to something negative.
	// (Required by the Seek interface.)
	_, e = b.Seek(-12, io.SeekStart)
	if e == nil {
		t.Logf("Didn't get expected error when seeking to negative offset.\n")
		t.FailNow()
	}
	t.Logf("Got expected error when seeking to negative offset: %s\n", e)

	// Make sure that writing something that doesn't require expanding the
	// buffer won't add to its length.
	currentLength, e := b.Seek(0, io.SeekEnd)
	if e != nil {
		t.Logf("Failed getting length of buffer: %s\n", e)
		t.FailNow()
	}
	if currentLength != 20 {
		t.Logf("Got incorrect length of buffer. Expected 20, got %d.\n",
			currentLength)
		t.FailNow()
	}
	_, e = b.Seek(2, io.SeekStart)
	if e != nil {
		t.Logf("Failed setting offset in buffer to byte 2: %s\n", e)
		t.FailNow()
	}
	_, e = b.Write([]byte{123, 211})
	if e != nil {
		t.Logf("Failed writing arbitrary 2 bytes to buffer: %s\n", e)
		t.FailNow()
	}
	currentLength, e = b.Seek(0, io.SeekEnd)
	if e != nil {
		t.Logf("Failed getting length of buffer (2nd time): %s\n", e)
		t.FailNow()
	}
	if currentLength != 20 {
		t.Logf("Buffer expanded to %d bytes when it shouldn't have.\n",
			currentLength)
		t.FailNow()
	}
}
