# AtomDB

AtomDB is a persistent key-value store written in Go. It implements a B+Tree storage engine from scratch without relying on external database libraries.

## Features

- **B+Tree Indexing**: Uses a copy-on-write B+Tree structure for efficient data retrieval.
- **Persistence**: Data is persisted to disk using memory mapping (mmap) and fsync.
- **Crash Recovery**: Implements a meta page mechanism to ensure database integrity across restarts.
- **Space Management**: Uses a free list to recycle used disk pages.

## Usage

### Running the Example

To run the included example which writes data to disk and reads it back:

```bash
go run .

```

### Basic API

You can use the database in your Go code as follows:

```go
package main

import (
    "fmt"
    "log"
)

func main() {
    db := &KV{Path: "data.db"}
    
    // Open the database
    if err := db.Open(); err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // Write a key-value pair
    if err := db.Set([]byte("key"), []byte("value")); err != nil {
        log.Fatal(err)
    }

    // Retrieve the value
    val, found := db.Get([]byte("key"))
    if found {
        fmt.Printf("Found value: %s\n", val)
    }
}

```

## References

This project is based on the book "Build Your Own Database From Scratch in Go" by James Smith.
