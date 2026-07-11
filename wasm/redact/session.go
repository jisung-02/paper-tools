package main

import (
	"fmt"
	"math"

	"file-utils/pdf"
)

type redactStartInfo struct {
	Revision      uint64 `json:"revision"`
	MaxChunkBytes int    `json:"maxChunkBytes"`
}

type redactPageStartInfo struct {
	Revision      uint64 `json:"revision"`
	PageRevision  uint64 `json:"pageRevision"`
	NextOffset    uint64 `json:"nextOffset"`
	MaxChunkBytes int    `json:"maxChunkBytes"`
}

type redactOutputInfo struct {
	OutputRevision uint64 `json:"outputRevision"`
	Size           uint64 `json:"size"`
}

type redactBridgeStats struct {
	SourceCopyCalls        uint64 `json:"sourceCopyCalls"`
	SourceCopiedBytes      uint64 `json:"sourceCopiedBytes"`
	PageCopyCalls          uint64 `json:"pageCopyCalls"`
	PageCopiedBytes        uint64 `json:"pageCopiedBytes"`
	OutputCopyCalls        uint64 `json:"outputCopyCalls"`
	OutputCopiedBytes      uint64 `json:"outputCopiedBytes"`
	MaxTransientCopyBytes  uint64 `json:"maxTransientCopyBytes"`
	SourceRetainedBytes    uint64 `json:"sourceRetainedBytes"`
	PageRetainedBytes      uint64 `json:"pageRetainedBytes"`
	SpoolRetainedBytes     uint64 `json:"spoolRetainedBytes"`
	PeakSpoolRetainedBytes uint64 `json:"peakSpoolRetainedBytes"`
}

type redactPageAllocator interface {
	Allocate(size int) []byte
	Release(data []byte)
}

type redactHeapPageAllocator struct{}

func (redactHeapPageAllocator) Allocate(size int) []byte { return make([]byte, size) }
func (redactHeapPageAllocator) Release([]byte)           {}

type redactPageUpload struct {
	revision  uint64
	size      uint64
	received  uint64
	widthPt   float64
	heightPt  float64
	data      []byte
	pending   int
	allocator redactPageAllocator
}

type redactSession struct {
	revision         uint64
	spool            *pdf.BoundedChunkSpool
	encoder          *pdf.RasterPDFEncoder
	limits           pdf.RasterPDFLimits
	expectedPages    int
	pages            int
	declaredPNGBytes uint64
	page             *redactPageUpload
}

type redactOutput struct {
	revision   uint64
	spool      *pdf.BoundedChunkSpool
	checkedOut []byte
}

type redactSessionManager struct {
	active        *redactSession
	output        *redactOutput
	revision      uint64
	stats         redactBridgeStats
	pageAllocator redactPageAllocator
}

func redactLifecycleError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", pdf.ErrRasterPDFLifecycle, fmt.Sprintf(format, args...))
}

func (m *redactSessionManager) nextRevision() (uint64, error) {
	if m.revision == math.MaxUint64 {
		return 0, redactLifecycleError("revision counter exhausted")
	}
	m.revision++
	return m.revision, nil
}

func (m *redactSessionManager) allocator() redactPageAllocator {
	if m.pageAllocator == nil {
		m.pageAllocator = redactHeapPageAllocator{}
	}
	return m.pageAllocator
}

func (m *redactSessionManager) updateTransient(size int) {
	if size > 0 && uint64(size) > m.stats.MaxTransientCopyBytes {
		m.stats.MaxTransientCopyBytes = uint64(size)
	}
}

func (m *redactSessionManager) updateSpoolStats(spool *pdf.BoundedChunkSpool) {
	if spool == nil {
		m.stats.SpoolRetainedBytes = 0
		return
	}
	retained := spool.RetainedBytes()
	m.stats.SpoolRetainedBytes = retained
	if retained > m.stats.PeakSpoolRetainedBytes {
		m.stats.PeakSpoolRetainedBytes = retained
	}
}

func (m *redactSessionManager) releasePage(session *redactSession) {
	if session == nil || session.page == nil {
		return
	}
	upload := session.page
	session.page = nil
	if upload.data != nil {
		upload.allocator.Release(upload.data)
		upload.data = nil
	}
	m.stats.PageRetainedBytes = 0
}

func (m *redactSessionManager) abortActive() {
	if m.active == nil {
		return
	}
	session := m.active
	m.active = nil
	m.releasePage(session)
	session.encoder.Abort()
	session.spool.Release()
	m.updateSpoolStats(nil)
}

func (m *redactSessionManager) releaseOutput() {
	if m.output == nil {
		return
	}
	output := m.output
	m.output = nil
	output.spool.Release()
	output.checkedOut = nil
	m.updateSpoolStats(nil)
}

// Start clears every prior session and output before validating the new
// declaration. A failed restart therefore cannot leave stale encoder state.
func (m *redactSessionManager) Start(pageCount int, opts pdf.RasterPDFOpts) (redactStartInfo, error) {
	m.Abort()
	limits, err := pdf.ResolveRasterPDFLimits(opts)
	if err != nil {
		return redactStartInfo{}, err
	}
	if pageCount <= 0 {
		return redactStartInfo{}, fmt.Errorf("%w: at least one page is required", pdf.ErrInvalidRasterPage)
	}
	if pageCount > limits.MaxPages {
		return redactStartInfo{}, fmt.Errorf("%w: pages %d exceed %d", pdf.ErrRasterPDFBudget, pageCount, limits.MaxPages)
	}
	revision, err := m.nextRevision()
	if err != nil {
		return redactStartInfo{}, err
	}
	spool, err := pdf.NewBoundedChunkSpool(limits.MaxOutputBytes, pdf.PDFBridgeChunkBytes, nil)
	if err != nil {
		return redactStartInfo{}, err
	}
	encoder, err := pdf.NewRasterPDFEncoder(spool, pageCount, opts)
	if err != nil {
		spool.Release()
		return redactStartInfo{}, err
	}
	m.active = &redactSession{
		revision: revision, spool: spool, encoder: encoder, limits: limits, expectedPages: pageCount,
	}
	m.updateSpoolStats(spool)
	return redactStartInfo{Revision: revision, MaxChunkBytes: pdf.PDFBridgeChunkBytes}, nil
}

func validRedactPagePoint(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 && value <= 14400
}

func (m *redactSessionManager) activeForRevision(revision uint64) (*redactSession, error) {
	if m.active == nil {
		return nil, redactLifecycleError("no active redaction session")
	}
	if revision == 0 || revision != m.active.revision {
		return nil, redactLifecycleError("stale redaction session revision")
	}
	return m.active, nil
}

func (m *redactSessionManager) failActive(err error) error {
	m.abortActive()
	return err
}

func (m *redactSessionManager) PageStart(revision, size uint64, widthPt, heightPt float64) (redactPageStartInfo, error) {
	session, err := m.activeForRevision(revision)
	if err != nil {
		return redactPageStartInfo{}, err
	}
	if session.page != nil || session.pages >= session.expectedPages {
		return redactPageStartInfo{}, m.failActive(redactLifecycleError("page upload is out of sequence"))
	}
	if !validRedactPagePoint(widthPt) || !validRedactPagePoint(heightPt) {
		return redactPageStartInfo{}, m.failActive(fmt.Errorf("%w: invalid page geometry", pdf.ErrInvalidRasterPage))
	}
	if size == 0 || size > session.limits.MaxPagePNGBytes || size > session.limits.MaxPNGBytes ||
		session.declaredPNGBytes > session.limits.MaxPNGBytes-size {
		return redactPageStartInfo{}, m.failActive(fmt.Errorf("%w: declared PNG bytes exceed budget", pdf.ErrRasterPDFBudget))
	}
	maxInt := uint64(^uint(0) >> 1)
	if size > maxInt {
		return redactPageStartInfo{}, m.failActive(fmt.Errorf("%w: declared PNG size overflows", pdf.ErrRasterPDFBudget))
	}
	pageRevision, err := m.nextRevision()
	if err != nil {
		return redactPageStartInfo{}, m.failActive(err)
	}
	allocator := m.allocator()
	data := allocator.Allocate(int(size))
	if len(data) != int(size) || cap(data) != int(size) {
		if data != nil {
			allocator.Release(data)
		}
		return redactPageStartInfo{}, m.failActive(redactLifecycleError("page allocator returned an invalid buffer"))
	}
	session.page = &redactPageUpload{
		revision: pageRevision, size: size, widthPt: widthPt, heightPt: heightPt, data: data, allocator: allocator,
	}
	m.stats.PageRetainedBytes = size
	return redactPageStartInfo{
		Revision: session.revision, PageRevision: pageRevision, NextOffset: 0, MaxChunkBytes: pdf.PDFBridgeChunkBytes,
	}, nil
}

func (m *redactSessionManager) pageForRevision(pageRevision uint64) (*redactSession, *redactPageUpload, error) {
	if m.active == nil || m.active.page == nil {
		return nil, nil, redactLifecycleError("no active page upload")
	}
	if pageRevision == 0 || pageRevision != m.active.page.revision {
		return nil, nil, redactLifecycleError("stale page upload revision")
	}
	return m.active, m.active.page, nil
}

// PageChunkTarget exposes only the exact preallocated destination range used
// by syscall/js.CopyBytesToGo. No intermediate Go slice is allocated.
func (m *redactSessionManager) PageChunkTarget(pageRevision, offset uint64, size int) ([]byte, error) {
	_, upload, err := m.pageForRevision(pageRevision)
	if err != nil {
		return nil, err
	}
	if upload.pending != 0 || size <= 0 || size > pdf.PDFBridgeChunkBytes ||
		offset != upload.received || uint64(size) > upload.size-upload.received {
		return nil, m.failActive(redactLifecycleError("page chunk offset or size is invalid"))
	}
	start := int(offset)
	upload.pending = size
	return upload.data[start : start+size], nil
}

func (m *redactSessionManager) CompletePageChunk(pageRevision uint64, copied int) (uint64, error) {
	_, upload, err := m.pageForRevision(pageRevision)
	if err != nil {
		return 0, err
	}
	if upload.pending == 0 || copied != upload.pending {
		return 0, m.failActive(redactLifecycleError("page chunk copy is incomplete"))
	}
	upload.received += uint64(copied)
	upload.pending = 0
	m.stats.PageCopyCalls++
	m.stats.PageCopiedBytes += uint64(copied)
	m.updateTransient(copied)
	return upload.received, nil
}

func (m *redactSessionManager) PageChunk(pageRevision, offset uint64, data []byte) (uint64, error) {
	target, err := m.PageChunkTarget(pageRevision, offset, len(data))
	if err != nil {
		return 0, err
	}
	copy(target, data)
	return m.CompletePageChunk(pageRevision, len(data))
}

func (m *redactSessionManager) PageFinish(pageRevision uint64) (int, error) {
	session, upload, err := m.pageForRevision(pageRevision)
	if err != nil {
		return 0, err
	}
	if upload.pending != 0 || upload.received != upload.size {
		return 0, m.failActive(redactLifecycleError("page upload is incomplete"))
	}
	page := pdf.RasterPage{PNGData: upload.data, WidthPt: upload.widthPt, HeightPt: upload.heightPt}
	if err := session.encoder.AddPage(page); err != nil {
		return 0, m.failActive(err)
	}
	session.pages++
	session.declaredPNGBytes += upload.size
	m.releasePage(session)
	m.updateSpoolStats(session.spool)
	return session.pages, nil
}

// Add preserves the original stateful start/add compatibility path.
func (m *redactSessionManager) Add(page pdf.RasterPage) (int, error) {
	if m.active == nil {
		return 0, redactLifecycleError("no active redaction session")
	}
	session := m.active
	if session.page != nil {
		return 0, m.failActive(redactLifecycleError("cannot add during a page upload"))
	}
	if err := session.encoder.AddPage(page); err != nil {
		return 0, m.failActive(err)
	}
	session.pages++
	session.declaredPNGBytes += uint64(len(page.PNGData))
	m.updateSpoolStats(session.spool)
	return session.pages, nil
}

func (m *redactSessionManager) RecordPageBridgeCopy(size int) {
	if size <= 0 {
		return
	}
	m.stats.PageCopyCalls++
	m.stats.PageCopiedBytes += uint64(size)
	m.updateTransient(size)
}

// Finish writes and stream-validates the canonical output without creating a
// second full byte slice. The returned value contains metadata only.
func (m *redactSessionManager) Finish(revision ...uint64) (redactOutputInfo, error) {
	if m.active == nil {
		return redactOutputInfo{}, redactLifecycleError("no active redaction session")
	}
	session := m.active
	if len(revision) > 1 || (len(revision) == 1 && revision[0] != session.revision) {
		return redactOutputInfo{}, redactLifecycleError("stale redaction session revision")
	}
	if session.page != nil {
		return redactOutputInfo{}, m.failActive(redactLifecycleError("page upload is incomplete"))
	}
	if err := session.encoder.Finish(); err != nil {
		return redactOutputInfo{}, m.failActive(err)
	}
	m.updateSpoolStats(session.spool)
	validation := pdf.RasterPDFValidationLimits{
		MaxInputBytes:         session.limits.MaxOutputBytes,
		MaxPagePixels:         session.limits.MaxPagePixels,
		MaxTotalPixels:        session.limits.MaxPixels,
		MaxDecodedStreamBytes: session.limits.MaxPagePixels * 3,
	}
	if err := pdf.ValidateRasterOnlyPDFStream(session.spool.Reader(), session.pages, validation); err != nil {
		return redactOutputInfo{}, m.failActive(err)
	}
	outputRevision, err := m.nextRevision()
	if err != nil {
		return redactOutputInfo{}, m.failActive(err)
	}
	session.encoder.Abort()
	m.active = nil
	m.output = &redactOutput{revision: outputRevision, spool: session.spool}
	m.updateSpoolStats(session.spool)
	return redactOutputInfo{OutputRevision: outputRevision, Size: session.spool.Size()}, nil
}

func (m *redactSessionManager) outputForRevision(revision uint64) (*redactOutput, error) {
	if m.output == nil {
		return nil, redactLifecycleError("no redaction output")
	}
	if revision == 0 || revision != m.output.revision {
		return nil, redactLifecycleError("stale redaction output revision")
	}
	return m.output, nil
}

func (m *redactSessionManager) OutputRead(revision uint64, maxBytes int) ([]byte, bool, error) {
	output, err := m.outputForRevision(revision)
	if err != nil {
		return nil, false, err
	}
	if maxBytes != pdf.PDFBridgeChunkBytes {
		return nil, false, pdf.ErrSpoolState
	}
	if output.checkedOut != nil {
		return nil, false, pdf.ErrSpoolState
	}
	chunk, done, err := output.spool.Take()
	if err != nil {
		return nil, false, err
	}
	if done {
		m.updateSpoolStats(output.spool)
		return nil, true, nil
	}
	output.checkedOut = chunk
	m.updateSpoolStats(output.spool)
	return chunk, false, nil
}

func (m *redactSessionManager) CompleteOutputRead(revision uint64, chunk []byte, copied int) error {
	output, err := m.outputForRevision(revision)
	if err != nil {
		return err
	}
	if output.checkedOut == nil || copied != len(output.checkedOut) || copied != len(chunk) {
		m.releaseOutput()
		return redactLifecycleError("output chunk copy is incomplete")
	}
	if err := output.spool.ReleaseTaken(chunk); err != nil {
		m.releaseOutput()
		return err
	}
	output.checkedOut = nil
	m.stats.OutputCopyCalls++
	m.stats.OutputCopiedBytes += uint64(copied)
	m.updateTransient(copied)
	m.updateSpoolStats(output.spool)
	return nil
}

func (m *redactSessionManager) OutputRelease(revision uint64) error {
	if _, err := m.outputForRevision(revision); err != nil {
		return err
	}
	m.releaseOutput()
	return nil
}

func (m *redactSessionManager) BridgeStats() redactBridgeStats {
	return m.stats
}

func (m *redactSessionManager) Abort() {
	m.abortActive()
	m.releaseOutput()
	m.stats.SourceRetainedBytes = 0
	m.stats.PageRetainedBytes = 0
	m.stats.SpoolRetainedBytes = 0
}

// Build preserves the one-shot []byte API. This compatibility path is the
// only path allowed to assemble a complete in-memory output copy.
func (m *redactSessionManager) Build(pages []pdf.RasterPage, opts pdf.RasterPDFOpts) ([]byte, error) {
	start, err := m.Start(len(pages), opts)
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		if _, err := m.Add(page); err != nil {
			return nil, err
		}
	}
	info, err := m.Finish(start.Revision)
	if err != nil {
		return nil, err
	}
	if info.Size > uint64(^uint(0)>>1) {
		m.releaseOutput()
		return nil, fmt.Errorf("%w: output size overflows", pdf.ErrRasterPDFBudget)
	}
	out := make([]byte, 0, int(info.Size))
	for {
		chunk, done, err := m.OutputRead(info.OutputRevision, pdf.PDFBridgeChunkBytes)
		if err != nil {
			m.releaseOutput()
			return nil, err
		}
		if done {
			break
		}
		out = append(out, chunk...)
		if err := m.CompleteOutputRead(info.OutputRevision, chunk, len(chunk)); err != nil {
			return nil, err
		}
	}
	if err := m.OutputRelease(info.OutputRevision); err != nil {
		return nil, err
	}
	return out, nil
}
