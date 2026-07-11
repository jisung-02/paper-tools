package pdf

import (
	"archive/zip"
	"fmt"
	"io"
)

type officeLimits struct {
	maxZipEntryBytes   int64
	maxZipEntries      int
	maxZipTotalBytes   int64
	maxHWPInputBytes   int64
	maxHWPSectionBytes int64
	maxTextBytes       int64
}

var officeParseLimits = officeLimits{
	maxZipEntryBytes:   64 << 20,
	maxZipEntries:      1_024,
	maxZipTotalBytes:   128 << 20,
	maxHWPInputBytes:   64 << 20,
	maxHWPSectionBytes: 32 << 20,
	maxTextBytes:       64 << 20,
}

func validateOfficeZIP(zr *zip.Reader, kind string) error {
	if len(zr.File) > officeParseLimits.maxZipEntries {
		return fmt.Errorf("%s: too many zip entries", kind)
	}
	var total uint64
	for _, f := range zr.File {
		if f.UncompressedSize64 > uint64(officeParseLimits.maxZipEntryBytes) {
			return fmt.Errorf("%s: zip entry %q too large", kind, f.Name)
		}
		total += f.UncompressedSize64
		if total > uint64(officeParseLimits.maxZipTotalBytes) {
			return fmt.Errorf("%s: zip contents too large", kind)
		}
	}
	return nil
}

func limitedOfficeReader(r io.Reader, limit int64, kind, name string) (io.Reader, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%s: invalid configured limit", kind)
	}
	return &io.LimitedReader{R: r, N: limit + 1}, nil
}

func readOfficeEntry(f *zip.File, kind string) ([]byte, error) {
	if f.UncompressedSize64 > uint64(officeParseLimits.maxZipEntryBytes) {
		return nil, fmt.Errorf("%s: zip entry %q too large", kind, f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: open %q: %w", kind, f.Name, err)
	}
	defer rc.Close()
	lr := &io.LimitedReader{R: rc, N: officeParseLimits.maxZipEntryBytes + 1}
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("%s: read %q: %w", kind, f.Name, err)
	}
	if int64(len(b)) > officeParseLimits.maxZipEntryBytes {
		return nil, fmt.Errorf("%s: zip entry %q too large", kind, f.Name)
	}
	return b, nil
}
