package pdf

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	rasterValidationInputBuffer   = 64 * 1024
	rasterValidationDecodedBuffer = 32 * 1024
	rasterValidationMaxLineBytes  = 64 * 1024
	rasterValidationMaxContent    = 1024
)

type RasterPDFValidationStats struct {
	InputBytes            uint64
	DecodedBytes          uint64
	MaxInputReadBytes     uint64
	MaxDecodedBufferBytes uint64
}

type RasterPDFValidationLimits struct {
	MaxInputBytes         uint64
	MaxPagePixels         uint64
	MaxTotalPixels        uint64
	MaxDecodedStreamBytes uint64
	Forbidden             [][]byte
	Stats                 *RasterPDFValidationStats
}

func rasterValidationError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRasterPDFInvariant, fmt.Sprintf(format, args...))
}

func resolveRasterValidationLimits(limits RasterPDFValidationLimits) (RasterPDFValidationLimits, error) {
	if limits.MaxInputBytes == 0 {
		limits.MaxInputBytes = defaultRasterMaxOutputBytes
	}
	if limits.MaxInputBytes > hardRasterMaxOutputBytes {
		return limits, rasterValidationError("input limit exceeds %d", hardRasterMaxOutputBytes)
	}
	if limits.MaxPagePixels == 0 {
		limits.MaxPagePixels = hardRasterMaxPagePixels
	}
	if limits.MaxPagePixels > hardRasterMaxPagePixels {
		return limits, rasterValidationError("page pixel limit exceeds %d", hardRasterMaxPagePixels)
	}
	if limits.MaxTotalPixels == 0 {
		limits.MaxTotalPixels = hardRasterMaxPixels
	}
	if limits.MaxTotalPixels > hardRasterMaxPixels {
		return limits, rasterValidationError("total pixel limit exceeds %d", hardRasterMaxPixels)
	}
	if limits.MaxDecodedStreamBytes == 0 {
		limits.MaxDecodedStreamBytes = hardRasterMaxPagePixels * 3
	}
	if limits.MaxDecodedStreamBytes > hardRasterMaxPagePixels*3 {
		return limits, rasterValidationError("decoded stream limit exceeds %d", hardRasterMaxPagePixels*3)
	}
	var forbiddenBytes uint64
	for _, pattern := range limits.Forbidden {
		if len(pattern) == 0 || len(pattern) > rasterValidationMaxLineBytes {
			return limits, rasterValidationError("invalid forbidden byte pattern")
		}
		if forbiddenBytes > 1<<20-uint64(len(pattern)) {
			return limits, rasterValidationError("forbidden byte patterns exceed 1 MiB")
		}
		forbiddenBytes += uint64(len(pattern))
	}
	if limits.Stats != nil {
		*limits.Stats = RasterPDFValidationStats{}
	}
	return limits, nil
}

type rasterForbiddenMatcher struct {
	patterns [][]byte
	failure  [][]int
	states   []int
}

func newRasterForbiddenMatcher(patterns [][]byte) *rasterForbiddenMatcher {
	matcher := &rasterForbiddenMatcher{
		patterns: patterns,
		failure:  make([][]int, len(patterns)),
		states:   make([]int, len(patterns)),
	}
	for index, pattern := range patterns {
		failure := make([]int, len(pattern))
		for cursor, prefix := 1, 0; cursor < len(pattern); cursor++ {
			for prefix > 0 && pattern[cursor] != pattern[prefix] {
				prefix = failure[prefix-1]
			}
			if pattern[cursor] == pattern[prefix] {
				prefix++
			}
			failure[cursor] = prefix
		}
		matcher.failure[index] = failure
	}
	return matcher
}

func (m *rasterForbiddenMatcher) Feed(data []byte) bool {
	if m == nil {
		return false
	}
	for _, value := range data {
		for index, pattern := range m.patterns {
			state := m.states[index]
			for state > 0 && value != pattern[state] {
				state = m.failure[index][state-1]
			}
			if value == pattern[state] {
				state++
			}
			if state == len(pattern) {
				return true
			}
			m.states[index] = state
		}
	}
	return false
}

type rasterValidationStatsReader struct {
	reader io.Reader
	stats  *RasterPDFValidationStats
}

func (r *rasterValidationStatsReader) Read(p []byte) (int, error) {
	if r.stats != nil && uint64(len(p)) > r.stats.MaxInputReadBytes {
		r.stats.MaxInputReadBytes = uint64(len(p))
	}
	return r.reader.Read(p)
}

type rasterCanonicalReader struct {
	buffer   *bufio.Reader
	consumed uint64
	limit    uint64
	raw      *rasterForbiddenMatcher
	stats    *RasterPDFValidationStats
}

func newRasterCanonicalReader(reader io.Reader, limits RasterPDFValidationLimits) *rasterCanonicalReader {
	limited := &io.LimitedReader{R: reader, N: int64(limits.MaxInputBytes + 1)}
	tracked := &rasterValidationStatsReader{reader: limited, stats: limits.Stats}
	return &rasterCanonicalReader{
		buffer: bufio.NewReaderSize(tracked, rasterValidationInputBuffer),
		limit:  limits.MaxInputBytes,
		raw:    newRasterForbiddenMatcher(limits.Forbidden),
		stats:  limits.Stats,
	}
}

func (r *rasterCanonicalReader) accept(data []byte) error {
	length := uint64(len(data))
	if r.consumed > r.limit || length > r.limit-r.consumed {
		return rasterValidationError("input exceeds %d bytes", r.limit)
	}
	r.consumed += length
	if r.stats != nil {
		r.stats.InputBytes = r.consumed
	}
	if r.raw.Feed(data) {
		return rasterValidationError("raw output contains forbidden bytes")
	}
	return nil
}

func (r *rasterCanonicalReader) readExpected(expected []byte) error {
	var buffer [4096]byte
	for offset := 0; offset < len(expected); {
		count := len(expected) - offset
		if count > len(buffer) {
			count = len(buffer)
		}
		if _, err := io.ReadFull(r.buffer, buffer[:count]); err != nil {
			return rasterValidationError("read canonical bytes: %v", err)
		}
		if err := r.accept(buffer[:count]); err != nil {
			return err
		}
		if !bytes.Equal(buffer[:count], expected[offset:offset+count]) {
			return rasterValidationError("non-canonical bytes at offset %d", r.consumed-uint64(count))
		}
		offset += count
	}
	return nil
}

func (r *rasterCanonicalReader) readLine() ([]byte, error) {
	line, err := r.buffer.ReadSlice('\n')
	if err != nil {
		return nil, rasterValidationError("read canonical line: %v", err)
	}
	if len(line) > rasterValidationMaxLineBytes {
		return nil, rasterValidationError("canonical line exceeds %d bytes", rasterValidationMaxLineBytes)
	}
	if err := r.accept(line); err != nil {
		return nil, err
	}
	return append([]byte(nil), line...), nil
}

func (r *rasterCanonicalReader) readBytes(length int) ([]byte, error) {
	if length < 0 || length > rasterValidationMaxContent {
		return nil, rasterValidationError("content length %d is invalid", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r.buffer, data); err != nil {
		return nil, rasterValidationError("read content bytes: %v", err)
	}
	if err := r.accept(data); err != nil {
		return nil, err
	}
	return data, nil
}

func (r *rasterCanonicalReader) readSome(p []byte) (int, error) {
	if len(p) > rasterValidationDecodedBuffer {
		p = p[:rasterValidationDecodedBuffer]
	}
	n, err := r.buffer.Read(p)
	if n > 0 {
		if acceptErr := r.accept(p[:n]); acceptErr != nil {
			return n, acceptErr
		}
	}
	return n, err
}

func (r *rasterCanonicalReader) readByte() (byte, error) {
	value, err := r.buffer.ReadByte()
	if err != nil {
		return 0, err
	}
	if err := r.accept([]byte{value}); err != nil {
		return 0, err
	}
	return value, nil
}

type rasterCanonicalLimitedReader struct {
	reader    *rasterCanonicalReader
	remaining uint64
}

func (r *rasterCanonicalLimitedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if uint64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.reader.readSome(p)
	r.remaining -= uint64(n)
	return n, err
}

func (r *rasterCanonicalLimitedReader) ReadByte() (byte, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	value, err := r.reader.readByte()
	if err == nil {
		r.remaining--
	}
	return value, err
}

func validateRasterImageStream(reader *rasterCanonicalReader, length, width, height int, limits RasterPDFValidationLimits) error {
	compressed := &rasterCanonicalLimitedReader{reader: reader, remaining: uint64(length)}
	decoded, err := zlib.NewReader(compressed)
	if err != nil {
		return rasterValidationError("image Flate stream: %v", err)
	}
	defer decoded.Close()
	expected, ok := checkedRasterProduct(uint64(width)*uint64(height), 3)
	if !ok || expected > limits.MaxDecodedStreamBytes {
		return rasterValidationError("decoded image exceeds %d bytes", limits.MaxDecodedStreamBytes)
	}
	bufferSize := rasterValidationDecodedBuffer
	if expected < uint64(bufferSize) {
		bufferSize = int(expected)
	}
	buffer := make([]byte, bufferSize)
	if limits.Stats != nil && uint64(bufferSize) > limits.Stats.MaxDecodedBufferBytes {
		limits.Stats.MaxDecodedBufferBytes = uint64(bufferSize)
	}
	forbidden := newRasterForbiddenMatcher(limits.Forbidden)
	var total uint64
	for {
		n, readErr := decoded.Read(buffer)
		if n > 0 {
			if total > expected-uint64(n) {
				return rasterValidationError("decoded image is larger than expected")
			}
			total += uint64(n)
			if limits.Stats != nil {
				limits.Stats.DecodedBytes += uint64(n)
			}
			if forbidden.Feed(buffer[:n]) {
				return rasterValidationError("decoded image contains forbidden bytes")
			}
		}
		if errorsIsEOF(readErr) {
			break
		}
		if readErr != nil {
			return rasterValidationError("decode image stream: %v", readErr)
		}
	}
	if err := decoded.Close(); err != nil {
		return rasterValidationError("close image stream: %v", err)
	}
	if compressed.remaining != 0 {
		return rasterValidationError("image stream has %d trailing bytes", compressed.remaining)
	}
	if total != expected {
		return rasterValidationError("decoded image bytes %d, want %d", total, expected)
	}
	return nil
}

func errorsIsEOF(err error) bool { return err == io.EOF }

func parseRasterImageDictionary(line []byte) (width, height, length int, err error) {
	const format = "<< /BitsPerComponent %d /ColorSpace /DeviceRGB /Filter /FlateDecode /Height %d /Length %d /Subtype /Image /Type /XObject /Width %d >>\n"
	var bits int
	count, scanErr := fmt.Sscanf(string(line), format, &bits, &height, &length, &width)
	if scanErr != nil || count != 4 || bits != 8 || width <= 0 || height <= 0 || length <= 0 {
		return 0, 0, 0, rasterValidationError("image dictionary is invalid")
	}
	want := fmt.Sprintf(format, bits, height, length, width)
	if string(line) != want {
		return 0, 0, 0, rasterValidationError("image dictionary is not canonical")
	}
	return width, height, length, nil
}

func parseRasterContent(data []byte) (widthToken, heightToken string, width, height float64, err error) {
	fields := strings.Fields(string(data))
	if len(fields) != 11 || fields[0] != "q" || fields[2] != "0" || fields[3] != "0" ||
		fields[5] != "0" || fields[6] != "0" || fields[7] != "cm" || fields[8] != "/Im0" ||
		fields[9] != "Do" || fields[10] != "Q" {
		return "", "", 0, 0, rasterValidationError("content operators are invalid")
	}
	width, err = strconv.ParseFloat(fields[1], 64)
	if err != nil || math.IsNaN(width) || math.IsInf(width, 0) || width <= 0 || width > maxRasterPagePoints || formatPDFNumber(width) != fields[1] {
		return "", "", 0, 0, rasterValidationError("content width is invalid")
	}
	height, err = strconv.ParseFloat(fields[4], 64)
	if err != nil || math.IsNaN(height) || math.IsInf(height, 0) || height <= 0 || height > maxRasterPagePoints || formatPDFNumber(height) != fields[4] {
		return "", "", 0, 0, rasterValidationError("content height is invalid")
	}
	want := "q " + fields[1] + " 0 0 " + fields[4] + " 0 0 cm /Im0 Do Q"
	if string(data) != want {
		return "", "", 0, 0, rasterValidationError("content stream is not canonical")
	}
	return fields[1], fields[4], width, height, nil
}

func ValidateRasterOnlyPDFStream(input io.Reader, expectedPages int, requested RasterPDFValidationLimits) error {
	if input == nil || expectedPages <= 0 || expectedPages > hardRasterMaxPages {
		return rasterValidationError("invalid validation input")
	}
	limits, err := resolveRasterValidationLimits(requested)
	if err != nil {
		return err
	}
	reader := newRasterCanonicalReader(input, limits)
	if err := reader.readExpected([]byte(rasterPDFHeader)); err != nil {
		return err
	}
	objectCount := expectedPages*3 + 2
	size := objectCount + 1
	offsets := make([]uint64, size)
	var totalPixels uint64
	for pageIndex := 0; pageIndex < expectedPages; pageIndex++ {
		imageObject := 3 + pageIndex*3
		contentObject := imageObject + 1
		pageObject := imageObject + 2

		offsets[imageObject] = reader.consumed
		if err := reader.readExpected([]byte(fmt.Sprintf("%d 0 obj\n", imageObject))); err != nil {
			return err
		}
		imageLine, err := reader.readLine()
		if err != nil {
			return err
		}
		width, height, length, err := parseRasterImageDictionary(imageLine)
		if err != nil {
			return err
		}
		pixels, ok := checkedRasterProduct(uint64(width), uint64(height))
		if !ok || pixels > limits.MaxPagePixels || pixels > limits.MaxTotalPixels || totalPixels > limits.MaxTotalPixels-pixels {
			return rasterValidationError("image pixel budget is invalid")
		}
		totalPixels += pixels
		if uint64(length) > limits.MaxInputBytes {
			return rasterValidationError("image stream length is invalid")
		}
		if err := reader.readExpected([]byte("stream\n")); err != nil {
			return err
		}
		if err := validateRasterImageStream(reader, length, width, height, limits); err != nil {
			return err
		}
		if err := reader.readExpected([]byte("\nendstream\nendobj\n")); err != nil {
			return err
		}

		offsets[contentObject] = reader.consumed
		if err := reader.readExpected([]byte(fmt.Sprintf("%d 0 obj\n", contentObject))); err != nil {
			return err
		}
		contentLine, err := reader.readLine()
		if err != nil {
			return err
		}
		var contentLength int
		if count, scanErr := fmt.Sscanf(string(contentLine), "<< /Length %d >>\n", &contentLength); scanErr != nil || count != 1 || string(contentLine) != fmt.Sprintf("<< /Length %d >>\n", contentLength) {
			return rasterValidationError("content dictionary is invalid")
		}
		if err := reader.readExpected([]byte("stream\n")); err != nil {
			return err
		}
		content, err := reader.readBytes(contentLength)
		if err != nil {
			return err
		}
		widthToken, heightToken, _, _, err := parseRasterContent(content)
		if err != nil {
			return err
		}
		if err := reader.readExpected([]byte("\nendstream\nendobj\n")); err != nil {
			return err
		}

		offsets[pageObject] = reader.consumed
		if err := reader.readExpected([]byte(fmt.Sprintf("%d 0 obj\n", pageObject))); err != nil {
			return err
		}
		pageLine, err := reader.readLine()
		if err != nil {
			return err
		}
		wantPage := fmt.Sprintf("<< /Contents %d 0 R /MediaBox [0 0 %s %s] /Parent 2 0 R /Resources << /XObject << /Im0 %d 0 R >> >> /Type /Page >>\n", contentObject, widthToken, heightToken, imageObject)
		if string(pageLine) != wantPage {
			return rasterValidationError("page %d dictionary is not canonical", pageIndex+1)
		}
		if err := reader.readExpected([]byte("endobj\n")); err != nil {
			return err
		}
	}

	offsets[2] = reader.consumed
	if err := reader.readExpected([]byte("2 0 obj\n")); err != nil {
		return err
	}
	pagesLine, err := reader.readLine()
	if err != nil {
		return err
	}
	var pages strings.Builder
	fmt.Fprintf(&pages, "<< /Count %d /Kids [", expectedPages)
	for index := 0; index < expectedPages; index++ {
		if index > 0 {
			pages.WriteByte(' ')
		}
		fmt.Fprintf(&pages, "%d 0 R", 5+index*3)
	}
	pages.WriteString("] /Type /Pages >>\n")
	if string(pagesLine) != pages.String() {
		return rasterValidationError("Pages dictionary is not canonical")
	}
	if err := reader.readExpected([]byte("endobj\n")); err != nil {
		return err
	}

	offsets[1] = reader.consumed
	if err := reader.readExpected([]byte("1 0 obj\n<< /Pages 2 0 R /Type /Catalog >>\nendobj\n")); err != nil {
		return err
	}
	xrefOffset := reader.consumed
	if err := reader.readExpected([]byte(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", size))); err != nil {
		return err
	}
	for object := 1; object <= objectCount; object++ {
		if offsets[object] == 0 || offsets[object] > 9_999_999_999 {
			return rasterValidationError("object %d offset is invalid", object)
		}
		if err := reader.readExpected([]byte(fmt.Sprintf("%010d 00000 n \n", offsets[object]))); err != nil {
			return err
		}
	}
	if err := reader.readExpected([]byte(fmt.Sprintf("trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", size, xrefOffset))); err != nil {
		return err
	}
	value, err := reader.buffer.ReadByte()
	if err == nil {
		if acceptErr := reader.accept([]byte{value}); acceptErr != nil {
			return acceptErr
		}
		return rasterValidationError("extra bytes follow EOF")
	}
	if err != io.EOF {
		return rasterValidationError("read final EOF: %v", err)
	}
	return nil
}
