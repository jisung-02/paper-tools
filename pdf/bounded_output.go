package pdf

import (
	"errors"
	"io"
	"math"
)

const PDFBridgeChunkBytes = 1 << 20

var (
	ErrOutputLimit = errors.New("pdf output limit exceeded")
	ErrSpoolState  = errors.New("invalid bounded spool state")
)

type ChunkAllocator interface {
	Allocate(size int) []byte
	Release(chunk []byte)
}

type HeapChunkAllocator struct{}

func (HeapChunkAllocator) Allocate(size int) []byte { return make([]byte, size) }
func (HeapChunkAllocator) Release([]byte)           {}

type BoundedChunkSpool struct {
	limit          uint64
	size           uint64
	remaining      uint64
	retained       uint64
	chunkBytes     int
	chunks         [][]byte
	used           []int
	head           int
	checkedOut     []byte
	allocator      ChunkAllocator
	stickyError    error
	sealed         bool
	readerActive   bool
	readerComplete bool
	drainStarted   bool
	released       bool
}

func NewBoundedChunkSpool(limit uint64, chunkBytes int, allocator ChunkAllocator) (*BoundedChunkSpool, error) {
	if limit == 0 || chunkBytes <= 0 {
		return nil, ErrSpoolState
	}
	if allocator == nil {
		allocator = HeapChunkAllocator{}
	}
	return &BoundedChunkSpool{limit: limit, chunkBytes: chunkBytes, allocator: allocator}, nil
}

func (s *BoundedChunkSpool) Write(p []byte) (int, error) {
	if s == nil {
		return 0, ErrSpoolState
	}
	if s.stickyError != nil {
		return 0, s.stickyError
	}
	if s.released || s.sealed {
		return 0, ErrSpoolState
	}
	length := uint64(len(p))
	if s.size > s.limit || length > s.limit-s.size {
		s.stickyError = ErrOutputLimit
		return 0, s.stickyError
	}
	written := 0
	for written < len(p) {
		if len(s.chunks) > 0 {
			last := len(s.chunks) - 1
			if available := len(s.chunks[last]) - s.used[last]; available > 0 {
				count := len(p) - written
				if count > available {
					count = available
				}
				copy(s.chunks[last][s.used[last]:], p[written:written+count])
				s.used[last] += count
				written += count
				s.size += uint64(count)
				s.remaining += uint64(count)
				continue
			}
		}
		availableLimit := s.limit - s.size
		allocate := uint64(s.chunkBytes)
		if allocate > availableLimit {
			allocate = availableLimit
		}
		if allocate == 0 || allocate > uint64(math.MaxInt) {
			s.stickyError = ErrSpoolState
			return written, s.stickyError
		}
		chunk := s.allocator.Allocate(int(allocate))
		if len(chunk) != int(allocate) || cap(chunk) != int(allocate) {
			if chunk != nil {
				s.allocator.Release(chunk)
			}
			s.stickyError = ErrSpoolState
			return written, s.stickyError
		}
		s.chunks = append(s.chunks, chunk)
		s.used = append(s.used, 0)
		s.retained += uint64(cap(chunk))
	}
	return written, nil
}

type boundedChunkReader struct {
	spool  *BoundedChunkSpool
	chunk  int
	offset int
	err    error
	done   bool
}

func (s *BoundedChunkSpool) Reader() io.Reader {
	reader := &boundedChunkReader{spool: s}
	if s == nil || s.released || s.drainStarted || s.readerActive || s.readerComplete {
		reader.err = ErrSpoolState
		return reader
	}
	if s.stickyError != nil {
		reader.err = s.stickyError
		return reader
	}
	s.sealed = true
	s.readerActive = true
	return reader
}

func (r *boundedChunkReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.done {
		return 0, io.EOF
	}
	if r.spool == nil || r.spool.released || r.spool.drainStarted {
		r.err = ErrSpoolState
		return 0, r.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	written := 0
	for written < len(p) && r.chunk < len(r.spool.chunks) {
		used := r.spool.used[r.chunk]
		if r.offset >= used {
			r.chunk++
			r.offset = 0
			continue
		}
		count := used - r.offset
		if count > len(p)-written {
			count = len(p) - written
		}
		copy(p[written:written+count], r.spool.chunks[r.chunk][r.offset:r.offset+count])
		written += count
		r.offset += count
	}
	if written > 0 {
		return written, nil
	}
	r.done = true
	r.spool.readerActive = false
	r.spool.readerComplete = true
	return 0, io.EOF
}

func (s *BoundedChunkSpool) Take() (chunk []byte, done bool, err error) {
	if s == nil || s.released {
		return nil, false, ErrSpoolState
	}
	if s.stickyError != nil {
		return nil, false, s.stickyError
	}
	if s.checkedOut != nil || s.readerActive || (s.sealed && !s.readerComplete) {
		return nil, false, ErrSpoolState
	}
	s.drainStarted = true
	for s.head < len(s.chunks) && s.chunks[s.head] == nil {
		s.head++
	}
	if s.head >= len(s.chunks) {
		return nil, true, nil
	}
	allocated := s.chunks[s.head]
	used := s.used[s.head]
	if used <= 0 || used > len(allocated) {
		return nil, false, ErrSpoolState
	}
	chunk = allocated[:used]
	s.chunks[s.head] = nil
	s.used[s.head] = 0
	s.head++
	s.remaining -= uint64(used)
	s.checkedOut = chunk
	return chunk, false, nil
}

func (s *BoundedChunkSpool) ReleaseTaken(chunk []byte) error {
	if s == nil || s.released || s.checkedOut == nil || len(chunk) == 0 || len(chunk) != len(s.checkedOut) || cap(chunk) != cap(s.checkedOut) || &chunk[0] != &s.checkedOut[0] {
		return ErrSpoolState
	}
	s.allocator.Release(s.checkedOut)
	s.retained -= uint64(cap(s.checkedOut))
	s.checkedOut = nil
	return nil
}

func (s *BoundedChunkSpool) Size() uint64 {
	if s == nil {
		return 0
	}
	return s.size
}

func (s *BoundedChunkSpool) Remaining() uint64 {
	if s == nil {
		return 0
	}
	return s.remaining
}

// RetainedBytes reports the allocator-backed capacity still owned by the
// spool, including a checked-out chunk awaiting ReleaseTaken.
func (s *BoundedChunkSpool) RetainedBytes() uint64 {
	if s == nil {
		return 0
	}
	return s.retained
}

func (s *BoundedChunkSpool) Release() {
	if s == nil || s.released {
		return
	}
	if s.checkedOut != nil {
		s.allocator.Release(s.checkedOut)
		s.retained -= uint64(cap(s.checkedOut))
		s.checkedOut = nil
	}
	for index, chunk := range s.chunks {
		if chunk != nil {
			s.allocator.Release(chunk)
			s.retained -= uint64(cap(chunk))
			s.chunks[index] = nil
		}
	}
	s.chunks = nil
	s.used = nil
	s.remaining = 0
	s.retained = 0
	s.readerActive = false
	s.sealed = true
	s.released = true
}
