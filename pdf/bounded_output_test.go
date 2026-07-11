package pdf

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type trackingChunkAllocator struct {
	allocations int
	releases    int
	retained    uint64
	peak        uint64
}

func (a *trackingChunkAllocator) Allocate(size int) []byte {
	a.allocations++
	a.retained += uint64(size)
	if a.retained > a.peak {
		a.peak = a.retained
	}
	return make([]byte, size)
}

func (a *trackingChunkAllocator) Release(chunk []byte) {
	a.releases++
	a.retained -= uint64(cap(chunk))
}

func TestBoundedChunkSpoolRejectsCapPlusOneBeforeAllocation(t *testing.T) {
	allocator := &trackingChunkAllocator{}
	spool, err := NewBoundedChunkSpool(3, 2, allocator)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := spool.Write([]byte{1, 2, 3}); err != nil || n != 3 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	before := allocator.allocations
	if n, err := spool.Write([]byte{4}); n != 0 || !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("cap+1 Write = %d, %v", n, err)
	}
	if allocator.allocations != before {
		t.Fatalf("cap+1 allocated %d chunks", allocator.allocations-before)
	}
	if n, err := spool.Write(nil); n != 0 || !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("sticky Write = %d, %v", n, err)
	}
	if spool.Size() != 3 || spool.Remaining() != 3 {
		t.Fatalf("size/remaining = %d/%d", spool.Size(), spool.Remaining())
	}
	spool.Release()
	if allocator.retained != 0 {
		t.Fatalf("retained after Release = %d", allocator.retained)
	}
}

func TestBoundedChunkSpoolReaderThenTakeOwnership(t *testing.T) {
	allocator := &trackingChunkAllocator{}
	spool, err := NewBoundedChunkSpool(5, 2, allocator)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 2, 3, 4, 5}
	if _, err := spool.Write(want); err != nil {
		t.Fatal(err)
	}
	allocations := allocator.allocations
	retained := allocator.retained

	reader := spool.Reader()
	first := make([]byte, 1)
	if n, err := reader.Read(first); err != nil || n != 1 || first[0] != 1 {
		t.Fatalf("first Read = %d, %v, %v", n, err, first)
	}
	if _, _, err := spool.Take(); !errors.Is(err, ErrSpoolState) {
		t.Fatalf("Take before Reader EOF = %v", err)
	}
	got := append([]byte(nil), first...)
	buffer := make([]byte, 2)
	for {
		n, err := reader.Read(buffer)
		got = append(got, buffer[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Reader = %v, want %v", got, want)
	}
	if allocator.allocations != allocations || allocator.retained != retained || spool.Remaining() != uint64(len(want)) {
		t.Fatalf("Reader mutated allocator/spool: allocations=%d retained=%d remaining=%d", allocator.allocations, allocator.retained, spool.Remaining())
	}
	if _, err := spool.Write([]byte{6}); !errors.Is(err, ErrSpoolState) {
		t.Fatalf("Write after Reader = %v", err)
	}

	chunk, done, err := spool.Take()
	if err != nil || done || !bytes.Equal(chunk, []byte{1, 2}) {
		t.Fatalf("first Take = %v, %v, %v", chunk, done, err)
	}
	if spool.Remaining() != 3 || allocator.retained != retained {
		t.Fatalf("Take remaining/retained = %d/%d", spool.Remaining(), allocator.retained)
	}
	if _, _, err := spool.Take(); !errors.Is(err, ErrSpoolState) {
		t.Fatalf("Take with checked-out chunk = %v", err)
	}
	wrong := append([]byte(nil), chunk...)
	if err := spool.ReleaseTaken(wrong); !errors.Is(err, ErrSpoolState) {
		t.Fatalf("ReleaseTaken wrong chunk = %v", err)
	}
	if err := spool.ReleaseTaken(chunk); err != nil {
		t.Fatal(err)
	}
	if allocator.retained != 3 {
		t.Fatalf("retained after first release = %d", allocator.retained)
	}
	if err := spool.ReleaseTaken(chunk); !errors.Is(err, ErrSpoolState) {
		t.Fatalf("duplicate ReleaseTaken = %v", err)
	}
	stateReader := spool.Reader()
	if n, err := stateReader.Read(make([]byte, 1)); n != 0 || !errors.Is(err, ErrSpoolState) {
		t.Fatalf("Reader after drain = %d, %v", n, err)
	}

	for {
		chunk, done, err = spool.Take()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if len(chunk) > 2 {
			t.Fatalf("chunk bytes = %d", len(chunk))
		}
		if err := spool.ReleaseTaken(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if spool.Remaining() != 0 || allocator.retained != 0 {
		t.Fatalf("drained remaining/retained = %d/%d", spool.Remaining(), allocator.retained)
	}
	spool.Release()
	spool.Release()
	if allocator.retained != 0 {
		t.Fatalf("repeated Release retained = %d", allocator.retained)
	}
}

func TestBoundedChunkSpoolReleaseDropsUnreadAndCheckedOutChunks(t *testing.T) {
	allocator := &trackingChunkAllocator{}
	spool, err := NewBoundedChunkSpool(8, 3, allocator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.Write([]byte{1, 2, 3, 4, 5, 6, 7}); err != nil {
		t.Fatal(err)
	}
	chunk, done, err := spool.Take()
	if err != nil || done || len(chunk) != 3 {
		t.Fatalf("Take = %d, %v, %v", len(chunk), done, err)
	}
	spool.Release()
	if allocator.retained != 0 || spool.Remaining() != 0 {
		t.Fatalf("Release retained/remaining = %d/%d", allocator.retained, spool.Remaining())
	}
	if err := spool.ReleaseTaken(chunk); !errors.Is(err, ErrSpoolState) {
		t.Fatalf("ReleaseTaken after Release = %v", err)
	}
	if _, _, err := spool.Take(); !errors.Is(err, ErrSpoolState) {
		t.Fatalf("Take after Release = %v", err)
	}
}

func TestBoundedChunkSpoolTracksRetainedChunkCapacity(t *testing.T) {
	allocator := &trackingChunkAllocator{}
	spool, err := NewBoundedChunkSpool(8, 3, allocator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if spool.Remaining() != 1 || spool.RetainedBytes() != 3 || spool.RetainedBytes() != allocator.retained {
		t.Fatalf("partial chunk remaining/retained/allocator = %d/%d/%d", spool.Remaining(), spool.RetainedBytes(), allocator.retained)
	}
	chunk, done, err := spool.Take()
	if err != nil || done {
		t.Fatalf("Take = %v, %v", done, err)
	}
	if spool.Remaining() != 0 || spool.RetainedBytes() != 3 {
		t.Fatalf("checked-out remaining/retained = %d/%d", spool.Remaining(), spool.RetainedBytes())
	}
	if err := spool.ReleaseTaken(chunk); err != nil {
		t.Fatal(err)
	}
	if spool.RetainedBytes() != 0 || allocator.retained != 0 {
		t.Fatalf("released retained/allocator = %d/%d", spool.RetainedBytes(), allocator.retained)
	}
}
