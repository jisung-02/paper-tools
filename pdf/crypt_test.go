package pdf

import (
	"bytes"
	"crypto/rand"
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

func TestProtectRequiresUserPassword(t *testing.T) {
	if _, err := Protect(classicPDF(), "", "owner"); err == nil {
		t.Fatalf("expected error for empty user password")
	}
	if _, err := ProtectCipher(classicPDF(), "", "owner", CipherAES256); err == nil {
		t.Fatalf("expected AES-256 error for empty user password")
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

func TestProtectRoundTripAES256(t *testing.T) {
	p, err := ProtectCipher(classicPDF(), "user1", "owner1", CipherAES256)
	if err != nil {
		t.Fatalf("ProtectCipher: %v", err)
	}
	if !bytes.Contains(p, []byte("/V 5")) {
		t.Errorf("expected /V 5 in AES-256-protected output")
	}
	if !bytes.Contains(p, []byte("/AESV3")) {
		t.Errorf("expected /AESV3 in AES-256-protected output")
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

func TestProtectAES256ContentIntact(t *testing.T) {
	wm, err := Watermark(classicPDF(), WatermarkOpts{Text: "SECRET"})
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	p, err := ProtectCipher(wm, "pw", "", CipherAES256)
	if err != nil {
		t.Fatalf("ProtectCipher: %v", err)
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

func TestUnlockAES256(t *testing.T) {
	p, err := ProtectCipher(classicPDF(), "pw", "", CipherAES256)
	if err != nil {
		t.Fatalf("ProtectCipher: %v", err)
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
}

func TestUnlockAES256WrongPassword(t *testing.T) {
	p, err := ProtectCipher(classicPDF(), "pw", "", CipherAES256)
	if err != nil {
		t.Fatalf("ProtectCipher: %v", err)
	}
	if _, err := Unlock(p, "nope"); err == nil {
		t.Fatalf("expected error for wrong password")
	}
}

// TestHashR6KeyDerivation exercises Algorithm 2.B end to end via the
// encrypt/decrypt helpers rather than a canned fixture: computeUAES256
// wraps a random file key under a user password using hashR6, and
// checkUserPasswordAES256 must recover the exact same key. It also
// double-checks the hardened hash isn't a no-op (differs from the plain
// R5 hash of the same inputs).
func TestHashR6KeyDerivation(t *testing.T) {
	fileKey := make([]byte, 32)
	for i := range fileKey {
		fileKey[i] = byte(i)
	}
	u, ue, err := computeUAES256(fileKey, "correct horse", 6)
	if err != nil {
		t.Fatalf("computeUAES256: %v", err)
	}
	got, ok := checkUserPasswordAES256("correct horse", u, ue, 6)
	if !ok {
		t.Fatalf("checkUserPasswordAES256: password rejected")
	}
	if !bytes.Equal(got, fileKey) {
		t.Fatalf("recovered file key = %x, want %x", got, fileKey)
	}
	if _, ok := checkUserPasswordAES256("wrong horse", u, ue, 6); ok {
		t.Fatalf("checkUserPasswordAES256: wrong password accepted")
	}

	r5 := hashR5([]byte("correct horse"), u[32:40], nil)
	r6 := hashR6([]byte("correct horse"), u[32:40], nil)
	if bytes.Equal(r5, r6) {
		t.Fatalf("hashR6 should differ from the unhardened hashR5")
	}
}

// TestHashR5KeyDerivation exercises the (deprecated) revision-5 fixture
// path: R5 skips Algorithm 2.B's hardening rounds entirely, computing the
// hash and key-wrap in one shot.
func TestHashR5KeyDerivation(t *testing.T) {
	fileKey := make([]byte, 32)
	if _, err := rand.Read(fileKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	u, ue, err := computeUAES256(fileKey, "r5-password", 5)
	if err != nil {
		t.Fatalf("computeUAES256: %v", err)
	}
	got, ok := checkUserPasswordAES256("r5-password", u, ue, 5)
	if !ok {
		t.Fatalf("checkUserPasswordAES256: password rejected")
	}
	if !bytes.Equal(got, fileKey) {
		t.Fatalf("recovered file key = %x, want %x", got, fileKey)
	}

	o, oe, err := computeOAES256(fileKey, "r5-owner", u, 5)
	if err != nil {
		t.Fatalf("computeOAES256: %v", err)
	}
	got, ok = checkOwnerPasswordAES256("r5-owner", o, oe, u, 5)
	if !ok {
		t.Fatalf("checkOwnerPasswordAES256: password rejected")
	}
	if !bytes.Equal(got, fileKey) {
		t.Fatalf("owner-recovered file key = %x, want %x", got, fileKey)
	}
	if _, ok := checkOwnerPasswordAES256("nope", o, oe, u, 5); ok {
		t.Fatalf("checkOwnerPasswordAES256: wrong password accepted")
	}
}

func TestPermsRoundTrip(t *testing.T) {
	fileKey := make([]byte, 32)
	if _, err := rand.Read(fileKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	p := int32(-4)
	perms, err := computePerms(fileKey, p, true)
	if err != nil {
		t.Fatalf("computePerms: %v", err)
	}
	if len(perms) != 16 {
		t.Fatalf("computePerms: got %d bytes, want 16", len(perms))
	}
	dec := aesCBCRaw(fileKey, zeroIV, perms, false)
	if dec[9] != 'a' || dec[10] != 'd' || dec[11] != 'b' {
		t.Fatalf("decrypted /Perms signature = %q, want \"adb\"", dec[9:12])
	}
	if dec[8] != 'T' {
		t.Errorf("decrypted /Perms EncryptMetadata byte = %q, want 'T'", dec[8])
	}
}
