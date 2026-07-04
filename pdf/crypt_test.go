package pdf

import (
	"bytes"
	"errors"
	"testing"
)

func TestProtectRoundTripAES(t *testing.T) {
	p, err := Protect(classicPDF(), "user1", "owner1")
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	if _, err := Parse(p); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("Parse on protected file: got %v, want ErrEncrypted", err)
	}

	for _, pw := range []string{"user1", "owner1"} {
		d, err := ParseWithPassword(p, pw)
		if err != nil {
			t.Fatalf("ParseWithPassword(%q): %v", pw, err)
		}
		pages, err := d.Pages()
		if err != nil {
			t.Fatalf("Pages: %v", err)
		}
		if len(pages) != 2 {
			t.Fatalf("expected 2 pages, got %d", len(pages))
		}
		pd, ok := d.Get(pages[1].Num).(Dict)
		if !ok {
			t.Fatalf("page 2 is not a dict")
		}
		if rot, _ := pd["Rotate"].(int); rot != 90 {
			t.Errorf("page 2 Rotate = %v, want 90", pd["Rotate"])
		}
	}

	if _, err := ParseWithPassword(p, "nope"); err == nil || !bytes.Contains([]byte(err.Error()), []byte("wrong password")) {
		t.Fatalf("expected wrong password error, got %v", err)
	}
}

func TestProtectRoundTripRC4(t *testing.T) {
	p, err := protectWith(classicPDF(), "user1", "owner1", true)
	if err != nil {
		t.Fatalf("protectWith: %v", err)
	}
	if !bytes.Contains(p, []byte("/V 2")) {
		t.Errorf("expected /V 2 in RC4-protected output")
	}
	d, err := ParseWithPassword(p, "user1")
	if err != nil {
		t.Fatalf("ParseWithPassword: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
}

func TestUnlock(t *testing.T) {
	p, err := Protect(classicPDF(), "pw", "")
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	out, err := Unlock(p, "pw")
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse unlocked: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	pd, ok := d.Get(pages[1].Num).(Dict)
	if !ok {
		t.Fatalf("page 2 is not a dict")
	}
	if rot, _ := pd["Rotate"].(int); rot != 90 {
		t.Errorf("page 2 Rotate = %v, want 90", pd["Rotate"])
	}
}

func TestProtectContentIntact(t *testing.T) {
	wm, err := Watermark(classicPDF(), WatermarkOpts{Text: "SECRET"})
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	p, err := Protect(wm, "pw", "")
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	d, err := ParseWithPassword(p, "pw")
	if err != nil {
		t.Fatalf("ParseWithPassword: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	found := false
	checkStream := func(v any) {
		st, ok := d.R(v).(*Stream)
		if !ok {
			return
		}
		data, err := d.decodeStream(st)
		if err != nil {
			t.Fatalf("decodeStream: %v", err)
		}
		if bytes.Contains(data, []byte("(SECRET) Tj")) {
			found = true
		}
	}
	for _, pg := range pages {
		pd, ok := d.Get(pg.Num).(Dict)
		if !ok {
			continue
		}
		switch c := d.R(pd["Contents"]).(type) {
		case *Stream:
			checkStream(c)
		case Array:
			for _, e := range c {
				checkStream(e)
			}
		}
	}
	if !found {
		t.Fatalf("expected a page content stream containing \"(SECRET) Tj\"")
	}
}

func TestProtectEmptyPassword(t *testing.T) {
	if _, err := Protect(classicPDF(), "", ""); err == nil {
		t.Fatalf("expected error for empty passwords")
	}
}

func TestProtectKoreanPassword(t *testing.T) {
	_, err := Protect(classicPDF(), "한글", "")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("Latin-1")) {
		t.Fatalf("expected Latin-1 error, got %v", err)
	}
}

func TestUnlockWrongPassword(t *testing.T) {
	p, err := Protect(classicPDF(), "pw", "")
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	if _, err := Unlock(p, "nope"); err == nil {
		t.Fatalf("expected error for wrong password")
	}
}

func TestGetInfoOnProtected(t *testing.T) {
	p, err := Protect(classicPDF(), "pw", "")
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	gi, err := GetInfo(p)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if gi["encrypted"] != true {
		t.Errorf("GetInfo encrypted = %v, want true", gi["encrypted"])
	}
}
