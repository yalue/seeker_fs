SeekerFS: Another Go Filesystem Implementation
==============================================

The `seeker_fs` package implements go1.16's filesystem interface in a flat
binary format on top of the `io.ReadSeeker` and `io.WriteSeeker` interfaces.

To create a SeekerFS, pass an existing `io/fs.FS` instance to the
`seeker_fs.CreateSeekerFS(...)` function.  (Note that the FS passed to
`CreateSeekerFS` must support the `ReadDirFile` interface on its directories,
including the root `.` file.)

To read an existing SeekerFS, pass an `io.ReadSeeker` to the
`LoadSeekerFS(...)` function.


Example Usage
-------------

```go
import (
    "archive/zip"
    "fmt"
    "github.com/yalue/byte_utils"
    "github.com/yalue/seeker_fs"
)

func main() {
    // ...

    // Assume that zipFile is a zip file that has been opened using os.Open,
    // and that fileSize is its size.
    zipFS, _ := zip.NewReader(zipFile, fileSize)

    // Create a SeekerFS in-memory.
    buffer := byte_utils.NewSeekableBuffer()
    e := seeker_fs.CreateSeekerFS(zipFS, buffer, nil)
    if e != nil {
        fmt.Printf("Error creating SeekerFS from zip: %s\n", e)
        return
    }

    // Get a SeekerFS instance from the in-memory buffer.
    sfs, e := seeker_fs.LoadSeekerFS(buffer)
    if e != nil {
        fmt.Printf("Error loading seeker FS: %s\n", e)
        return
    }

    // ... Use the "sfs" instance wherever you would use any other io/fs.FS.
}
```

