package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"file-utils/pdf"
)

func sessionPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func drainRedactOutput(t *testing.T, manager *redactSessionManager, info redactOutputInfo) []byte {
	t.Helper()
	var out []byte
	for {
		chunk, done, err := manager.OutputRead(info.OutputRevision, pdf.PDFBridgeChunkBytes)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if len(chunk) == 0 || len(chunk) > pdf.PDFBridgeChunkBytes {
			t.Fatalf("output chunk bytes = %d", len(chunk))
		}
		out = append(out, chunk...)
		if err := manager.CompleteOutputRead(info.OutputRevision, chunk, len(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.OutputRelease(info.OutputRevision); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRedactSessionChunkLifecycleAndStats(t *testing.T) {
	pngData := sessionPNG(t)
	manager := &redactSessionManager{}
	start, err := manager.Start(1, pdf.RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if start.Revision == 0 || start.MaxChunkBytes != pdf.PDFBridgeChunkBytes {
		t.Fatalf("Start = %+v", start)
	}
	pageStart, err := manager.PageStart(start.Revision, uint64(len(pngData)), 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if pageStart.PageRevision == 0 || pageStart.NextOffset != 0 || pageStart.MaxChunkBytes != pdf.PDFBridgeChunkBytes {
		t.Fatalf("PageStart = %+v", pageStart)
	}

	split := len(pngData) / 2
	if received, err := manager.PageChunk(pageStart.PageRevision, 0, pngData[:split]); err != nil || received != uint64(split) {
		t.Fatalf("first PageChunk = %d, %v", received, err)
	}
	if received, err := manager.PageChunk(pageStart.PageRevision, uint64(split), pngData[split:]); err != nil || received != uint64(len(pngData)) {
		t.Fatalf("second PageChunk = %d, %v", received, err)
	}
	if pages, err := manager.PageFinish(pageStart.PageRevision); err != nil || pages != 1 {
		t.Fatalf("PageFinish = %d, %v", pages, err)
	}
	stats := manager.BridgeStats()
	if stats.PageCopyCalls != 2 || stats.PageCopiedBytes != uint64(len(pngData)) || stats.PageRetainedBytes != 0 {
		t.Fatalf("page stats = %+v", stats)
	}
	if stats.SpoolRetainedBytes != pdf.PDFBridgeChunkBytes || stats.PeakSpoolRetainedBytes != pdf.PDFBridgeChunkBytes {
		t.Fatalf("partial spool capacity stats = %+v", stats)
	}

	info, err := manager.Finish(start.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if info.OutputRevision == 0 || info.Size == 0 || manager.active != nil || manager.output == nil {
		t.Fatalf("Finish = %+v, active=%v output=%v", info, manager.active, manager.output)
	}
	if _, _, err := manager.OutputRead(info.OutputRevision, pdf.PDFBridgeChunkBytes-1); !errors.Is(err, pdf.ErrSpoolState) {
		t.Fatalf("non-exact OutputRead max = %v", err)
	}
	out := drainRedactOutput(t, manager, info)
	if uint64(len(out)) != info.Size {
		t.Fatalf("output bytes = %d, want %d", len(out), info.Size)
	}
	if err := pdf.ValidateRasterOnlyPDF(out, 1); err != nil {
		t.Fatal(err)
	}
	stats = manager.BridgeStats()
	if stats.OutputCopyCalls == 0 || stats.OutputCopiedBytes != uint64(len(out)) || stats.SpoolRetainedBytes != 0 || stats.PageRetainedBytes != 0 {
		t.Fatalf("terminal stats = %+v", stats)
	}
	if stats.MaxTransientCopyBytes > pdf.PDFBridgeChunkBytes || stats.PeakSpoolRetainedBytes < uint64(len(out)) {
		t.Fatalf("peak stats = %+v", stats)
	}
}

type redactTrackingPageAllocator struct {
	allocations int
	releases    int
	retained    uint64
}

func (a *redactTrackingPageAllocator) Allocate(size int) []byte {
	a.allocations++
	a.retained += uint64(size)
	return make([]byte, size)
}

func (a *redactTrackingPageAllocator) Release(data []byte) {
	a.releases++
	a.retained -= uint64(cap(data))
}

func TestRedactSessionRejectsPageCapBeforeAllocation(t *testing.T) {
	allocator := &redactTrackingPageAllocator{}
	manager := &redactSessionManager{pageAllocator: allocator}
	start, err := manager.Start(1, pdf.RasterPDFOpts{MaxPagePNGBytes: 8, MaxPNGBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PageStart(start.Revision, 9, 100, 100); !errors.Is(err, pdf.ErrRasterPDFBudget) {
		t.Fatalf("PageStart cap+1 = %v", err)
	}
	if allocator.allocations != 0 || allocator.retained != 0 || manager.active != nil {
		t.Fatalf("cap+1 state allocations=%d retained=%d active=%v", allocator.allocations, allocator.retained, manager.active)
	}
}

func TestRedactSessionRevisionsOffsetsAndCleanup(t *testing.T) {
	pngData := sessionPNG(t)
	manager := &redactSessionManager{}
	first, err := manager.Start(1, pdf.RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := manager.PageStart(first.Revision, uint64(len(pngData)), 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PageChunk(page.PageRevision-1, 0, pngData[:1]); !errors.Is(err, pdf.ErrRasterPDFLifecycle) {
		t.Fatalf("stale page revision = %v", err)
	}
	if manager.active == nil {
		t.Fatal("stale revision destroyed the current session")
	}
	if _, err := manager.PageChunk(page.PageRevision, 1, pngData[:1]); !errors.Is(err, pdf.ErrRasterPDFLifecycle) {
		t.Fatalf("offset gap = %v", err)
	}
	if manager.active != nil || manager.BridgeStats().PageRetainedBytes != 0 {
		t.Fatalf("fatal sequence retained state: active=%v stats=%+v", manager.active, manager.BridgeStats())
	}

	second, err := manager.Start(1, pdf.RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision <= first.Revision {
		t.Fatalf("revisions = %d then %d", first.Revision, second.Revision)
	}
	if _, err := manager.Start(0, pdf.RasterPDFOpts{}); err == nil {
		t.Fatal("invalid restart succeeded")
	}
	if manager.active != nil || manager.output != nil {
		t.Fatal("invalid restart retained stale state")
	}
	manager.Abort()
	manager.Abort()
}

func TestRedactSessionDirectCopyTargetAndLegacyWrapper(t *testing.T) {
	pngData := sessionPNG(t)
	page := pdf.RasterPage{PNGData: pngData, WidthPt: 100, HeightPt: 200}
	manager := &redactSessionManager{}
	start, err := manager.Start(1, pdf.RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := manager.PageStart(start.Revision, uint64(len(pngData)), page.WidthPt, page.HeightPt)
	if err != nil {
		t.Fatal(err)
	}
	target, err := manager.PageChunkTarget(upload.PageRevision, 0, len(pngData))
	if err != nil {
		t.Fatal(err)
	}
	copy(target, pngData)
	if received, err := manager.CompletePageChunk(upload.PageRevision, len(target)); err != nil || received != uint64(len(pngData)) {
		t.Fatalf("CompletePageChunk = %d, %v", received, err)
	}
	if _, err := manager.PageFinish(upload.PageRevision); err != nil {
		t.Fatal(err)
	}
	info, err := manager.Finish(start.Revision)
	if err != nil {
		t.Fatal(err)
	}
	chunked := drainRedactOutput(t, manager, info)

	legacy, err := manager.Build([]pdf.RasterPage{page}, pdf.RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(chunked, legacy) {
		t.Fatal("legacy wrapper used a different encoder path")
	}
	if manager.active != nil || manager.output != nil || manager.BridgeStats().SpoolRetainedBytes != 0 {
		t.Fatalf("legacy wrapper retained state: %+v", manager.BridgeStats())
	}
}

func TestRedactSessionOutputRevisionAndRelease(t *testing.T) {
	manager := &redactSessionManager{}
	page := pdf.RasterPage{PNGData: sessionPNG(t), WidthPt: 100, HeightPt: 100}
	start, err := manager.Start(1, pdf.RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(page); err != nil {
		t.Fatal(err)
	}
	info, err := manager.Finish(start.Revision)
	if err != nil {
		t.Fatal(err)
	}
	chunk, done, err := manager.OutputRead(info.OutputRevision, pdf.PDFBridgeChunkBytes)
	if err != nil || done {
		t.Fatalf("OutputRead = %d, %v, %v", len(chunk), done, err)
	}
	if err := manager.CompleteOutputRead(info.OutputRevision+1, chunk, len(chunk)); !errors.Is(err, pdf.ErrRasterPDFLifecycle) {
		t.Fatalf("stale CompleteOutputRead = %v", err)
	}
	if manager.output == nil {
		t.Fatal("stale output revision destroyed current output")
	}
	if err := manager.OutputRelease(info.OutputRevision); err != nil {
		t.Fatal(err)
	}
	if manager.output != nil || manager.BridgeStats().SpoolRetainedBytes != 0 {
		t.Fatalf("OutputRelease retained state: %+v", manager.BridgeStats())
	}
	if err := manager.CompleteOutputRead(info.OutputRevision, chunk, len(chunk)); !errors.Is(err, pdf.ErrRasterPDFLifecycle) {
		t.Fatalf("completion after release = %v", err)
	}
}
