package main

import "encoding/binary"

const (
	FREE_LIST_HEADER = 8
	FREE_LIST_CAP    = (BTREE_PAGE_SIZE - FREE_LIST_HEADER) / 8
)

type FreeList struct {
	get func(uint64) []byte 
	new func([]byte) uint64 
	set func(uint64) []byte // get mutable page

	headPage uint64
	headSeq  uint64
	tailPage uint64
	tailSeq  uint64
	maxSeq   uint64 // Saved tailSeq to prevent reading newly added items
}

// Helper to calculate index in a list node
func seq2idx(seq uint64) int {
	return int(seq % FREE_LIST_CAP)
}

// Push a page ID to the tail 
func (fl *FreeList) PushTail(ptr uint64) {
	// Add to tail node
	setPtr(fl.set(fl.tailPage), seq2idx(fl.tailSeq), ptr)
	fl.tailSeq++

	// If tail is full, add new node
	if seq2idx(fl.tailSeq) == 0 {
		next, head := flPop(fl) // try to reuse head
		if next == 0 {
			// allocate new
			next = fl.new(make([]byte, BTREE_PAGE_SIZE))
		}
		// Link old tail to new tail
		setNext(fl.set(fl.tailPage), next)
		fl.tailPage = next
        
        // If we reused a head node, add it back as a value
		if head != 0 {
			setPtr(fl.set(fl.tailPage), 0, head)
			fl.tailSeq++
		}
	}
}

// Pop a page ID from the head 
func (fl *FreeList) PopHead() uint64 {
	ptr, head := flPop(fl)
	if head != 0 {
        // Recycle the empty head node itself
		fl.PushTail(head)
	}
	return ptr
}

func flPop(fl *FreeList) (uint64, uint64) {
	if fl.headSeq == fl.maxSeq {
		return 0, 0 
	}
	node := fl.get(fl.headPage)
	ptr := getPtr(node, seq2idx(fl.headSeq))
	fl.headSeq++

	head := uint64(0)
	if seq2idx(fl.headSeq) == 0 {
        // Head node exhausted, move to next
		head = fl.headPage
		next := getNext(node)
		fl.headPage = next
	}
	return ptr, head
}

func (fl *FreeList) SetMaxSeq() {
    fl.maxSeq = fl.tailSeq
}

func getNext(node []byte) uint64 { return binary.LittleEndian.Uint64(node[0:8]) }
func setNext(node []byte, val uint64) { binary.LittleEndian.PutUint64(node[0:8], val) }
func getPtr(node []byte, idx int) uint64 { return binary.LittleEndian.Uint64(node[FREE_LIST_HEADER+idx*8:]) }
func setPtr(node []byte, idx int, val uint64) { binary.LittleEndian.PutUint64(node[FREE_LIST_HEADER+idx*8:], val) }