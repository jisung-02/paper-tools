# PDF 보안 감사 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**목표:** PDF의 metadata, active action, attachment, form/XFA, signature, encryption 구조를 로컬에서 bounded read-only scan하고 완전성·제한을 포함한 JSON 보고서와 안전 조치 안내를 제공한다.

**구조:** pdf 패키지가 encryption gate 전 envelope facts와 checked object traversal을 소유하고 typed SecurityAuditReport를 만든다. 전용 persistent TinyGo Worker는 `sourceStart → sourceChunk* → sourceFinish → audit|abort` 상태 머신으로 최대 1MiB 청크를 최종 Go source에 직접 복사한다. `/securityaudit/`은 shared required-Worker client, bounded File uploader, 안전한 DOM 표시, recipe와 revision-bound JSON 다운로드만 담당한다.

**기술:** Go 표준 라이브러리, TinyGo WASM, vanilla ES modules, Node test runner, Playwright

## 전역 제약

- 설계 기준은 docs/superpowers/specs/2026-07-09-security-audit-design.md다.
- Go 외부 모듈을 추가하지 않고 go.mod와 go.sum을 변경하지 않는다.
- 입력 PDF는 256MiB, source chunk는 1MiB, xref/object 각 100,000개, edge 1,000,000개, depth 64, 단일 token 8MiB, 누적 parsed string/name 64MiB, finding 1,000개, 개별 decoded stream 64MiB, 누적 decoded stream 256MiB, JSON 1MiB를 넘지 않는다.
- JavaScript source, attachment payload, password, O/U/OE/UE/Perms bytes를 결과에 포함하지 않는다.
- Go 감사기가 최종 source slice에서 SHA-256을 계산한다. `transferOwnership`은 main-thread 사전 복사만 제거하며 JS→Go 복사는 남는다고 명시한다. 장기 보유 모델은 File backing + Go source이고, 전송 추가 메모리는 최대 1MiB 한 chunk다.
- 브라우저 전체 `File.arrayBuffer()`와 main-thread Go fallback을 금지한다. Worker 부재·load/error/messageerror/postMessage 실패는 명시적으로 실패한다.
- signature는 ByteRange 구조만 검사하고 진위·trust를 주장하지 않는다.
- Complete:false를 정상 non-finding으로 바꾸지 않는다.
- `Password *string`으로 미제공과 explicit empty password를 구분하며 wire는 `passwordProvided`를 사용한다.
- catalog runtime marker는 `{"worker":"required","stateful":true,"chunkedIO":true}`다.
- commit과 push를 실행하지 않는다. 각 작업은 검증 결과를 .superpowers/sdd/ report에 기록한다.

---

### 작업 1: encryption gate 전 envelope와 checked resolver

**파일:**

- 수정: pdf/pdf.go
- 수정: pdf/crypt.go
- 생성: pdf/read_limits.go
- 생성: pdf/read_limits_test.go
- 테스트: pdf/pdf_test.go
- 테스트: pdf/crypt_test.go

**인터페이스:**

- 생산: `parseEnvelope(data []byte) (*Doc, error)`
- 생산: `func (d *Doc) resolveChecked(num int) (any, error)`
- 생산: `ParseBounded`, `GetBounded`, `ResolveBounded`, `PagesBounded`, `ReadStats`
- 생산: `func (d *Doc) validateRoot() error`
- 생산: `var ErrWrongPassword = errors.New("wrong password")`; 기존 error text는 wrapping으로 유지한다.
- 유지: encrypted error precedence와 `Doc.Get`의 기존 loose semantics

~~~go
type PDFReadLimits struct {
    MaxInputBytes              uint64 `json:"maxInputBytes"`
    MaxPages                   int    `json:"maxPages"`
    MaxXRefEntries             int    `json:"maxXRefEntries"`
    MaxObjects                 int    `json:"maxObjects"`
    MaxEdges                   int    `json:"maxEdges"`
    MaxDepth                   int    `json:"maxDepth"`
    MaxTokenBytes              uint64 `json:"maxTokenBytes"`
    MaxParsedStringBytes       uint64 `json:"maxParsedStringBytes"`
    MaxDecodedStreamBytes      uint64 `json:"maxDecodedStreamBytes"`
    MaxDecodedStreamTotalBytes uint64 `json:"maxDecodedStreamTotalBytes"`
}

type PDFReadStats struct {
    XRefEntries           uint64 `json:"xrefEntries"`
    CachedObjects         uint64 `json:"cachedObjects"`
    ContainerEdges        uint64 `json:"containerEdges"`
    MaxObservedDepth      uint64 `json:"maxObservedDepth"`
    MaxObservedTokenBytes uint64 `json:"maxObservedTokenBytes"`
    ParsedStringBytes     uint64 `json:"parsedStringBytes"`
    DecodedStreamBytes    uint64 `json:"decodedStreamBytes"`
    PeakDecodedStream     uint64 `json:"peakDecodedStreamBytes"`
    PageTreeNodes         uint64 `json:"pageTreeNodes"`
    Pages                 uint64 `json:"pages"`
}

func ParseBounded(data []byte, limits PDFReadLimits) (*Doc, error)
func (d *Doc) GetBounded(num int) (any, error)
func (d *Doc) ResolveBounded(value any) (any, error)
func (d *Doc) PagesBounded() ([]Page, error)
func (d *Doc) ReadStats() PDFReadStats

type envelopeFacts struct {
    Sections      int
    Revisions     int
    Kinds         []string
    RepeatedPrev  bool
    PrevCycle     bool
    MalformedPrev bool
}
~~~

`ParseBounded`가 만드는 `Doc`은 resolved `PDFReadLimits`와 session-wide counter를 소유한다. public `Parse/Get/R/Pages`는 legacy behavior를 유지하고 Security Audit, Scan Cleanup, Smart Split만 checked bounded API를 사용한다. source read counter와 output graph counter는 서로 별도다.

- [ ] **1. encrypted envelope RED 테스트 작성**

~~~go
func TestParseEnvelopeIndexesEncryptedDocumentWithoutDecryptingContent(t *testing.T) {
    protected, err := Protect(classicPDF(), "pw", "")
    if err != nil {
        t.Fatalf("Protect: %v", err)
    }
    doc, err := parseEnvelope(protected)
    if err != nil {
        t.Fatalf("parseEnvelope: %v", err)
    }
    if _, ok := doc.trailer["Encrypt"]; !ok {
        t.Fatal("encrypted envelope lost trailer Encrypt")
    }
    if doc.envelopeFacts.Sections < 1 || doc.envelopeFacts.Revisions < 1 {
        t.Fatalf("envelope facts = %#v", doc.envelopeFacts)
    }
    if _, err := Parse(protected); !errors.Is(err, ErrEncrypted) {
        t.Fatalf("Parse error = %v, want ErrEncrypted", err)
    }
}
~~~

- [ ] **2. RED 확인**

실행: go test ./pdf -run 'TestParseEnvelope|TestProtectRoundTrip' -count=1

예상: undefined: parseEnvelope로 실패한다.

- [ ] **3. xref facts와 encrypted precedence RED 테스트 작성**

test helper는 trailer bytes 길이를 바꾸지 않고 마지막 key를 변형한다.

~~~go
func replaceLastExact(t *testing.T, src []byte, old, replacement string) []byte {
    t.Helper()
    if len(old) != len(replacement) {
        t.Fatalf("replacement length %d != %d", len(replacement), len(old))
    }
    out := append([]byte(nil), src...)
    at := bytes.LastIndex(out, []byte(old))
    if at < 0 {
        t.Fatalf("fixture does not contain %q", old)
    }
    copy(out[at:at+len(old)], replacement)
    return out
}

func TestEncryptedErrorPrecedesRootValidation(t *testing.T) {
    protected, err := Protect(classicPDF(), "pw", "")
    if err != nil { t.Fatal(err) }
    missing := replaceLastExact(t, protected, "/Root", "/R00t")
    bad := replaceLastExact(t, protected, "/Root 1 0 R", "/Root 0 0 R")
    for name, file := range map[string][]byte{"missing": missing, "bad": bad} {
        t.Run(name, func(t *testing.T) {
            if _, err := Parse(file); !errors.Is(err, ErrEncrypted) {
                t.Fatalf("Parse error = %v, want ErrEncrypted", err)
            }
            if _, err := ParseWithPassword(file, "wrong"); !errors.Is(err, ErrWrongPassword) {
                t.Fatalf("wrong password error = %v", err)
            }
            if _, err := ParseWithPassword(file, "pw"); err == nil || !strings.Contains(err.Error(), "Root") {
                t.Fatalf("valid password error = %v, want Root error", err)
            }
        })
    }
}

func TestParseEnvelopeRecordsPrevChainFacts(t *testing.T) {
    for _, tc := range []struct {
        name string
        file []byte
        want envelopeFacts
    }{
        {"classic", classicPDF(), envelopeFacts{Sections: 1, Revisions: 1, Kinds: []string{"classic"}}},
        {"xref stream", xrefStreamPDF(), envelopeFacts{Sections: 1, Revisions: 1, Kinds: []string{"stream"}}},
        {"hybrid", hybridXrefPDF(t), envelopeFacts{Sections: 2, Revisions: 1, Kinds: []string{"hybrid"}}},
        {"prev cycle", prevCyclePDF(t), envelopeFacts{Sections: 2, Revisions: 2, Kinds: []string{"classic"}, RepeatedPrev: true, PrevCycle: true}},
    } {
        t.Run(tc.name, func(t *testing.T) {
            doc, err := parseEnvelope(tc.file)
            if err != nil { t.Fatal(err) }
            if !reflect.DeepEqual(doc.envelopeFacts, tc.want) {
                t.Fatalf("facts = %#v, want %#v", doc.envelopeFacts, tc.want)
            }
        })
    }
}
~~~

`hybridXrefPDF`, `prevCyclePDF`는 test file 안에서 raw PDF/xref offset을 계산하는 deterministic helper로 작성하고 fixture의 `/Prev`가 실제 이전 xref 또는 cycle offset을 가리키는지 생성 직후 assert한다.

- [ ] **4. parse flow와 typed facts 구현**

parse의 xref/trailer indexing을 다음 계약으로 옮긴다.

~~~go
func parseEnvelope(data []byte) (*Doc, error) {
    off, err := findStartXref(data)
    if err != nil {
        return nil, err
    }
    d := &Doc{
        data: data, xref: map[int]xrefEntry{}, trailer: Dict{},
        objs: map[int]any{}, loading: map[int]bool{},
    }
    d.envelopeFacts = envelopeFacts{Kinds: []string{}}
    seen := map[int]bool{}
    for revision := 0; off != 0; revision++ {
        if seen[off] {
            d.envelopeFacts.RepeatedPrev = true
            d.envelopeFacts.PrevCycle = true
            break
        }
        seen[off] = true
        prev, kind, sections, readErr := d.readXrefEnvelopeSection(off)
        if readErr != nil {
            if revision == 0 { return nil, readErr }
            d.envelopeFacts.MalformedPrev = true
            d.envelopeErr = readErr
            break
        }
        d.envelopeFacts.Revisions++
        d.envelopeFacts.Sections += sections
        d.envelopeFacts.Kinds = appendEnvelopeKind(d.envelopeFacts.Kinds, kind)
        off = prev
    }
    return d, nil
}
~~~

`readXrefEnvelopeSection`은 main section의 kind와 hybrid companion을 합쳐 `sections`를 반환한다. `parse`는 `parseEnvelope` 뒤 `/Encrypt`와 password를 먼저 처리하고 그 다음 `envelopeErr`, `validateRoot` 순으로 검사한다. cycle은 현재처럼 parse를 중단시키지 않지만 typed fact로 남고 감사의 `Complete:false` 근거가 된다.

- [ ] **5. 공통 bounded source parser RED 테스트 작성**

`pdf/read_limits_test.go`에 xref 100,001번째 entry, cached object 100,001번째, direct dict/array child 1,000,001번째, depth 65, name/literal/hex token 8MiB+1, 누적 parsed string/name 64MiB+1, object-stream `/N` 100,001, page-tree node/page 501번째와 depth 65 fixture를 둔다. tracking hook은 collected/index/pair/page slice capacity, map insert, decoded buffer, inherited attribute copy와 recursive frame이 cap을 넘기 전에 실패했는지 기록한다. `ParseBounded` 성공 뒤 여러 `GetBounded`와 `PagesBounded` 호출의 counter가 session 전체에서 누적되고 `ReadStats`가 resolved limit을 넘지 않는지 검증한다.

기존 `Parse`, `ParseWithPassword`, `Get`, `R`, `Pages` regression은 동일 fixture의 legacy behavior를 바꾸지 않는지 별도로 고정한다. bounded error는 resource code와 limit을 가진 typed error이며 raw token이나 payload를 message에 넣지 않는다.

- [ ] **6. 공통 bounded parser·resolver·page traversal 구현**

`pdf/read_limits.go`에 resolved limit과 session-wide counter를 구현하고 `lexer`, classic/xref-stream reader, object-stream loader와 page-tree walker가 optional budget을 받도록 일반화한다. `ParseBounded`는 input 크기를 먼저 검사한 뒤 xref entry와 revision seen offset을 합쳐 `MaxXRefEntries` 안에서 map에 넣으며, `GetBounded`는 object budget을 parse/cache 전에, `ResolveBounded`는 hop/depth와 decode budget을 dereference 전에, `PagesBounded`는 visited/inherited/page append와 child recursion 전에 소비한다. `readClassicXref`의 collected slice, xref-stream `/Index`, object-stream pair capacity는 선언 count를 checked arithmetic으로 검증한 뒤에만 할당한다. public loose API는 nil budget으로 기존 경로를 사용한다.

- [ ] **7. checked resolver RED 테스트 작성**

~~~go
func TestResolveCheckedReportsMissingObject(t *testing.T) {
    doc, err := Parse(classicPDF())
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    if _, err := doc.resolveChecked(999999); err == nil {
        t.Fatal("missing object resolved without an error")
    }
}
~~~

- [ ] **8. resolver와 root validation 구현 후 regression GREEN 확인**

resolveChecked는 xref 존재, re-entrancy와 object parse/decrypt/decode 오류를 반환하고 audit 외 기존 Get semantics는 유지한다.

실행:

~~~sh
go test ./pdf -count=1
go test -race ./pdf -count=1
go vet ./pdf
~~~

예상: 모두 exit 0.

- [ ] **9. 작업 보고 기록**

.superpowers/sdd/security-audit-task-1-report.md에 RED/GREEN 명령·출력, allocation-before-rejection counter, `ReadStats`와 public behavior 비변경 근거를 기록한다. commit/push는 하지 않는다.

### 작업 2: typed bounded 감사 core

**파일:**

- 생성: pdf/security_audit.go
- 생성: pdf/security_audit_test.go
- 생성: pdf/security_signature.go
- 생성: pdf/security_signature_test.go
- 수정: pdf/pdf.go
- 수정: pdf/graph.go
- 수정: pdf/filters.go
- 테스트: pdf/filters_test.go
- 테스트 재사용: pdf/rasterpdf_test.go

**인터페이스:**

~~~go
type SecurityAuditLimits struct {
    MaxInputBytes              uint64
    MaxPages                   int
    MaxXRefEntries             int
    MaxObjects                 int
    MaxEdges                   int
    MaxDepth                   int
    MaxTreeNodes               int
    MaxTokenBytes              uint64
    MaxParsedStringBytes       uint64
    MaxFindings                int
    MaxFieldBytes              int
    MaxReportBytes             int
    MaxDecodedStreamBytes      uint64
    MaxDecodedStreamTotalBytes uint64
}

type SecurityAuditOptions struct {
    Password *string
    Limits   SecurityAuditLimits
}

func AuditSecurity(file []byte, options SecurityAuditOptions) (SecurityAuditReport, error)

type auditDecodeBudget struct {
    perStream uint64
    total     uint64
    used      uint64
    decodeFn  func(func(any) any, *Stream, int) ([]byte, error)
}

type auditReadBudget struct {
    read       *pdfReadBudget
    decode     *auditDecodeBudget
    limitation func(resource string, objectNum int) error
}

func (b *auditDecodeBudget) decode(d *Doc, stream *Stream) ([]byte, error)
func parseEnvelopeForAudit(data []byte, budget *auditReadBudget) (*Doc, error)
func (d *Doc) resolveAuditChecked(num int, budget *auditReadBudget) (any, error)

type signatureRawFacts struct {
    ContentsStart int64
    ContentsEnd   int64
    GapMatches    bool
}

func scanSignatureSyntax(data []byte, entry xrefEntry, byteRange [4]uint64, maxScanBytes uint64) (signatureRawFacts, error)
~~~

- report subtype exact field는 다음이며 추가 임의 map을 두지 않는다.

| type | exact fields |
|---|---|
| `SecurityAuditReport` | `SchemaVersion string`, `Complete bool`, `Limitations []AuditLimitation`, `File AuditFileFacts`, `Encryption AuditEncryptionFacts`, `Summary AuditSummary`, `Findings []AuditFinding` |
| `AuditFileFacts` | `Bytes uint64`, `SHA256 string`, `PDFVersion string`, `Pages int`, `XRef AuditXRefFacts` |
| `AuditXRefFacts` | `Sections int`, `Revisions int`, `Kinds []string`, `RepeatedPrev bool`, `PrevCycle bool`, `MalformedPrev bool` |
| `AuditLimitation` | `Code string`, `Scope string`, `ObjectNum int,omitempty`, `Page int,omitempty`, `Count int,omitempty` |
| `AuditPermissionFacts` | `Known`, `Print`, `Modify`, `Copy`, `Annotate`, `FillForms`, `Accessibility`, `Assemble`, `HighQualityPrint` 모두 `bool` |
| `AuditEncryptionFacts` | `Present bool`, `Handler string`, `V int`, `R int`, `KeyBits int`, `CryptFilters []string`, `EncryptMetadata *bool`, `RawPermissions int64`, `Permissions AuditPermissionFacts`, `PasswordProvided bool`, `ContentInspectable bool`, `InspectionStatus string` |
| `AuditSummary` | `Metadata`, `ActiveContent`, `Attachments`, `Forms`, `Signatures`, `Encryption`, `Total`, `Limitations` 모두 `int` |
| `AuditFindingDetails` | `Filter`, `SubFilter`, `ActionType`, `FieldType`, `Filename`, `Coverage`, `Handler`, `CryptFilter`, `Permission`, `DeclaredSize`, `MetadataKey` 모두 `string,omitempty` |
| `AuditFinding` | `Code`, `Category`, `Severity`, `Evidence` string; `ObjectNum`, `Page` int,omitempty; `Count int`; `Name string,omitempty`; `EncodedLen uint64,omitempty`; `Details AuditFindingDetails` |

모든 JSON name은 설계의 lower camelCase tag를 그대로 사용한다. finding code는 설계의 metadata 6개, active content 10개, attachment 5개, form 6개, signature 6개, encryption 4개 exact allowlist를 상수 table로 정의한다. limitation 19개, category 6개, severity 3개, evidence 9개, inspection status 4개도 설계 allowlist와 byte-for-byte 동일해야 한다.

- [ ] **1. 실제 여섯 범주 aggregate canary RED 테스트 작성**

`securitySixCategoryCanaryPDF`는 XMP, JavaScript, EmbeddedFile, AcroForm, signature field를 가진 builder fixture를 `Protect(..., "pw", "")`로 암호화한다. password를 제공해 content graph까지 검사하고 여섯 범주의 대표 code를 모두 검증한다.

~~~go
func TestAuditSecurityFindsRequiredLiveGraphCategories(t *testing.T) {
    password := "pw"
    report, err := AuditSecurity(
        securitySixCategoryCanaryPDF(t, password),
        SecurityAuditOptions{Password: &password},
    )
    if err != nil {
        t.Fatalf("AuditSecurity: %v", err)
    }
    got := map[string]bool{}
    for _, finding := range report.Findings {
        got[finding.Code] = true
    }
    for _, code := range []string{
        "metadata.xmp",
        "action.javascript",
        "attachment.embedded_file",
        "form.acroform",
        "signature.present",
        "encryption.present",
    } {
        if !got[code] {
            t.Errorf("missing finding %q", code)
        }
    }
    if !report.Complete {
        t.Fatalf("complete fixture reported limitations: %#v", report.Limitations)
    }
}
~~~

- [ ] **2. RED 확인**

실행: go test ./pdf -run '^TestAuditSecurity' -count=1

예상: undefined: AuditSecurity로 실패한다.

- [ ] **3. exact report type, allowlist와 limit resolver 구현**

설계의 exact JSON field와 stable code를 상수 table로 구현한다. zero limit은 설계 기본값으로 해석하고 hard ceiling보다 큰 caller 값은 입력 오류로 거부한다. `SecurityAuditLimits`의 input/page/xref/object/edge/depth/token/parsed/decode field를 작업 1의 `PDFReadLimits`로 byte-for-byte 매핑하고 audit adapter만 partial/hard 분류를 추가한다. `Password:nil`은 미제공, `ptr("")`는 explicit empty password다. `AuditFileFacts.XRef`는 작업 1의 `envelopeFacts`를 변환하며 cycle/malformed facts가 있으면 limitation과 `Complete:false`를 함께 만든다.

- [ ] **4. parser allocation budget RED/GREEN**

audit fixture가 xref subsection 100,001번째 entry, 100,001번째 cached object, dictionary/array 1,000,001번째 edge, 65단계 container, 8MiB+1 literal/hex/name token, 누적 parsed string/name 64MiB+1을 각각 유도한다. tracking allocator 또는 lexer hook으로 cap을 넘는 map/slice/string allocation 전에 실패했음을 확인한다. `parseEnvelopeForAudit`은 latest xref 한도에서 hard input error, 이전 section/도달 object 한도에서 typed limitation과 `Complete:false`를 반환한다. public `Parse` fixture regression은 기존 behavior를 유지한다.

감사 경로는 작업 1의 `pdfReadBudget`과 checked lexer/xref/object-stream primitive를 그대로 사용한다. `auditReadBudget`은 그 typed resource-limit error를 latest envelope에서는 hard error, 이전 section이나 도달 object에서는 limitation으로 분류하는 adapter일 뿐 별도 unbounded parser를 만들지 않는다. 한도 오류가 payload나 raw token을 error/report에 포함하지 않는지 검증한다.

- [ ] **5. decoded-stream budget RED/GREEN**

~~~go
func TestAuditDecodeBudgetPassesRemainingLimitBeforeDecode(t *testing.T) {
    budget := auditDecodeBudget{perStream: 64 << 20, total: 80 << 20}
    calls := []int{}
    budget.decodeFn = func(_ func(any) any, _ *Stream, limit int) ([]byte, error) {
        calls = append(calls, limit)
        return make([]byte, 40<<20), nil
    }
    doc, err := Parse(classicPDF())
    if err != nil { t.Fatal(err) }
    stream := &Stream{Dict: Dict{}, Data: []byte("encoded")}
    if _, err := budget.decode(doc, stream); err != nil { t.Fatal(err) }
    if _, err := budget.decode(doc, stream); err != nil { t.Fatal(err) }
    if !reflect.DeepEqual(calls, []int{64 << 20, 40 << 20}) {
        t.Fatalf("decode limits = %v", calls)
    }
    if _, err := budget.decode(doc, stream); !errors.Is(err, ErrAuditDecodedTotalLimit) {
        t.Fatalf("third decode error = %v", err)
    }
}
~~~

production `decodeFn`은 `decodeStreamWithLimit`다. unfiltered stream도 길이를 먼저 검사하고, filtered stream은 `min(perStream, total-used)`를 decoder에 넘긴 뒤에만 allocation을 허용한다. `parseEnvelopeForAudit`의 xref stream/hybrid companion과 `resolveAuditChecked`의 type-2 object stream도 작업 1에서 `Doc`에 보관한 같은 read/decode counter를 사용하며 public loose `parseEnvelope`, `Doc.Get`과 `loadObjStm`은 기존 동작을 유지한다. walker는 decoded buffer를 detector 호출 하나에만 전달하고 다음 stream 전에 reference를 nil로 만들어 동시에 하나만 보유한다. 64MiB+1 inflate fixture, xref stream+object stream+metadata stream이 합계 cap을 넘는 fixture와 40MiB 두 개/64MiB cumulative fixture가 각각 hard envelope error 또는 `decoded_stream_limit`, `decoded_stream_total_limit`과 `Complete:false`를 만드는지 검증한다.

- [ ] **6. live graph walker 구현**

queue item은 object number, depth, location kind와 page number를 가진다. object와 edge counter를 증가시키기 전에 overflow와 limit을 검사하고 cycle은 visited object로 차단한다. walker는 public `Doc.Get` 대신 `resolveAuditChecked`를 사용해 compressed object가 cumulative decoder를 우회하지 못하게 한다. checked resolver 실패는 Complete=false와 unresolved_object 또는 해당 decoded-stream limitation을 추가한다.

- [ ] **7. 전체 fixture matrix RED 테스트 작성**

각 row는 별도 subtest이고 exact stable code/fact/limitation을 assert한다.

| fixture | 필수 assertion |
|---|---|
| classic xref | `xref.kinds=[classic]`, sections 1, revisions 1 |
| xref stream + object stream | `xref.kinds=[stream]`, compressed live object finding, 두 decode가 같은 cumulative counter에 포함됨 |
| hybrid xref | `xref.kinds=[hybrid]`, sections 2, revisions 1 |
| repeated `/Prev` cycle | `repeatedPrev`, `prevCycle`, `xref_prev_cycle`, incomplete |
| malformed previous xref | successfully indexed section/revision count, `malformedPrev`, `xref_prev_malformed`, incomplete; latest section 자체 malformed는 fatal |
| Info/XMP/Page metadata | `metadata.info`, `metadata.xmp`, `metadata.piece_info`, `metadata.last_modified`, `metadata.thumbnail`, `metadata.trailer_id` |
| OpenAction/AA/name tree/Next | `action.open_action`, `action.additional_action`, `action.javascript`; cycle은 `action_cycle` |
| non-JS actions | Launch, SubmitForm, ImportData, GoToR, Rendition, RichMedia, URI 각각 대응 stable code |
| attachment graph | embedded name tree, Filespec, EmbeddedFile, FileAttachment, Collection 각각 대응 stable code와 dedupe count |
| inherited field/widget | `form.acroform`, `form.field`, `form.widget`, inherited `fieldType` |
| XFA stream/packet array | 두 형태 모두 `form.xfa`, payload 미포함 |
| signatures | valid, negative, out-of-range, overlap, current-file, trailing-revision ByteRange와 DocMDP code |
| supported encryption | Standard handler, V/R/key bits/crypt filter/EncryptMetadata/raw P/permission facts, inspected |
| password omitted | encryption finding + `encrypted_password_required`, incomplete, content graph 미순회 |
| explicit empty password | `Password:&empty`와 `PasswordProvided:true`; empty-password fixture full inspection |
| wrong password | `errors.Is(err, ErrWrongPassword)`이며 password text 미포함 |
| unsupported handler | `encryption.unsupported_handler`, `encrypted_handler_unsupported`, incomplete, error 아님 |
| every budget | xref/object/edge/depth/token/누적 parsed string/page/tree/finding/field/report/개별 decoded/누적 decoded 각각 stable limitation 또는 hard input error |

- [ ] **8. metadata/action/attachment/form detector 구현**

각 detector는 payload를 decode하지 않고 dictionary key, object number, encoded stream length와 bounded filename만 finding으로 만든다. 동일 code/object/page는 count로 집계한다.

- [ ] **9. signature raw-offset RED fixture 작성**

builder로 `/FT /Sig`, `/ByteRange`, `/Contents <00>`, `/Perms /DocMDP`를 가진 문서를 만든다. parsed `String`에는 source offset이 없으므로 `scanSignatureSyntax`가 xref type-1 object의 raw bounds 안에서 comment, literal string, hex string과 nested dictionary를 구분해 `/Contents` token delimiter offset을 찾도록 RED test를 작성한다. scanner는 object당 최대 8MiB만 읽고 type-2 object stream 또는 cap 초과에서는 `signature_contents_unlocatable` limitation을 반환한다. valid, negative, out-of-range, overlap, gap mismatch, current file 끝, trailing revision을 table test로 검증한다.

- [ ] **10. signature·encryption detector 구현**

ByteRange는 정확히 4개 non-negative integer, ordered/non-overlap, file bounds와 gap 구조를 검사한다. encryption finding은 Filter/V/R/Length/EncryptMetadata/P와 inspection 가능 여부만 기록한다.

- [ ] **11. password pointer와 incomplete RED/GREEN**

`Protect(classicPDF(), "pw", "")`에 대해 `Password:nil`은 `password_required`, `Password:&pw`는 full scan, `Password:&wrong`은 `ErrWrongPassword`다. 별도 empty-user-password fixture에 `Password:&empty`를 주어 full scan하고 nil과 구분한다. unsupported handler는 partial report이며 wrong password error와 같은 경로를 사용하지 않는다.

- [ ] **12. budget·payload·schema canary RED/GREEN**

xref/object/edge/depth/token/누적 parsed string/page/tree/finding/field/report/개별 decoded/누적 decoded limit 각각이 Complete=false 또는 hard input error로 귀결되는 test를 작성한다. 모든 emitted code/category/severity/evidence/status가 allowlist에 속하고 `AuditFindingDetails` JSON에 정의되지 않은 key가 없음을 검사한다. serialized report에서 JavaScript source, attachment canary, password와 O/U/OE/UE/Perms bytes가 검출되지 않으며 JSON 길이가 1MiB 이하인지 검사한다.

- [ ] **13. core 검증**

~~~sh
go test ./pdf -run 'TestAuditSecurity|TestSecurityAudit' -count=1
go test -race ./pdf -run 'TestAuditSecurity|TestSecurityAudit' -count=1
go vet ./pdf
~~~

예상: 모두 exit 0.

- [ ] **14. 작업 보고 기록**

.superpowers/sdd/security-audit-task-2-report.md에 stable code 목록, limit 증거와 테스트 결과를 기록한다. commit/push는 하지 않는다.

### 작업 3: TinyGo WASM bridge와 Worker 계약

**파일:**

- 생성: wasm/securityaudit/main.go
- 생성: wasm/securityaudit/protocol.go
- 생성: wasm/securityaudit/session.go
- 생성: wasm/securityaudit/session_test.go
- 수정: web/app.js
- 수정: web/wasm-client.mjs
- 수정: web/wasm-client.test.mjs
- 생성: web/wasm-chunks.mjs
- 생성: web/wasm-chunks.test.mjs
- 생성: web/securityaudit/index.html
- 생성: web/securityaudit/protocol.mjs
- 생성: web/securityaudit/protocol.test.mjs
- 생성: web/securityaudit/securityaudit.mjs
- 수정: tools/operation-catalog.json
- 테스트: tests/worker-contract.test.mjs

**인터페이스:**

WASM global은 객체 request 하나만 받고 설계의 `SecurityAuditWireResponse`를 `{json:string}`으로 반환한다.

~~~text
pdfRun({command:"sourceStart", revision, size})
pdfRun({command:"sourceChunk", revision, offset, data})
pdfRun({command:"sourceFinish", revision})
pdfRun({command:"audit", revision, reportRevision, passwordProvided, password?})
pdfRun({command:"abort", revision})
~~~

~~~go
const maxSourceChunkBytes = 1 << 20

type auditSession struct {
    revision       uint64
    reportRevision uint64
    state          string
    source         []byte
    received       uint64
    chunkCopies    uint64
    peakChunkBytes uint64
}

func (s *auditSession) SourceStart(revision, size uint64) SecurityAuditWireResponse
func (s *auditSession) ChunkWindow(revision, offset, size uint64) ([]byte, SecurityAuditWireResponse)
func (s *auditSession) CommitChunk(size uint64) SecurityAuditWireResponse
func (s *auditSession) SourceFinish(revision uint64) SecurityAuditWireResponse
func (s *auditSession) Audit(revision, reportRevision uint64, password *string) SecurityAuditWireResponse
func (s *auditSession) Abort(revision uint64) SecurityAuditWireResponse
~~~

공유 client interface는 다음 option을 생산한다.

~~~js
createWasmClient(operation, {
  worker,
  requireWorker: true,
  transferOwnership: true,
})
~~~

- shared chunk helper interface는 다음으로 고정한다.

~~~js
export const WASM_BRIDGE_CHUNK_BYTES = 1024 * 1024;
export const WASM_SOURCE_UPLOAD_COMMANDS = Object.freeze({
  start: "sourceStart", chunk: "sourceChunk", finish: "sourceFinish", abort: "abort",
});
export async function uploadBlobChunks({ blob, start, commands = WASM_SOURCE_UPLOAD_COMMANDS, run, signal }) {}
export async function* readOutputChunks({ outputRevision, run, signal }) {}

export function createSecurityAuditProtocol({ run, chunkBytes = WASM_BRIDGE_CHUNK_BYTES }) {
  return {
    async upload(blob, sourceRevision, signal) {},
    async audit({ sourceRevision, reportRevision, passwordProvided, password, signal }) {},
    async abort(sourceRevision) {},
    dispose() {},
  };
}
~~~

`createSecurityAuditProtocol`은 Worker의 `SecurityAuditWireResponse`를 검증하고 shared helper용 `run(command,payload)` adapter로 바꾼다. adapter는 helper의 `uploadRevision`을 Security wire의 `revision`에 매핑하고 start/chunk/finish 응답을 `{uploadRevision,nextOffset,maxChunkBytes}`로 normalize한다. raw `{json}` parse, `ok:false` product error와 transport reject를 섞지 않는다.

- [ ] **1. state machine과 memory counter RED 테스트 작성**

`auditSession` test는 빈/256MiB 초과 start, 1MiB+1 chunk, non-sequential offset, stale/non-increasing source revision, finish 전 audit, incomplete finish, non-increasing report revision을 거부한다. audit은 실행 중 `auditing`, success/wrong-password product error 뒤 `ready`로 돌아온다. 2.5MiB fixture를 1MiB/1MiB/0.5MiB로 `ChunkWindow`의 반환 destination에 직접 `copy`하고 `CommitChunk`한 뒤 다음을 assert한다.

~~~go
if got.SourceBytes != 5<<19 || got.ReceivedBytes != 5<<19 {
    t.Fatalf("bytes = %#v", got)
}
if got.ChunkCopies != 3 || got.PeakChunkBytes != 1<<20 || got.RetainedChunkBytes != 0 {
    t.Fatalf("transport = %#v", got)
}
~~~

`Abort`는 receiving/ready/auditing state에서 idempotent하고 source 길이, received, password/report/parser reference를 0/nil로 만든다. session은 password를 field에 저장하지 않으며 `Audit` 호출의 local `*string`으로만 전달한다.

- [ ] **2. RED 확인**

실행: go test ./wasm/securityaudit -count=1

예상: package 또는 auditSession이 없어 실패한다.

- [ ] **3. session GREEN과 direct JS→Go copy 구현**

`SourceStart`는 더 큰 revision에서 기존 source/parser/report를 해제한 뒤 선언 size의 Go source slice를 한 번만 할당한다. `ChunkWindow`는 offset/size를 검증한 뒤 최종 `source[offset:offset+size]`를 반환하고 별도 payload slice를 만들지 않는다. js/wasm `main.go`의 `sourceChunk` branch는 다음 복사만 수행한다.

~~~go
dst, response := session.ChunkWindow(revision, offset, uint64(data.Get("byteLength").Int()))
if !response.OK { return jsu.JSONOut(response, nil) }
copied := js.CopyBytesToGo(dst, data)
if copied != len(dst) {
    return jsu.JSONOut(session.Abort(revision).withError("internal"), nil)
}
return jsu.JSONOut(session.CommitChunk(uint64(copied)), nil)
~~~

`main.go`는 `sourceChunk`에서 `jsu.Bytes`를 호출하지 않는다. `audit` branch는 `passwordProvided:false`일 때 password property 존재를 거부하고 nil을 전달한다. true일 때 property 존재와 UTF-8 1,024-byte cap을 검사하며 빈 string pointer를 보존한다. audit 반환 직후 local password pointer를 nil로 만든다.

- [ ] **4. ownership-transfer/required-Worker client RED 테스트 작성**

`web/wasm-client.test.mjs`에 다음 behavioral case를 추가한다.

- `transferOwnership:true`는 원래 `ArrayBuffer`를 transfer list에 넣고 typed array byte identity를 유지하며 동일 buffer view 둘은 transfer list에 한 번만 넣는다.
- completed `sourceStart`, `sourceChunk`, `sourceFinish`, `audit`가 Worker instance 하나를 재사용한다.
- active command 중 새 command는 첫 Promise를 AbortError로 reject하고 Worker를 terminate하며 새 Worker를 만든다.
- `requireWorker:true`에서 Worker 부재, constructor throw, load error message, `onerror`, `onmessageerror`, synchronous `postMessage` throw 각각 fallback call count 0이다.
- cancel 또는 transport failure로 Worker를 terminate한 reject error는 `workerGone === true`이고 product error에는 이 marker가 없다.
- `{type:"done", result:{json:"...product error..."}}`는 Worker를 종료하지 않고 다음 command가 같은 instance에서 성공한다.
- default option의 기존 copy/fallback test는 그대로 통과한다.

- [ ] **5. client GREEN 구현**

`copyValue`에 ownership mode와 buffer `Set`을 전달한다. ownership mode에서는 ArrayBuffer/view를 복사하지 않고 원래 buffer를 dedupe해 transfer list에 넣는다. exact backing buffer가 아닌 subview는 전체 backing buffer를 뜻밖에 detach하지 않도록 거부한다. `getWorker`는 constructor error를 포착하고 `onmessageerror`를 등록한다. required mode에서는 Worker를 얻지 못하거나 Worker command가 transport/load error로 reject되면 `fallback`을 호출하지 않는다. cancel/transport failure는 `workerGone=true`를 붙이고 pending reject와 terminate는 한 번만 수행한다. 완료된 command의 `active`만 null로 만들고 idle Worker는 유지한다.

- [ ] **6. shared 1MiB input/output helper RED/GREEN**

`uploadBlobChunks`의 `run(command,payload)` fake는 start command를 normalized `{uploadRevision,nextOffset:0,maxChunkBytes}`로 응답한다. 2.5MiB Blob이 offset 0, 1MiB, 2MiB 순서와 1MiB, 1MiB, 0.5MiB 크기로 default `sourceStart/sourceChunk/sourceFinish`에 전송되는지, custom `pageStart/pageChunk/pageFinish`도 같은 backpressure/metrics를 쓰는지, `start`의 tool-specific revision/stem/limits가 보존되는지, `run` 동시 실행 수가 최대 1인지, 각 ArrayBuffer가 ownership transfer 뒤 detach되는지 검증한다. 반환 metric은 `sourceBytes:2.5MiB`, `chunks:3`, `peakInFlightBytes:1MiB`, `retainedBytes:0`, `detachedChunks:3`이다. fake Blob의 top-level `arrayBuffer()`는 throw하게 두어 호출되지 않음을 증명한다. application error/cooperative abort는 configured abort command를 한 번 호출하고 transport `workerGone`은 호출하지 않으며 retained 0이어야 한다.

`readOutputChunks` test는 매번 `run("outputRead",{outputRevision,maxBytes:WASM_BRIDGE_CHUNK_BYTES})`를 호출해 2.5MiB output을 같은 세 chunk로 yield하며 결합 buffer를 만들지 않고, success/application error/cooperative abort/consumer early-return 모두 `outputRelease`를 정확히 한 번 호출하는지 검증한다. transport failure의 `workerGone:true`에서는 새 Worker를 만들 수 있는 release를 건너뛴다. 이 helper는 Security report에는 사용하지 않지만 후속 Scan/Smart의 chunked output contract가 그대로 import한다.

- [ ] **7. shared boot option wiring과 catalog marker 추가**

`web/app.js`의 `boot(wasmFile, clientOptions={})`가 options를 `createWasmClient`에 전달하고 기존 `window.runWasm`을 유지한다. 반환값은 기존 `ready` Promise 자체에 non-enumerable `{run,cancel,dispose}` method를 붙인 `Promise<void> & WasmBootHandle`이다. 따라서 `md2pdf`, `hwpx2pdf`, `txt2pdf`, `docx2pdf`, `hwp2pdf`의 `.then/.catch`와 OCR의 `await window.boot(...)`가 byte-for-byte 동작을 유지한다. `/securityaudit/`은 augmented Promise를 handle로 보관하고 `run`을 protocol에 주입한다. `dispose`는 ready 전 호출도 기억해 초기화 직후 Worker를 종료하고, active/idle Worker를 종료한 뒤 다음 `run`은 같은 config로 새 Worker를 만들 수 있어 새 입력을 재ingest할 수 있다. `requireWorker`에서는 fallback closure가 호출되지 않으므로 main-thread `ensureMainRuntime`과 `Go` runtime은 instantiate되지 않는다.

~~~js
window.securityAuditWasm = boot("./securityaudit.wasm", {
  requireWorker: true,
  transferOwnership: true,
});
~~~

source selection은 monotonic revision으로 shared uploader의 `sourceStart`, `sourceChunk`, `sourceFinish`를 순차 await한다. source page는 Task 4에서 확장할 최소 PDF input, Run/Cancel/status element와 module script를 가진다.

~~~json
{"id":"securityaudit","engine":"wasm","entry":"/securityaudit/securityaudit.wasm","input":{"kind":"pdf","min":1,"max":1},"output":{"kind":"json"},"capabilities":{"preview":true,"pipeline":false,"batch":false,"terminal":true},"runtime":{"worker":"required","stateful":true,"chunkedIO":true},"build":{"package":"./wasm/securityaudit","output":"web/securityaudit/securityaudit.wasm"}}
~~~

- [ ] **8. Worker contract worker-only branch RED/GREEN**

`tests/worker-contract.test.mjs`는 catalog runtime marker로 branch한다.

~~~js
const workerOnly = descriptor.runtime?.worker === "required";
if (workerOnly) {
  assert.equal(descriptor.runtime.stateful, true);
  assert.equal(descriptor.runtime.chunkedIO, true);
  assert.match(source, /boot\s*\([\s\S]*requireWorker:\s*true/);
  assert.match(source, /requireWorker:\s*true/);
  assert.match(source, /transferOwnership:\s*true/);
} else {
  // 기존 boot/await runWasm 검사를 그대로 실행한다.
}
~~~

별도 behavioral test가 기존 `.then/.catch` 5개 page와 `await boot`가 계속 resolve/reject되는지, boot handle의 `sourceStart → sourceChunk → sourceFinish → audit → abort` 순차 forwarding과 같은 Worker identity, ready 전/idle `dispose` terminate, dispose 뒤 다음 run의 fresh Worker, command error의 non-fatal 재사용, load/error/messageerror에서 non-fallback과 main-thread instantiate count 0을 검증한다. 정적 contract는 runtime behavior의 대체물이 아니라 catalog/page wiring 검증으로만 사용한다.

- [ ] **9. TinyGo와 focused contract 검증**

~~~sh
go test ./wasm/securityaudit -count=1
node --test web/wasm-client.test.mjs web/wasm-chunks.test.mjs web/securityaudit/protocol.test.mjs tests/worker-contract.test.mjs
GOOS=js GOARCH=wasm go build ./wasm/securityaudit
tinygo build -target wasm -no-debug -o /tmp/securityaudit.wasm ./wasm/securityaudit
wasm-opt -Oz /tmp/securityaudit.wasm -o /tmp/securityaudit.opt.wasm
wc -c < /tmp/securityaudit.opt.wasm
JOBS=1 ./build.sh
wc -c < web/securityaudit/securityaudit.wasm
~~~

예상: build 모두 exit 0, optimized binary가 2MiB 미만이다.

- [ ] **10. 작업 보고 기록**

.superpowers/sdd/security-audit-task-3-report.md에 actual byte size, state transition, Worker non-fallback matrix, chunk copy/retained counter와 명령 결과를 기록한다. commit/push는 하지 않는다.

### 작업 4: 안전한 보고서 UI와 recipe

**파일:**

- 수정: web/securityaudit/index.html
- 수정: web/securityaudit/securityaudit.mjs
- 수정: web/securityaudit/protocol.mjs
- 테스트: web/securityaudit/protocol.test.mjs
- 생성: web/securityaudit/report.mjs
- 생성: web/securityaudit/report.test.mjs
- 생성: web/securityaudit/recipe.mjs
- 생성: web/securityaudit/recipe.test.mjs
- 생성: web/securityaudit/securityaudit.css
- 생성: tests/e2e/securityaudit.spec.mjs

**인터페이스:**

~~~js
export function normalizeAuditReport(value) {}
export function normalizeAuditWireResponse(value) {}
export function auditSummaryRows(report, labels) {}
export function recipeForFindings(report, routes, labels) {}
export function buildAuditDownloadWrapper({ sourceRevision, reportRevision, fileName, wire }) {}
export function canDownloadAudit({ currentSourceRevision, currentReportRevision, accepted }) {}
~~~

- [ ] **1. report normalization RED 테스트 작성**

~~~js
test("rejects an unsafe or oversized audit report", () => {
  assert.throws(() => normalizeAuditReport({ schemaVersion: "", findings: [] }), /schema/i);
  assert.throws(() => normalizeAuditReport({
    schemaVersion: "security-audit-v1",
    complete: true,
    limitations: [],
    findings: Array.from({ length: 1001 }, () => ({ code: "x" })),
  }), /finding/i);
});

test("download wrapper binds source and report revisions", () => {
  const wrapper = buildAuditDownloadWrapper({
    sourceRevision: 7,
    reportRevision: 3,
    fileName: "document.pdf",
    wire: validAuditWireResponse(),
  });
  assert.equal(wrapper.schemaVersion, "security-audit-download-v1");
  assert.equal(wrapper.sourceRevision, 7);
  assert.equal(wrapper.reportRevision, 3);
  assert.deepEqual(wrapper.source, {
    name: "document.pdf",
    bytes: wrapper.report.file.bytes,
    sha256: wrapper.report.file.sha256,
  });
  assert.equal(JSON.stringify(wrapper).includes("sourcePayloadCanary"), false);
});
~~~

- [ ] **2. RED 확인**

실행: node --test web/securityaudit/report.test.mjs web/securityaudit/recipe.test.mjs

예상: module 또는 export가 없어 실패한다.

- [ ] **3. pure report·recipe GREEN 구현**

`normalizeAuditReport`는 `security-audit-v1`, exact subtype field, boolean complete, 64자리 lowercase SHA-256, xref/encryption enum, stable code allowlist, 4KiB string과 1,000 finding/1MiB JSON bound를 다시 검증한다. `normalizeAuditWireResponse`는 `security-audit-worker-v1`, command/state/revision 일치와 typed error만 허용한다. `buildAuditDownloadWrapper`는 `security-audit-download-v1` exact schema 외 key를 만들지 않고 source bytes/hash를 report에서 가져온다. `recipeForFindings`는 명세 순서의 non-executing route descriptor만 반환하고 HTML string을 만들지 않는다.

- [ ] **4. source page 작성**

page는 PDF dropzone, `password로 다시 검사` checkbox, checkbox가 켜질 때만 활성화되는 password input, Run/Cancel, progress live region, completeness banner, category filter, finding list, recipe와 JSON download를 가진다. checkbox off는 `passwordProvided:false`, on+empty input은 explicit empty password다. 모든 asset path는 `/securityaudit/` 또는 root absolute다. `securityaudit.mjs`는 Task 3의 protocol과 `window.securityAuditWasm` handle을 사용하고 HTML은 worker-required boot options를 한 번만 설정한다.

- [ ] **5. orchestration RED E2E 추가**

malicious filename과 canary PDF를 넣고 DOM에 image/script node가 생기지 않으며 text만 표시되는 테스트를 먼저 작성한다. input 변경 뒤 old JSON download가 disabled되고 pre-abort/pagehide가 Worker를 종료하는 테스트를 포함한다. download JSON이 exact wrapper schema/revision/hash를 가지며 original PDF bytes, password, JavaScript/attachment canary를 포함하지 않는지 검사한다.

- [ ] **6. orchestration 구현**

boot가 만든 required-Worker client를 source revision별로 소유하고 File reference, AbortController, source/report revision을 사용한다. 각 run은 `{owner,handle,cleanupPromise}` local record를 캡처하고 새 source는 이전 `cleanupPromise`를 await한 뒤에만 시작한다. old catch/finally는 `owner === currentOwner`인 경우에만 shared handle에 `abort`/`dispose`를 적용하며, owner가 바뀌었거나 `workerGone`이면 fresh Worker에 cleanup command를 보내지 않는다. 이 generation race를 delayed abort/dispose fake로 재현해 old cleanup이 새 Worker identity를 terminate하지 않는지 검증한다.

`uploadBlobChunks`로만 source를 넣으며 `File.arrayBuffer()`를 호출하지 않는다. Run은 password input의 존재와 무관하게 항상 `passwordProvided` boolean을 명시하고, 사용자가 password 시도를 선택한 경우 빈 value도 property로 보낸다. unencrypted/full audit 성공은 report normalize/accept 뒤 owned `abort` best effort와 `dispose`로 Worker를 종료한다. password-required/wrong-password일 때만 retry 동안 source Worker를 유지한다. input 변경·새 실행·취소·pagehide도 owned cleanup 뒤 File/report/password reference를 해제한다. JSON download는 current source/report revision과 report SHA-256/bytes가 accepted state와 모두 일치할 때만 enabled다.

- [ ] **7. 실제 TinyGo copy/retained counter E2E 추가**

`tests/e2e/securityaudit.spec.mjs`가 2.5MiB Blob을 `createSecurityAuditProtocol`로 실제 optimized audit WASM Worker에 `sourceStart/sourceChunk/sourceFinish`하고 audit 전 `abort`한다. main thread에서 `File.prototype.arrayBuffer`는 throw하게 두되 slice Blob의 `arrayBuffer`는 허용한다. 다음 exact assertion을 둔다.

~~~js
expect(result.browser).toEqual({
  sourceBytes: 5 << 19,
  chunks: 3,
  peakInFlightBytes: 1 << 20,
  retainedBytes: 0,
  detachedChunks: 3,
});
expect(result.worker).toMatchObject({
  sourceBytes: 5 << 19,
  receivedBytes: 5 << 19,
  chunkCopies: 3,
  peakChunkBytes: 1 << 20,
  retainedChunkBytes: 0,
});
~~~

test는 각 command 사이 Worker identity가 동일하고 browser send concurrency가 1인지 protocol instrumentation으로 확인한다. 이는 logical call order만 세는 test가 아니라 실제 transferred buffer detachment와 TinyGo `js.CopyBytesToGo`가 갱신한 counter를 함께 검증한다.

- [ ] **8. locale·offline UI 테스트 추가**

한국어 URL에서 completeness, recipe와 button 번역을 확인하고 첫 온라인 load 뒤 offline page+WASM 실행 smoke를 추가한다.

- [ ] **9. focused 검증**

~~~sh
node --test web/securityaudit/*.test.mjs web/wasm-client.test.mjs web/wasm-chunks.test.mjs
JOBS=1 ./build.sh
npx playwright test tests/e2e/securityaudit.spec.mjs --project=chromium --project=firefox --project=webkit --project=mobile-chromium --project=mobile-webkit
node --check web/securityaudit/securityaudit.mjs
~~~

예상: 모두 exit 0.

- [ ] **10. 작업 보고 기록**

.superpowers/sdd/security-audit-task-4-report.md에 DOM 안전성, wrapper revision, stale/offline, actual bridge copy/retained-byte counter 결과를 기록한다. commit/push는 하지 않는다.

### 작업 5: 다국어·목록·문서·전체 통합

**파일:**

- 수정: web/index.html
- 수정: tools/meta-i18n.json
- 수정: web/i18n/ja.js
- 수정: web/i18n/zh.js
- 수정: web/i18n/es.js
- 수정: web/i18n/fr.js
- 수정: web/i18n/de.js
- 수정: tests/operation-catalog.test.mjs
- 수정: tests/release-integration.test.mjs
- 수정: README.md
- 수정: README.ko.md
- 수정: web/llms.txt
- 수정: web/manifest.webmanifest
- 생성물: web/{ko,ja,zh,es,fr,de}/securityaudit/index.html
- 생성물: web/sitemap.xml

**완료 후 수치:** catalog 43개, WASM 38개, visible tool 42개

- [ ] **1. count와 translation coverage RED 테스트 수정**

catalog expected count를 43/38로 바꾸고 visible count를 catalog에서 계산해 landing/README/llms/manifest가 42를 사용해야 함을 먼저 실패시킨다. Security descriptor의 runtime object가 exact `{"worker":"required","stateful":true,"chunkedIO":true}`인지 검사한다. translation scanner를 hard-coded module 목록 대신 visible page의 module src와 shared runtime module 목록에서 English key를 수집하도록 일반화한다.

- [ ] **2. landing·metadata·문서 GREEN 수정**

Security Audit card, ItemList JSON-LD, 7-language metadata, README 두 언어, llms와 manifest count를 추가·수정한다. 설명은 구조 감사이며 malware/signature 진위 검사가 아님을 명시한다.

- [ ] **3. 다섯 dictionary 채우기**

source HTML과 securityaudit modules의 English key를 release coverage가 0 missing으로 확인할 때까지 정확히 번역한다.

- [ ] **4. generator와 build copy 실행**

~~~sh
cp tools/operation-catalog.json web/operation-catalog.json
node tools/gen-i18n.mjs
~~~

예상: 0 missing I18N keys encountered, generated page 수 264.

- [ ] **5. 전체 focused 검증**

~~~sh
go test ./... -count=1
go test -race ./pdf ./wasm/securityaudit -count=1
go vet ./...
node --test tests/operation-catalog.test.mjs tests/release-integration.test.mjs tests/worker-contract.test.mjs web/wasm-client.test.mjs web/wasm-chunks.test.mjs web/securityaudit/*.test.mjs
GOOS=js GOARCH=wasm go build ./wasm/...
tinygo build -target wasm -no-debug -o /tmp/securityaudit.wasm ./wasm/securityaudit
wasm-opt -Oz /tmp/securityaudit.wasm -o /tmp/securityaudit.opt.wasm
wc -c < /tmp/securityaudit.opt.wasm
JOBS=1 ./build.sh
node tools/check-wasm-size.mjs web
npx playwright test tests/e2e/securityaudit.spec.mjs --project=chromium --project=firefox --project=webkit --project=mobile-chromium --project=mobile-webkit
git diff --check
~~~

예상: 모두 exit 0, i18n warning 0, 개별 WASM 2MiB 미만·전체 gate 통과, Chromium/Firefox/WebKit/mobile-chromium/mobile-webkit에서 보안·stale·chunk counter scenario 통과.

- [ ] **6. 최종 작업 보고 기록**

.superpowers/sdd/security-audit-task-5-report.md에 count, build size, test 수와 남은 명시적 한계를 기록한다. commit/push는 하지 않는다.

## 계획 자체 검토

- 명세의 여섯 finding 범주, password nil/empty/non-empty, completeness, payload 비노출, recipe와 page/개별·누적 decoded stream을 포함한 모든 budget이 작업 1~5에 연결된다.
- 작업 1의 공통 `ParseBounded/GetBounded/ResolveBounded/PagesBounded`가 xref/object/container/token/decoded/page allocation을 선제 제한하며 Scan/Smart가 재사용할 exact API와 legacy public behavior 보존 test가 있다.
- parser/WASM/UI interface 이름은 `AuditSecurity`, `SecurityAuditReport`, `sourceStart/sourceChunk/sourceFinish/audit/abort`, `normalizeAuditReport`로 일관된다.
- 대용량 입력은 File backing + Go source + 최대 1MiB chunk이며 ownership transfer가 JS→Go copy를 제거한다는 주장을 하지 않는다. 실제 TinyGo/browser counter와 detachment test가 작업 4에 있다.
- worker-only catalog branch, required Worker non-fallback과 five-project browser 실행이 작업 3~5에 있다.
- placeholder와 외부 Go dependency가 없다.
- 구현 순서는 parser → core → bridge → UI → release이며 각 단계가 독립 검증 가능하다.
