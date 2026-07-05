package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"unicode/utf8"
)

// This file implements the PDF standard security handler: ISO 32000-1
// §7.6.3 revisions 3 (RC4-128) and 4 (AES-128), plus ISO 32000-2 §7.6.4
// revisions 5 and 6 (AES-256, /V 5 /AESV3).

var padBytes = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

func padPassword(pw string) ([]byte, error) {
	b := make([]byte, 0, 32)
	for _, r := range pw {
		if r > 255 {
			return nil, fmt.Errorf("password must be Latin-1")
		}
		b = append(b, byte(r))
	}
	if len(b) > 32 {
		b = b[:32]
	}
	out := make([]byte, 32)
	n := copy(out, b)
	copy(out[n:], padBytes)
	return out, nil
}

func rc4Apply(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		// key length is always validated by callers (5..16 bytes); this
		// only fires on programmer error.
		panic(err)
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

func xorKey(key []byte, i byte) []byte {
	out := make([]byte, len(key))
	for j, c := range key {
		out[j] = c ^ i
	}
	return out
}

// computeFileKey implements Algorithm 2: compute the RC4/AES file
// encryption key from a padded password.
func computeFileKey(paddedPw, o []byte, p int32, id0 []byte, keyLen int, r int, encryptMeta bool) []byte {
	h := md5.New()
	h.Write(paddedPw)
	h.Write(o[:32])
	var pb [4]byte
	up := uint32(p)
	pb[0] = byte(up)
	pb[1] = byte(up >> 8)
	pb[2] = byte(up >> 16)
	pb[3] = byte(up >> 24)
	h.Write(pb[:])
	h.Write(id0)
	if r >= 4 && !encryptMeta {
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	sum := h.Sum(nil)
	for i := 0; i < 50; i++ {
		sum2 := md5.Sum(sum[:keyLen])
		sum = sum2[:]
	}
	return sum[:keyLen]
}

// computeO implements Algorithm 3: compute the /O entry.
func computeO(userPw, ownerPw string) ([]byte, error) {
	if ownerPw == "" {
		ownerPw = userPw
	}
	paddedOwner, err := padPassword(ownerPw)
	if err != nil {
		return nil, err
	}
	paddedUser, err := padPassword(userPw)
	if err != nil {
		return nil, err
	}
	k := md5.Sum(paddedOwner)
	sum := k[:]
	for i := 0; i < 50; i++ {
		sum2 := md5.Sum(sum[:16])
		sum = sum2[:]
	}
	rc4key := sum[:16]
	o := rc4Apply(rc4key, paddedUser)
	for i := 1; i <= 19; i++ {
		o = rc4Apply(xorKey(rc4key, byte(i)), o)
	}
	return o, nil
}

// computeU implements Algorithm 5: compute the /U entry for R3/R4.
func computeU(fileKey, id0 []byte) []byte {
	h := md5.New()
	h.Write(padBytes)
	h.Write(id0)
	sum := h.Sum(nil)
	u := rc4Apply(fileKey, sum)
	for i := 1; i <= 19; i++ {
		u = rc4Apply(xorKey(fileKey, byte(i)), u)
	}
	out := make([]byte, 32)
	copy(out, u)
	return out
}

// checkUserPassword implements Algorithm 6: verify a candidate user
// password (already padded), returning the file key on success.
func checkUserPassword(paddedPw, o []byte, p int32, id0 []byte, keyLen, r int, encryptMeta bool, storedU []byte) ([]byte, bool) {
	key := computeFileKey(paddedPw, o, p, id0, keyLen, r, encryptMeta)
	u := computeU(key, id0)
	if len(storedU) < 16 || !bytes.Equal(u[:16], storedU[:16]) {
		return nil, false
	}
	return key, true
}

// checkOwnerPassword implements Algorithm 7: verify a candidate owner
// password, returning the file key on success.
func checkOwnerPassword(ownerPw string, o []byte, p int32, id0 []byte, keyLen, r int, encryptMeta bool, storedU []byte) ([]byte, bool) {
	paddedOwner, err := padPassword(ownerPw)
	if err != nil {
		return nil, false
	}
	k := md5.Sum(paddedOwner)
	sum := k[:]
	for i := 0; i < 50; i++ {
		sum2 := md5.Sum(sum[:16])
		sum = sum2[:]
	}
	rc4key := sum[:16]
	decrypted := append([]byte(nil), o[:32]...)
	for i := 19; i >= 0; i-- {
		decrypted = rc4Apply(xorKey(rc4key, byte(i)), decrypted)
	}
	// decrypted is now the padded user password; feed it straight into
	// Algorithm 2/6 without re-padding.
	return checkUserPassword(decrypted, o, p, id0, keyLen, r, encryptMeta, storedU)
}

// objectKey implements Algorithm 1: derive the per-object encryption key.
func objectKey(fileKey []byte, num, gen int, aesFilter bool) []byte {
	h := md5.New()
	h.Write(fileKey)
	h.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16)})
	h.Write([]byte{byte(gen), byte(gen >> 8)})
	if aesFilter {
		h.Write([]byte{0x73, 0x41, 0x6C, 0x54})
	}
	sum := h.Sum(nil)
	n := len(fileKey) + 5
	if n > 16 {
		n = 16
	}
	return sum[:n]
}

// isV5 reports whether fileKey is a V5 (AES-256) file key, which is used
// directly for string/stream crypto instead of through objectKey. RC4 and
// AES-128 file keys are always 5-16 bytes, so a 32-byte key is unambiguous.
func isV5(fileKey []byte) bool {
	return len(fileKey) == 32
}

// cryptKey returns the key to use for object num/gen: the file key itself
// for V5, or the Algorithm 1 per-object key otherwise.
func cryptKey(fileKey []byte, num, gen int, aesFilter bool) []byte {
	if isV5(fileKey) {
		return fileKey
	}
	return objectKey(fileKey, num, gen, aesFilter)
}

func aesEncrypt(key, plain []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	padLen := 16 - len(plain)%16
	padded := make([]byte, len(plain)+padLen)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		panic(err)
	}
	out := make([]byte, 16+len(padded))
	copy(out, iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[16:], padded)
	return out
}

func aesDecrypt(key, data []byte) []byte {
	if len(data) < 32 || len(data)%16 != 0 {
		return data
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return data
	}
	iv := data[:16]
	ct := data[16:]
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
	// ponytail: lenient padding, garbage in garbage out beats hard failure
	if n := len(out); n > 0 {
		padLen := int(out[n-1])
		if padLen >= 1 && padLen <= 16 && padLen <= n {
			ok := true
			for _, b := range out[n-padLen:] {
				if int(b) != padLen {
					ok = false
					break
				}
			}
			if ok {
				return out[:n-padLen]
			}
		}
	}
	return out
}

// ------------------------------------------------------- AES-256 (V5) ---
//
// ISO 32000-2 §7.6.4 replaces the RC4/AES-128 handler above with a scheme
// built around a random 32-byte file key that is never derived from the
// password directly: the password instead unwraps /UE or /OE (Algorithm
// 8/9, checked via Algorithm 11/12) to recover it. Strings/streams are then
// AES-256-CBC'd with that file key used as-is (no per-object Algorithm 1
// derivation, unlike RC4/AES-128).
//
// There is no separate Doc field marking a document as "V5": the file key
// length is unambiguous (RC4/AES-128 keys are always 5-16 bytes, AES-256
// keys always 32), so isV5 below just checks length.

// zeroIV is the all-zero initialization vector Algorithms 8, 9, 10, 11 and
// 12 use for their AES-256-CBC-no-padding operations.
var zeroIV = make([]byte, 16)

// aesCBCRaw runs raw (unpadded) AES-CBC in either direction; data must be a
// multiple of the block size. With a zero iv and exactly one 16-byte block,
// this is equivalent to the ECB step Algorithm 10 specifies for /Perms.
func aesCBCRaw(key, iv, data []byte, encrypt bool) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(data))
	if encrypt {
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	} else {
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	}
	return out
}

// prepPassword256 prepares a password for the V5 handler: UTF-8 bytes
// truncated to 127 bytes without splitting a multi-byte rune. This is a
// SASLprep-lite stand-in; full SASLprep normalization needs Unicode tables
// this stdlib-only codebase doesn't carry.
func prepPassword256(pw string) []byte {
	b := []byte(pw)
	if len(b) > 127 {
		b = b[:127]
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	return b
}

// hashR5 is the revision-5 password hash (Adobe's pre-standard "extension
// level 3" scheme, later folded into ISO 32000-2 as Algorithm 2.A): plain
// SHA-256 over password || salt || extra, with no hardening rounds.
func hashR5(pw, salt, extra []byte) []byte {
	h := sha256.New()
	h.Write(pw)
	h.Write(salt)
	h.Write(extra)
	return h.Sum(nil)
}

// hashR6 implements ISO 32000-2 Algorithm 2.B, the hardened hash used by
// revision 6: repeatedly AES-encrypt 64 copies of (password||K||extra)
// under a key/iv drawn from K, then rehash the ciphertext with a hash
// function chosen by the ciphertext itself, for at least 64 rounds.
func hashR6(pw, salt, extra []byte) []byte {
	k := hashR5(pw, salt, extra) // initial K = SHA-256(pw||salt||extra)
	chunk := make([]byte, 0, len(pw)+64+len(extra))
	round := 0
	for {
		chunk = chunk[:0]
		chunk = append(chunk, pw...)
		chunk = append(chunk, k...)
		chunk = append(chunk, extra...)
		k1 := make([]byte, 0, 64*len(chunk))
		for i := 0; i < 64; i++ {
			k1 = append(k1, chunk...)
		}
		e := aesCBCRaw(k[:16], k[16:32], k1, true)
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		switch sum % 3 {
		case 0:
			s := sha256.Sum256(e)
			k = s[:]
		case 1:
			s := sha512.Sum384(e)
			k = s[:]
		default:
			s := sha512.Sum512(e)
			k = s[:]
		}
		round++
		if round >= 64 && int(e[len(e)-1]) <= round-32 {
			break
		}
	}
	return k[:32]
}

// hashRevision dispatches to Algorithm 2.A (r==5) or 2.B (r==6).
func hashRevision(r int, pw, salt, extra []byte) []byte {
	if r >= 6 {
		return hashR6(pw, salt, extra)
	}
	return hashR5(pw, salt, extra)
}

// computeUAES256 implements Algorithm 8: compute the /U and /UE entries
// for a V5 encryption dictionary from a random file key.
func computeUAES256(fileKey []byte, userPw string, r int) (u, ue []byte, err error) {
	pw := prepPassword256(userPw)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	valSalt, keySalt := salt[:8], salt[8:]
	hash := hashRevision(r, pw, valSalt, nil)
	u = make([]byte, 48)
	copy(u, hash)
	copy(u[32:40], valSalt)
	copy(u[40:48], keySalt)
	interKey := hashRevision(r, pw, keySalt, nil)
	ue = aesCBCRaw(interKey, zeroIV, fileKey, true)
	return u, ue, nil
}

// computeOAES256 implements Algorithm 9: compute the /O and /OE entries.
// u is the full 48-byte /U string, used as extra hash input.
func computeOAES256(fileKey []byte, ownerPw string, u []byte, r int) (o, oe []byte, err error) {
	pw := prepPassword256(ownerPw)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	valSalt, keySalt := salt[:8], salt[8:]
	hash := hashRevision(r, pw, valSalt, u)
	o = make([]byte, 48)
	copy(o, hash)
	copy(o[32:40], valSalt)
	copy(o[40:48], keySalt)
	interKey := hashRevision(r, pw, keySalt, u)
	oe = aesCBCRaw(interKey, zeroIV, fileKey, true)
	return o, oe, nil
}

// computePerms implements Algorithm 10: compute the /Perms entry.
func computePerms(fileKey []byte, p int32, encryptMeta bool) ([]byte, error) {
	var b [16]byte
	up := uint32(p)
	b[0] = byte(up)
	b[1] = byte(up >> 8)
	b[2] = byte(up >> 16)
	b[3] = byte(up >> 24)
	b[4], b[5], b[6], b[7] = 0xFF, 0xFF, 0xFF, 0xFF
	if encryptMeta {
		b[8] = 'T'
	} else {
		b[8] = 'F'
	}
	b[9], b[10], b[11] = 'a', 'd', 'b'
	if _, err := rand.Read(b[12:16]); err != nil {
		return nil, err
	}
	return aesCBCRaw(fileKey, zeroIV, b[:], true), nil
}

// checkUserPasswordAES256 implements Algorithm 11 (validate a candidate
// user password) plus the AES-256-CBC-no-pad unwrap that recovers the file
// key from /UE on success.
func checkUserPasswordAES256(pw string, u, ue []byte, r int) ([]byte, bool) {
	if len(u) < 48 || len(ue) < 32 {
		return nil, false
	}
	pwBytes := prepPassword256(pw)
	hash := hashRevision(r, pwBytes, u[32:40], nil)
	if !bytes.Equal(hash, u[:32]) {
		return nil, false
	}
	interKey := hashRevision(r, pwBytes, u[40:48], nil)
	return aesCBCRaw(interKey, zeroIV, ue[:32], false), true
}

// checkOwnerPasswordAES256 implements Algorithm 12 (validate a candidate
// owner password) plus the /OE unwrap.
func checkOwnerPasswordAES256(pw string, o, oe, u []byte, r int) ([]byte, bool) {
	if len(o) < 48 || len(oe) < 32 || len(u) < 48 {
		return nil, false
	}
	pwBytes := prepPassword256(pw)
	hash := hashRevision(r, pwBytes, o[32:40], u[:48])
	if !bytes.Equal(hash, o[:32]) {
		return nil, false
	}
	interKey := hashRevision(r, pwBytes, o[40:48], u[:48])
	return aesCBCRaw(interKey, zeroIV, oe[:32], false), true
}

// ------------------------------------------------------------ parsing ---

// setupCrypt reads the /Encrypt dictionary, verifies pw against it (as
// either the user or owner password), and installs the resulting file key.
func (d *Doc) setupCrypt(pw string) error {
	encRef, ok := d.trailer["Encrypt"]
	if !ok {
		return nil
	}
	enc, ok := d.R(encRef).(Dict)
	if !ok {
		return fmt.Errorf("bad /Encrypt dictionary")
	}
	if filter, _ := enc["Filter"].(Name); filter != "Standard" {
		return fmt.Errorf("unsupported security handler")
	}
	v, _ := enc["V"].(int)
	r, _ := enc["R"].(int)

	if v >= 5 || r >= 5 {
		return d.setupCryptV5(enc, pw)
	}

	length, ok := enc["Length"].(int)
	if !ok {
		length = 40
	}
	keyLen := length / 8

	oStr, ok := enc["O"].(String)
	if !ok || len(oStr) < 32 {
		return fmt.Errorf("bad /Encrypt /O entry")
	}
	uStr, ok := enc["U"].(String)
	if !ok || len(uStr) < 32 {
		return fmt.Errorf("bad /Encrypt /U entry")
	}
	o := []byte(oStr[:32])
	u := []byte(uStr[:32])

	var p int32
	switch pv := enc["P"].(type) {
	case int:
		p = int32(pv)
	case float64:
		p = int32(pv)
	}

	encryptMeta := true
	if em, ok := enc["EncryptMetadata"].(bool); ok {
		encryptMeta = em
	}

	var id0 []byte
	if idArr, ok := d.R(d.trailer["ID"]).(Array); ok && len(idArr) > 0 {
		if s, ok := idArr[0].(String); ok {
			id0 = []byte(s)
		}
	}

	aesFilter := false
	switch v {
	case 1:
		keyLen = 5
	case 2:
		keyLen = length / 8
	case 4:
		keyLen = length / 8
		cf, _ := d.R(enc["CF"]).(Dict)
		stdCF, _ := d.R(cf["StdCF"]).(Dict)
		cfm, _ := stdCF["CFM"].(Name)
		switch cfm {
		case "AESV2":
			aesFilter = true
		case "V2":
			aesFilter = false
		default:
			return fmt.Errorf("unsupported crypt filter")
		}
		if stmF, ok := enc["StmF"]; ok && stmF != Name("StdCF") {
			return fmt.Errorf("unsupported crypt filter")
		}
		if strF, ok := enc["StrF"]; ok && strF != Name("StdCF") {
			return fmt.Errorf("unsupported crypt filter")
		}
	}

	paddedPw, err := padPassword(pw)
	if err != nil {
		return err
	}
	key, okU := checkUserPassword(paddedPw, o, p, id0, keyLen, r, encryptMeta, u)
	if !okU {
		key, okU = checkOwnerPassword(pw, o, p, id0, keyLen, r, encryptMeta, u)
	}
	if !okU {
		return fmt.Errorf("wrong password")
	}

	d.fileKey = key
	d.cryptAES = aesFilter
	return nil
}

// setupCryptV5 handles /Encrypt dictionaries with V>=5 (R=5 or R=6):
// AES-256 per ISO 32000-2 §7.6.4 (algorithms 2.A/2.B, 8, 9, 10, 11, 12).
func (d *Doc) setupCryptV5(enc Dict, pw string) error {
	r, _ := enc["R"].(int)
	if r != 5 && r != 6 {
		return fmt.Errorf("unsupported encryption revision")
	}
	if cf, ok := d.R(enc["CF"]).(Dict); ok {
		if stdCF, ok := d.R(cf["StdCF"]).(Dict); ok {
			if cfm, _ := stdCF["CFM"].(Name); cfm != "" && cfm != "AESV3" {
				return fmt.Errorf("unsupported crypt filter")
			}
		}
	}

	oStr, ok := enc["O"].(String)
	if !ok || len(oStr) < 48 {
		return fmt.Errorf("bad /Encrypt /O entry")
	}
	uStr, ok := enc["U"].(String)
	if !ok || len(uStr) < 48 {
		return fmt.Errorf("bad /Encrypt /U entry")
	}
	ueStr, ok := enc["UE"].(String)
	if !ok || len(ueStr) < 32 {
		return fmt.Errorf("bad /Encrypt /UE entry")
	}
	oeStr, ok := enc["OE"].(String)
	if !ok || len(oeStr) < 32 {
		return fmt.Errorf("bad /Encrypt /OE entry")
	}
	o, u, ue, oe := []byte(oStr), []byte(uStr), []byte(ueStr), []byte(oeStr)

	fileKey, okKey := checkUserPasswordAES256(pw, u, ue, r)
	if !okKey {
		fileKey, okKey = checkOwnerPasswordAES256(pw, o, oe, u, r)
	}
	if !okKey {
		return fmt.Errorf("wrong password")
	}

	if permsStr, ok := enc["Perms"].(String); ok && len(permsStr) >= 16 {
		perms := aesCBCRaw(fileKey, zeroIV, []byte(permsStr[:16]), false)
		if perms[9] != 'a' || perms[10] != 'd' || perms[11] != 'b' {
			return fmt.Errorf("bad /Encrypt /Perms entry")
		}
	}

	d.fileKey = fileKey
	d.cryptAES = true
	return nil
}

// decryptValue recursively decrypts strings and stream data belonging to
// object num/gen using the document's file key.
func (d *Doc) decryptValue(num, gen int, v any) any {
	switch t := v.(type) {
	case String:
		key := cryptKey(d.fileKey, num, gen, d.cryptAES)
		if d.cryptAES {
			return String(aesDecrypt(key, []byte(t)))
		}
		return String(rc4Apply(key, []byte(t)))
	case Dict:
		for k, vv := range t {
			t[k] = d.decryptValue(num, gen, vv)
		}
		return t
	case Array:
		for i, vv := range t {
			t[i] = d.decryptValue(num, gen, vv)
		}
		return t
	case *Stream:
		d.decryptValue(num, gen, t.Dict)
		key := cryptKey(d.fileKey, num, gen, d.cryptAES)
		if d.cryptAES {
			t.Data = aesDecrypt(key, t.Data)
		} else {
			t.Data = rc4Apply(key, t.Data)
		}
		t.Dict["Length"] = len(t.Data)
		return t
	default:
		return v
	}
}

// encryptValue is the mirror of decryptValue, used by Protect.
func encryptValue(fileKey []byte, aesFilter bool, num, gen int, v any) any {
	switch t := v.(type) {
	case String:
		key := cryptKey(fileKey, num, gen, aesFilter)
		if aesFilter {
			return String(aesEncrypt(key, []byte(t)))
		}
		return String(rc4Apply(key, []byte(t)))
	case Dict:
		for k, vv := range t {
			t[k] = encryptValue(fileKey, aesFilter, num, gen, vv)
		}
		return t
	case Array:
		for i, vv := range t {
			t[i] = encryptValue(fileKey, aesFilter, num, gen, vv)
		}
		return t
	case *Stream:
		encryptValue(fileKey, aesFilter, num, gen, t.Dict)
		key := cryptKey(fileKey, num, gen, aesFilter)
		if aesFilter {
			t.Data = aesEncrypt(key, t.Data)
		} else {
			t.Data = rc4Apply(key, t.Data)
		}
		t.Dict["Length"] = len(t.Data)
		return t
	default:
		return v
	}
}

// ------------------------------------------------------------- public ---

// ParseWithPassword opens an encrypted (or plain) PDF.
func ParseWithPassword(data []byte, pw string) (*Doc, error) {
	return parse(data, &pw)
}

// Cipher selects the encryption algorithm ProtectCipher uses.
type Cipher int

const (
	// CipherAES128 selects AES-128 (V4/R4, ISO 32000-1) — Protect's default.
	CipherAES128 Cipher = iota
	// CipherAES256 selects AES-256 (V5/R6, ISO 32000-2).
	CipherAES256
)

// Protect encrypts the document with AES-128 (R4). ownerPw == "" reuses
// userPw as the owner password too.
func Protect(file []byte, userPw, ownerPw string) ([]byte, error) {
	return ProtectCipher(file, userPw, ownerPw, CipherAES128)
}

// ProtectCipher is Protect with an explicit cipher choice.
func ProtectCipher(file []byte, userPw, ownerPw string, c Cipher) ([]byte, error) {
	if c == CipherAES256 {
		return protectAES256(file, userPw, ownerPw)
	}
	return protectWith(file, userPw, ownerPw, false)
}

// protectWith implements Protect; useRC4 selects R3/RC4-128 instead of
// R4/AES-128, used by tests to exercise the RC4 decrypt path.
func protectWith(file []byte, userPw, ownerPw string, useRC4 bool) ([]byte, error) {
	if userPw == "" && ownerPw == "" {
		return nil, fmt.Errorf("password required")
	}
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	b, root, err := buildDoc([]*Doc{d}, [][]Page{pages}, nil)
	if err != nil {
		return nil, err
	}

	id0 := md5.Sum(file)
	b.id = String(id0[:])

	p := int32(-4) // ponytail: grant all permissions, we don't model fine-grained bits

	o, err := computeO(userPw, ownerPw)
	if err != nil {
		return nil, err
	}

	r := 4
	aesFilter := true
	if useRC4 {
		r = 3
		aesFilter = false
	}

	paddedUser, err := padPassword(userPw)
	if err != nil {
		return nil, err
	}
	fileKey := computeFileKey(paddedUser, o, p, id0[:], 16, r, true)
	u := computeU(fileKey, id0[:])

	var encDict Dict
	if aesFilter {
		encDict = Dict{
			"Filter": Name("Standard"),
			"V":      4,
			"R":      4,
			"Length": 128,
			"CF": Dict{
				"StdCF": Dict{
					"CFM":       Name("AESV2"),
					"AuthEvent": Name("DocOpen"),
					"Length":    16,
				},
			},
			"StmF": Name("StdCF"),
			"StrF": Name("StdCF"),
			"O":    String(o),
			"U":    String(u),
			"P":    int(p),
		}
	} else {
		encDict = Dict{
			"Filter": Name("Standard"),
			"V":      2,
			"R":      3,
			"Length": 128,
			"O":      String(o),
			"U":      String(u),
			"P":      int(p),
		}
	}
	encRef := b.alloc()
	b.objs[encRef.Num-1] = encDict
	b.encryptRef = encRef

	for i := range b.objs {
		if i == encRef.Num-1 {
			continue
		}
		b.objs[i] = encryptValue(fileKey, aesFilter, i+1, 0, b.objs[i])
	}

	return b.bytes(root), nil
}

// protectAES256 implements ProtectCipher(..., CipherAES256): a random
// 32-byte file key, wrapped for both the user and owner password per
// Algorithms 8 and 9, written out as a V=5 R=6 /AESV3 encrypt dict.
func protectAES256(file []byte, userPw, ownerPw string) ([]byte, error) {
	if userPw == "" && ownerPw == "" {
		return nil, fmt.Errorf("password required")
	}
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	b, root, err := buildDoc([]*Doc{d}, [][]Page{pages}, nil)
	if err != nil {
		return nil, err
	}

	id0 := md5.Sum(file)
	b.id = String(id0[:])

	p := int32(-4) // ponytail: grant all permissions, we don't model fine-grained bits
	const r = 6

	fileKey := make([]byte, 32)
	if _, err := rand.Read(fileKey); err != nil {
		return nil, err
	}

	if ownerPw == "" {
		ownerPw = userPw
	}

	u, ue, err := computeUAES256(fileKey, userPw, r)
	if err != nil {
		return nil, err
	}
	o, oe, err := computeOAES256(fileKey, ownerPw, u, r)
	if err != nil {
		return nil, err
	}
	perms, err := computePerms(fileKey, p, true)
	if err != nil {
		return nil, err
	}

	encDict := Dict{
		"Filter": Name("Standard"),
		"V":      5,
		"R":      r,
		"Length": 256,
		"CF": Dict{
			"StdCF": Dict{
				"CFM":       Name("AESV3"),
				"AuthEvent": Name("DocOpen"),
				"Length":    32,
			},
		},
		"StmF":  Name("StdCF"),
		"StrF":  Name("StdCF"),
		"O":     String(o),
		"U":     String(u),
		"OE":    String(oe),
		"UE":    String(ue),
		"P":     int(p),
		"Perms": String(perms),
	}
	encRef := b.alloc()
	b.objs[encRef.Num-1] = encDict
	b.encryptRef = encRef

	for i := range b.objs {
		if i == encRef.Num-1 {
			continue
		}
		b.objs[i] = encryptValue(fileKey, true, i+1, 0, b.objs[i])
	}

	return b.bytes(root), nil
}

// Unlock removes encryption, given a valid user or owner password.
func Unlock(file []byte, password string) ([]byte, error) {
	d, err := ParseWithPassword(file, password)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	return build([]*Doc{d}, [][]Page{pages})
}
