package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"fmt"
)

// This file implements the PDF 1.7 standard security handler (ISO 32000-1
// §7.6.3), revisions 3 (RC4-128) and 4 (AES-128). Revision 5/6 (AES-256) is
// not supported.

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

	if v == 5 || r >= 5 {
		return fmt.Errorf("AES-256 encrypted files are not supported yet")
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

// decryptValue recursively decrypts strings and stream data belonging to
// object num/gen using the document's file key.
func (d *Doc) decryptValue(num, gen int, v any) any {
	switch t := v.(type) {
	case String:
		key := objectKey(d.fileKey, num, gen, d.cryptAES)
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
		key := objectKey(d.fileKey, num, gen, d.cryptAES)
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
		key := objectKey(fileKey, num, gen, aesFilter)
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
		key := objectKey(fileKey, num, gen, aesFilter)
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

// Protect encrypts the document with AES-128 (R4). ownerPw == "" reuses
// userPw as the owner password too.
func Protect(file []byte, userPw, ownerPw string) ([]byte, error) {
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
