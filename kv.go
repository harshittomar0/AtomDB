package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
)

const DB_SIG = "BuildYourOwnDB06"

type KV struct {
	Path string
	fd   int
	tree BTree
	free FreeList
	mmap struct {
		chunks [][]byte // read-only mmap chunks
		total  int      // total mmap size
	}
	page struct {
		flushed uint64            // number of pages flushed to disk
		temp    [][]byte          // newly allocated pages (memory)
		updates map[uint64][]byte // updates to existing pages (memory)
	}
}

[cite_start]// Open initializes the DB file, mmap, and reads the meta page 
func (db *KV) Open() error {
	var err error
	// Open file in RDWR + CREATE mode
	db.fd, err = syscall.Open(db.Path, syscall.O_RDWR|syscall.O_CREAT, 0644)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}

	// File size check
	fi, _ := os.Stat(db.Path)
	fileSize := fi.Size()

	// Initialize callbacks
	db.page.updates = make(map[uint64][]byte)
	db.page.temp = make([][]byte, 0)

	db.free.get = db.pageRead
	db.free.new = db.pageAppend
	db.free.set = db.pageWrite

	db.tree.get = db.pageRead
	db.tree.new = db.pageAlloc
	db.tree.del = func(ptr uint64) { db.free.PushTail(ptr) }

	// Read Meta Page or Initialize
	if err := db.readRoot(fileSize); err != nil {
		return fmt.Errorf("readRoot: %w", err)
	}
	return nil
}

// Close ensures cleanup (unmap, close file)
func (db *KV) Close() {
	for _, chunk := range db.mmap.chunks {
		syscall.Munmap(chunk)
	}
	syscall.Close(db.fd)
}

[cite_start]// Get: High-level wrapper for BTree.Get 
func (db *KV) Get(key []byte) ([]byte, bool) {
	return db.tree.Get(key)
}

[cite_start]// Set: Inserts key-value and persists to disk 
func (db *KV) Set(key []byte, val []byte) error {
	[cite_start]// Initialize root if empty if db.tree.root == 0 {
		root := BNode(make([]byte, BTREE_PAGE_SIZE))
		root.setHeader(BNODE_LEAF, 2)
		// Dummy key to ensure lookup works
		nodeAppendKV(root, 0, 0, nil, nil)
		nodeAppendKV(root, 1, 0, key, val)
		db.tree.root = db.tree.new(root)
		return updateFile(db)
	}

	// Insert into B+Tree
	node := treeInsert(&db.tree, BNode(db.tree.get(db.tree.root)), key, val)
	nsplit, split := nodeSplit3(node)
	db.tree.del(db.tree.root)

	// Handle root split
	if nsplit > 1 {
		root := BNode(make([]byte, BTREE_PAGE_SIZE))
		root.setHeader(BNODE_NODE, nsplit)
		for i, knode := range split[:nsplit] {
			ptr, key := db.tree.new(knode), knode.getKey(0)
			nodeAppendKV(root, uint16(i), ptr, key, nil)
		}
		db.tree.root = db.tree.new(root)
	} else {
		db.tree.root = db.tree.new(split[0])
	}

	// Persist changes
	return updateFile(db)
}

[cite_start]// --- Persistence & Meta Page  ---

// Load meta page
func (db *KV) readRoot(fileSize int64) error {
	if fileSize == 0 {
		db.page.flushed = 2 // Meta page + 1st free list page
		db.free.headPage = 1
		db.free.tailPage = 1
		return nil
	}

	// Map the file
	if err := extendMmap(db, int(fileSize)); err != nil {
		return err
	}

	// Read meta
	data := db.mmap.chunks[0]
	if string(data[:16]) != DB_SIG {
		return errors.New("bad signature")
	}
	db.tree.root = binary.LittleEndian.Uint64(data[16:])
	db.page.flushed = binary.LittleEndian.Uint64(data[24:])
	db.free.headPage = binary.LittleEndian.Uint64(data[32:])
	db.free.headSeq = binary.LittleEndian.Uint64(data[40:])
	db.free.tailPage = binary.LittleEndian.Uint64(data[48:])
	db.free.tailSeq = binary.LittleEndian.Uint64(data[56:])
	return nil
}

// Save meta page
func saveMeta(db *KV) []byte {
	var data [64]byte // Meta page is small
	copy(data[:16], []byte(DB_SIG))
	binary.LittleEndian.PutUint64(data[16:], db.tree.root)
	binary.LittleEndian.PutUint64(data[24:], db.page.flushed)
	binary.LittleEndian.PutUint64(data[32:], db.free.headPage)
	binary.LittleEndian.PutUint64(data[40:], db.free.headSeq)
	binary.LittleEndian.PutUint64(data[48:], db.free.tailPage)
	binary.LittleEndian.PutUint64(data[56:], db.free.tailSeq)
	return data[:]
}

[cite_start]// Update file (2-phase commitish) 
func updateFile(db *KV) error {
	// 1. Write new nodes
	if err := writePages(db); err != nil {
		return err
	}
	// 2. Fsync file data
	if err := syscall.Fsync(db.fd); err != nil {
		return err
	}
	// 3. Update meta page
	if _, err := syscall.Pwrite(db.fd, saveMeta(db), 0); err != nil {
		return err
	}
	// 4. Fsync everything
	if err := syscall.Fsync(db.fd); err != nil {
		return err
	}

	// Update free list max seq for next txn
	db.free.SetMaxSeq()
	// Clear temp pages
	db.page.flushed += uint64(len(db.page.temp))
	db.page.temp = db.page.temp[:0]
	db.page.updates = make(map[uint64][]byte)
	return nil
}

func writePages(db *KV) error {
	// Extend mmap if needed
	newPagesSize := len(db.page.temp) * BTREE_PAGE_SIZE
	totalSize := int(db.page.flushed)*BTREE_PAGE_SIZE + newPagesSize
	if err := extendMmap(db, totalSize); err != nil {
		return err
	}

	// Append new pages
	offset := int64(db.page.flushed * BTREE_PAGE_SIZE)
	// Go does not have writev for file descriptors easily, so we loop Pwrite
	// (Efficiency optimization possible here)
	for i, page := range db.page.temp {
		if _, err := syscall.Pwrite(db.fd, page, offset+int64(i*BTREE_PAGE_SIZE)); err != nil {
			return err
		}
	}
	return nil
}

[cite_start]// --- Mmap Management  ---

func extendMmap(db *KV, size int) error {
	if size <= db.mmap.total {
		return nil
	}
	alloc := max(db.mmap.total, 64<<20) // Minimum 64MB grow
	for db.mmap.total+alloc < size {
		alloc *= 2
	}

	chunk, err := syscall.Mmap(
		db.fd, int64(db.mmap.total), alloc,
		syscall.PROT_READ, syscall.MAP_SHARED,
	)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	db.mmap.chunks = append(db.mmap.chunks, chunk)
	db.mmap.total += alloc
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- Page Access ---

func (db *KV) pageRead(ptr uint64) []byte {
	if node, ok := db.page.updates[ptr]; ok {
		return node
	}
	[cite_start]// Read from mmap 
	offset := uint64(0)
	for _, chunk := range db.mmap.chunks {
		end := offset + uint64(len(chunk))/BTREE_PAGE_SIZE
		if ptr < end {
			idx := (ptr - offset) * BTREE_PAGE_SIZE
			return chunk[idx : idx+BTREE_PAGE_SIZE]
		}
		offset = end
	}
	panic("bad ptr")
}

func (db *KV) pageAppend(node []byte) uint64 {
	ptr := db.page.flushed + uint64(len(db.page.temp))
	db.page.temp = append(db.page.temp, node)
	return ptr
}

func (db *KV) pageAlloc(node []byte) uint64 {
	if ptr := db.free.PopHead(); ptr != 0 {
		db.page.updates[ptr] = node
		return ptr
	}
	return db.pageAppend(node)
}

func (db *KV) pageWrite(ptr uint64) []byte {
	if node, ok := db.page.updates[ptr]; ok {
		return node
	}
	node := make([]byte, BTREE_PAGE_SIZE)
	copy(node, db.pageRead(ptr))
	db.page.updates[ptr] = node
	return node
}