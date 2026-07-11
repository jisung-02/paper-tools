# PDF 보안 감사 설계

## 목적

`/securityaudit/`에 PDF의 보안·개인정보 관련 구조를 로컬에서 읽기 전용으로 검사하는 도구를 추가한다. 다음 여섯 범주를 필수로 검사한다.

1. Info/XMP 및 페이지 메타데이터
2. JavaScript와 기타 active action
3. 첨부파일과 portfolio
4. AcroForm/XFA와 widget
5. 전자서명·인증 구조
6. 암호화 handler·revision·권한

도구는 PDF를 실행하거나 수정하지 않는다. 결과는 “발견함”, “지원 범위에서 발견하지 못함”, “검사 불완전”을 구분한다. 전체 문서가 안전하다는 boolean이나 문구는 제공하지 않는다.

## 선택한 접근

`pdf` 패키지에 typed, bounded 구조 감사기를 추가하고 TinyGo WASM은 JSON bridge만 담당한다.

- PDF 객체·xref·암호화 의미는 Go에서 검사한다.
- JavaScript나 첨부 payload를 decode하거나 반환하지 않는다.
- Go 감사기는 source slice에서 SHA-256을 계산한다. 브라우저는 그 hash를 표시·wrapper에 결합하고 결과 표시, 필터, JSON 다운로드와 기존 도구로 가는 비실행 recipe만 담당한다.
- PDF.js의 `getJavaScript`나 attachment API는 payload를 materialize할 수 있으므로 authoritative scanner로 사용하지 않는다.
- 기존 `GetInfo`의 loose map에 감사 결과를 섞지 않는다.

## 입력과 사용자 흐름

- 입력은 PDF 한 개이며 최대 256MiB다.
- 기본 페이지 한도는 500, parser-only hard page 한도는 2,000이다.
- 암호화 PDF는 먼저 envelope-only 부분 보고서를 만든다.
- 사용자가 권한 있는 password를 입력하면 기존 `ParseWithPassword` 지원 범위에서만 전체 감사를 다시 실행한다.
- UI는 `password로 다시 검사` checkbox와 password input을 분리한다. checkbox가 꺼지면 미제공, 켜지고 input이 비어 있으면 explicit empty password 시도다.
- password는 DOM value와 현재 Worker session 동안만 보유하고 저장·로그·보고서에 포함하지 않는다. wire의 `passwordProvided`가 `false`이면 Go의 `Password`는 `nil`이고, `true`이면 빈 문자열을 포함해 `*string`으로 전달한다.
- 브라우저는 `File.slice(offset, offset+1MiB).arrayBuffer()`를 한 번에 하나만 읽고 전용 Worker로 ownership-transfer한다. Worker는 청크를 최종 Go source slice에 바로 복사하며 전체 `File.arrayBuffer()`를 만들지 않는다.
- 결과는 요약, 범주 filter, finding 목록, 검사 범위와 제한, 안전 조치 recipe, JSON 보고서 다운로드로 구성한다.
- 파일 제공 문자열은 모두 `textContent`로 표시하고 URL·JavaScript·attachment data를 실행 가능한 DOM으로 만들지 않는다.

## 보고서 모델

Go의 public model은 stable code와 typed field를 사용한다.

```go
type SecurityAuditReport struct {
    SchemaVersion string               `json:"schemaVersion"`
    Complete      bool                 `json:"complete"`
    Limitations   []AuditLimitation    `json:"limitations"`
    File          AuditFileFacts       `json:"file"`
    Encryption    AuditEncryptionFacts `json:"encryption"`
    Summary       AuditSummary         `json:"summary"`
    Findings      []AuditFinding       `json:"findings"`
}

type AuditFileFacts struct {
    Bytes      uint64         `json:"bytes"`
    SHA256     string         `json:"sha256"`
    PDFVersion string         `json:"pdfVersion"`
    Pages      int            `json:"pages"`
    XRef       AuditXRefFacts `json:"xref"`
}

type AuditXRefFacts struct {
    Sections       int      `json:"sections"`
    Revisions      int      `json:"revisions"`
    Kinds          []string `json:"kinds"`
    RepeatedPrev   bool     `json:"repeatedPrev"`
    PrevCycle      bool     `json:"prevCycle"`
    MalformedPrev  bool     `json:"malformedPrev"`
}

type AuditLimitation struct {
    Code      string `json:"code"`
    Scope     string `json:"scope"`
    ObjectNum int    `json:"objectNum,omitempty"`
    Page      int    `json:"page,omitempty"`
    Count     int    `json:"count,omitempty"`
}

type AuditPermissionFacts struct {
    Known            bool `json:"known"`
    Print            bool `json:"print"`
    Modify           bool `json:"modify"`
    Copy             bool `json:"copy"`
    Annotate         bool `json:"annotate"`
    FillForms        bool `json:"fillForms"`
    Accessibility    bool `json:"accessibility"`
    Assemble         bool `json:"assemble"`
    HighQualityPrint bool `json:"highQualityPrint"`
}

type AuditEncryptionFacts struct {
    Present            bool                 `json:"present"`
    Handler            string               `json:"handler"`
    V                  int                  `json:"v"`
    R                  int                  `json:"r"`
    KeyBits            int                  `json:"keyBits"`
    CryptFilters       []string             `json:"cryptFilters"`
    EncryptMetadata    *bool                `json:"encryptMetadata"`
    RawPermissions     int64                `json:"rawPermissions"`
    Permissions        AuditPermissionFacts `json:"permissions"`
    PasswordProvided   bool                 `json:"passwordProvided"`
    ContentInspectable bool                 `json:"contentInspectable"`
    InspectionStatus   string               `json:"inspectionStatus"`
}

type AuditSummary struct {
    Metadata      int `json:"metadata"`
    ActiveContent int `json:"activeContent"`
    Attachments   int `json:"attachments"`
    Forms         int `json:"forms"`
    Signatures    int `json:"signatures"`
    Encryption    int `json:"encryption"`
    Total         int `json:"total"`
    Limitations   int `json:"limitations"`
}

type AuditFindingDetails struct {
    Filter       string `json:"filter,omitempty"`
    SubFilter    string `json:"subFilter,omitempty"`
    ActionType   string `json:"actionType,omitempty"`
    FieldType    string `json:"fieldType,omitempty"`
    Filename     string `json:"filename,omitempty"`
    Coverage     string `json:"coverage,omitempty"`
    Handler      string `json:"handler,omitempty"`
    CryptFilter  string `json:"cryptFilter,omitempty"`
    Permission   string `json:"permission,omitempty"`
    DeclaredSize string `json:"declaredSize,omitempty"`
    MetadataKey  string `json:"metadataKey,omitempty"`
}

type AuditFinding struct {
    Code       string              `json:"code"`
    Category   string              `json:"category"`
    Severity   string              `json:"severity"`
    Evidence   string              `json:"evidence"`
    ObjectNum  int                 `json:"objectNum,omitempty"`
    Page       int                 `json:"page,omitempty"`
    Count      int                 `json:"count"`
    Name       string              `json:"name,omitempty"`
    EncodedLen uint64              `json:"encodedLen,omitempty"`
    Details    AuditFindingDetails `json:"details"`
}
```

`AuditFileFacts`는 Go 표준 `crypto/sha256`으로 입력 slice에서 계산한 lowercase SHA-256을 포함한다. 브라우저가 보고서 식별 hash를 만들기 위해 `File.arrayBuffer()`로 입력 전체를 다시 읽지 않는다.

`SchemaVersion`은 `security-audit-v1`이다. `AuditXRefFacts.Kinds`는 `classic`, `stream`, `hybrid`만, `AuditEncryptionFacts.Handler`는 `none`, `Standard`, `unsupported`만, `InspectionStatus`는 `not_encrypted`, `password_required`, `inspected`, `unsupported_handler`만 사용한다. `Category`는 `metadata`, `active_content`, `attachment`, `form`, `signature`, `encryption`, `Severity`는 `info`, `review`, `high`, `Evidence`는 `trailer_key`, `catalog_key`, `page_key`, `annotation_key`, `field_key`, `action_dictionary`, `name_tree`, `stream_dictionary`, `byte_range` 중 하나다.

stable finding code allowlist는 다음과 같다.

- metadata: `metadata.info`, `metadata.xmp`, `metadata.piece_info`, `metadata.last_modified`, `metadata.thumbnail`, `metadata.trailer_id`
- active content: `action.open_action`, `action.additional_action`, `action.javascript`, `action.launch`, `action.submit_form`, `action.import_data`, `action.goto_remote`, `action.rendition`, `action.rich_media`, `action.uri`
- attachment: `attachment.embedded_files`, `attachment.filespec`, `attachment.embedded_file`, `attachment.file_annotation`, `attachment.collection`
- form: `form.acroform`, `form.field`, `form.widget`, `form.need_appearances`, `form.calculation_order`, `form.xfa`
- signature: `signature.present`, `signature.doc_mdp`, `signature.byte_range_valid`, `signature.byte_range_invalid`, `signature.covers_current_file`, `signature.covers_prior_revision`
- encryption: `encryption.present`, `encryption.standard`, `encryption.unsupported_handler`, `encryption.permissions`

stable limitation code allowlist는 `encrypted_password_required`, `encrypted_handler_unsupported`, `xref_prev_cycle`, `xref_prev_malformed`, `unresolved_object`, `unsupported_filter`, `signature_contents_unlocatable`, `action_cycle`, `tree_cycle`, `object_limit`, `edge_limit`, `depth_limit`, `tree_node_limit`, `page_limit`, `finding_limit`, `field_bytes_limit`, `report_bytes_limit`, `decoded_stream_limit`, `decoded_stream_total_limit`이다. `Scope`는 `file`, `xref`, `object`, `stream`, `action`, `tree`, `report` 중 하나다.

`AuditFindingDetails` 외의 임의 key를 허용하지 않는다. 문자열 값은 항목당 UTF-8 4KiB, 보고서 전체 1MiB를 넘을 수 없다. finding은 최대 1,000개까지 emit하고 동일 code/location은 count로 집계한다. 초과 시 `Complete:false`와 `finding_limit` 제한을 추가한다.

`Complete`는 지원한 live graph 범위를 한도 내에서 모두 순회했음을 의미할 뿐 안전함을 뜻하지 않는다. UI는 다음 문구를 구분한다.

- `검사 완료: 지원 범위에서 발견된 항목을 아래에 표시합니다.`
- `검사 완료: 지원 범위에서 항목을 발견하지 못했습니다. 안전을 보증하지는 않습니다.`
- `검사 불완전: 제한 또는 지원하지 않는 구조 때문에 일부를 확인하지 못했습니다.`

## parser 경계

현재 `Parse`가 `/Encrypt`를 발견하면 `ErrEncrypted`로 종료하는 흐름을 다음처럼 분리한다.

1. 내부 `parseEnvelope`가 latest xref/trailer를 읽은 뒤 unique `/Prev` offset과 `/XRefStm` companion을 추적하고 `Doc.envelopeFacts`에 section 수, revision 수, `classic|stream|hybrid`, repeated offset, cycle, malformed previous section을 보존한다.
2. latest section 자체를 읽을 수 없으면 fatal parse error다. 이전 section이 malformed이면 `Doc.envelopeErr`에 보존해 감사에서는 partial limitation으로 바꾸고 public parse에서는 기존 error로 반환한다.
3. `parseEnvelope`는 `/Root`를 검증하지 않는다. public `Parse`와 `ParseWithPassword`는 먼저 `/Encrypt`를 처리한 뒤 previous-chain error와 `/Root` 부재·잘못된 reference를 검증한다.
4. 따라서 encrypted+missing-root, encrypted+bad-root는 password 미제공 `Parse`에서 계속 `ErrEncrypted`가 우선하고, `ParseWithPassword`는 password 검증 성공 뒤 root error를 반환한다. wrong password는 root error보다 우선한다.
5. public `Parse`의 나머지 기존 동작과 `Doc.Get`의 loose semantics는 유지한다.
6. 감사기는 password 없이 trailer의 Encrypt dictionary만 읽는다.
7. password가 없으면 encrypted object graph를 순회하지 않고 `Complete:false`를 반환한다.
8. `Password != nil`이면 빈 문자열도 실제 password 시도로 취급하고, 기존 security handler로 key를 설정한 뒤 live graph를 순회한다.

`Doc.Get`이 parse/decode 실패를 `nil`로 바꾸는 기존 동작만으로 부재를 판단하지 않는다. 감사 전용 checked resolver가 object 번호와 오류를 반환하며, 도달 가능한 reference 하나라도 해석하지 못하면 `unresolved_object` 제한을 기록한다.

감사기는 현재 xref의 live object graph를 authority로 사용한다. `AuditXRefFacts.Sections`는 hybrid companion을 포함해 성공적으로 읽은 unique physical xref section 수, `Revisions`는 latest startxref와 성공적으로 따라간 main `/Prev` chain의 unique revision 수다. hybrid companion은 revision을 추가하지 않는다. repeated offset/cycle/malformed chain은 typed fact와 limitation을 함께 만들며 `Complete:false`다. 이전 revision의 물리 bytes까지 안전하게 제거됐다고 주장하지 않는다.

## traversal 한도

```go
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
```

Security Audit가 먼저 공통 `pdf/read_limits.go`를 만든다. `ParseBounded`는 resolved limit과 session-wide remaining counter를 `Doc`에 보관하고, `GetBounded`, `ResolveBounded`, `PagesBounded`가 같은 counter를 계속 소비한다. xref map/slice와 revision seen offset은 합쳐 `MaxXRefEntries` 안에서, object-stream pair/cache는 `MaxObjects` 안에서 제한한다. dictionary/array item, name/literal/hex string, recursive frame, inherited page attribute copy, visited page-tree node와 page append도 각각의 allocation 전에 해당 edge/token/depth/object/page limit을 검사한다. `GetBounded`와 `ResolveBounded`는 parse/decode 오류를 `nil`로 숨기지 않는다. `ReadStats`는 retained metadata를 정확한 byte heap으로 가장하지 않고 bounded 항목 수, max observed token, 누적 parsed/decode 양과 peak decoded buffer를 보고한다.

기존 `Parse`, `ParseWithPassword`, `Doc.Get`, `Doc.R`, `Doc.Pages`의 legacy behavior는 유지한다. 감사기는 공통 checked lexer/xref/object-stream primitive와 counter를 사용하되 previous section/object detector 한도를 partial limitation으로 바꾸는 audit envelope adapter를 둔다. 이후 Scan Cleanup과 Smart Split은 public loose API를 호출하지 않고 이 공통 bounded API만 사용한다. source read counter와 output `BoundedGraphLimits` counter는 ceiling 값이 같아도 별도이며 이중 차감하지 않는다.

| 자원 | 한도 |
|---|---:|
| 입력 PDF | 256MiB |
| xref entry | 100,000 |
| indirect object | 100,000 |
| graph edge | 1,000,000 |
| 재귀 depth | 64 |
| 단일 literal/hex/name token | 8MiB |
| 누적 parsed string/name bytes | 64MiB |
| name/field tree node | 각 10,000 |
| finding | 1,000 |
| 표시 문자열 | 항목당 4KiB |
| JSON 보고서 | 1MiB |
| decoded stream | 개별 64MiB, 누적 256MiB |
| password 입력 | UTF-8 1,024 bytes |
| Worker source chunk | 1MiB |
| signature raw-object scan window | 8MiB |

공통 `pdfReadBudget`은 walker가 시작되기 전의 allocation도 제한한다. xref entry는 map/slice에 추가하기 전, indirect object cache는 parse·저장 전, dictionary/array item은 container append 전, literal·hex string과 name은 decode/copy 전에 각각 object/edge/depth/token/누적 parsed-byte budget을 소비한다. lexer는 재귀 호출 전에 depth를 검사하고 cap을 넘는 길이는 payload를 먼저 만들지 않는다. oversized object-stream `/N`은 pair slice capacity를 만들기 전에 거부한다. latest xref/header에서 한도가 나면 report를 만들 수 없는 hard input error이고, 도달 object에서 나면 `Complete:false`와 `object_limit`, `edge_limit`, `depth_limit`, `field_bytes_limit` 중 해당 limitation을 남긴다. public loose API는 기존 behavior를 유지하고 Security/Scan/Smart의 bounded 경로만 이 budget을 받는다.

`SecurityAuditLimits`는 `MaxDecodedStreamBytes`와 `MaxDecodedStreamTotalBytes`를 별도 필드로 가진다. 공통 bounded primitive를 쓰는 audit envelope adapter는 xref stream, hybrid companion, object stream과 detector가 요청한 metadata/text stream을 모두 같은 `Doc` budget에 넣는다. 각 decode는 allocation 전에 `min(개별 잔여, 누적 잔여)`를 기존 bounded filter decoder에 넘기며 성공한 decoded byte만 누적한다. JavaScript와 attachment는 encoded length와 dictionary 정보만 읽으므로 내용 stream을 decode하지 않는다. public `Parse`는 기존 64MiB per-stream 동작을 유지하고 bounded 경로만 cumulative budget을 사용한다. 개별 또는 누적 한도를 넘으면 각각 `decoded_stream_limit`, `decoded_stream_total_limit`과 `Complete:false`다. latest xref 자체를 budget 때문에 열지 못하면 report를 만들 수 없는 hard input error이고, 이전 xref/object/detector stream 한도는 partial limitation이다.

## 대용량 메모리 계약

`transferOwnership:true`는 main thread의 `slice()` 사본과 postMessage structured-clone payload 복사를 제거하지만 JS→Go 복사를 제거하지 않는다. 보안 감사는 이 경계를 다음처럼 고정한다.

1. main thread에는 사용자가 선택한 `File` backing만 장기 보유한다. 전체 입력 `ArrayBuffer`를 만들지 않는다.
2. `sourceStart`가 Worker의 Go heap에 정확히 `size` 길이 source slice 하나를 할당한다.
3. main thread는 순차적으로 최대 1MiB `File.slice`만 읽는다. 각 chunk의 exact-size buffer를 ownership-transfer하고 다음 chunk는 이전 command가 끝난 뒤에만 읽는다.
4. Worker bridge는 `jsu.Bytes`로 중간 Go slice를 만들지 않고 `js.CopyBytesToGo(session.source[offset:offset+n], value)`를 직접 한 번 호출한다. command 종료 뒤 JS chunk reference를 남기지 않는다.
5. 감사 parser와 SHA-256은 같은 Go source slice를 참조한다. report JSON은 1MiB hard cap 안에서만 Go JSON bytes와 JS string이 잠시 함께 존재할 수 있다.
6. raw stream은 source의 subslice를 참조하고 별도 raw payload 사본으로 cache하지 않는다. decoded stream은 한 번에 하나만 보유하고 개별 64MiB 안에서 detector 사용 직후 해제한다. object/string cache는 object·edge·field budget, report는 1MiB budget에 묶인다.
7. unencrypted/full audit 성공 뒤 브라우저는 report를 normalize한 다음 `abort`를 보내고 client를 dispose해 Worker 전체를 종료한다. password-required 또는 wrong-password 상태에서만 사용자가 재시도할 동안 source를 유지한다. Cancel, input 변경, fatal Worker transport error, `pagehide`도 source, parser cache, report, password reference를 해제하고 Worker를 종료한다.

따라서 대용량 입력의 장기 보유 모델은 `File backing + Go source`이고 추가 전송 메모리는 최대 1MiB 한 chunk다. 이를 “무복사”라고 표현하지 않는다. 실제 TinyGo E2E는 Worker가 반환하는 `sourceBytes`, `receivedBytes`, `chunkCopies`, `peakChunkBytes`, `retainedChunkBytes` 계측과 브라우저 upload counter를 비교해 `chunkCopies == ceil(fileBytes/1MiB)`, `peakChunkBytes <= 1MiB`, command 사이 `retainedChunkBytes == 0`, caller buffer detachment, 동시에 하나 이하의 in-flight chunk를 검증한다.

## 필수 finding

### 메타데이터

- trailer `/Info` 존재와 채워진 표준 key 이름
- Catalog와 Page의 `/Metadata` stream 위치·encoded length·filter
- Page `/PieceInfo`, `/LastModified`, `/Thumb`
- trailer `/ID` 존재

Info/XMP 전체 값을 보고서에 복사하지 않는다. 사용자에게 보여야 할 title/author 같은 값은 4KiB로 자르고 control character를 제거한다.

### JavaScript와 active action

다음 root와 generic graph walker를 함께 사용한다.

- Catalog `/OpenAction`
- Catalog·Page·Annotation·Field의 `/AA`
- Catalog `/Names /JavaScript`
- `/S /JavaScript` action과 `/Next` chain
- JavaScript String 또는 Stream의 위치와 encoded length

source code는 반환하지 않는다. `/Launch`, `/SubmitForm`, `/ImportData`, `/GoToR`, `/Rendition`, `/RichMedia`, `/URI`는 별도 code로 보고하고 JavaScript라고 잘못 표시하지 않는다. action chain cycle과 depth 초과는 검사 불완전으로 처리한다.

### 첨부파일

- `/Names /EmbeddedFiles`
- `/Type /Filespec`와 `/EF`
- `/Type /EmbeddedFile` stream
- `/Subtype /FileAttachment` annotation
- Catalog `/Collection`

동일 Filespec/stream을 object 번호로 dedupe한다. `/UF` 또는 `/F` filename은 path separator와 control character를 제거하고 255자로 자른다. `/Params /Size`는 신뢰하지 않는 선언값으로 표시하고 실제 decoded size로 주장하지 않는다.

### Form/XFA

- Catalog `/AcroForm`
- bounded field tree의 field 수, inherited `/FT`, widget 수
- `/NeedAppearances`, `/CO`, field `/AA`
- `/XFA` stream 또는 packet array 존재

Form 자체는 interactive content로 분류한다. XFA와 active field action은 더 강한 review finding으로 구분한다.

### 전자서명

- inherited `/FT /Sig` field
- `/Type /Sig` 또는 `/ByteRange`와 `/Contents`를 가진 signature-like dictionary
- Catalog `/Perms /DocMDP`
- `/SubFilter`, signer-name 존재, signing-time 존재, Contents encoded length

표준 라이브러리 범위에서는 다음만 검증한다.

- ByteRange가 정확히 4개의 non-negative 정수인지
- 두 범위가 정렬·비중첩이고 파일 범위 안인지
- gap이 Contents 위치와 구조적으로 일치하는지
- 범위가 현재 파일 끝까지 덮는지, 이후 incremental bytes가 존재하는지

CMS/PKCS#7 signed attributes, message digest, certificate chain, trust anchor, revocation, TSA는 검증하지 않는다. UI는 `서명 존재`, `ByteRange 구조 정상/비정상`, `현재 파일 전체/이전 revision까지만 범위 포함`만 표시하고 `유효한 서명`이라고 표현하지 않는다.

### 암호화

- Encrypt entry 존재
- handler가 Standard인지 지원하지 않는 handler인지
- V, R, nominal key length, crypt filter 이름
- EncryptMetadata
- raw P와 best-effort permission 해석
- password 제공 여부와 content inspection 가능 여부

O/U/OE/UE/Perms bytes와 password는 반환하지 않는다. 암호화는 접근 제어일 뿐 JavaScript·첨부파일·악성 콘텐츠 제거로 표현하지 않는다.

## 안전 조치 recipe

`web/securityaudit/recipe.mjs`가 finding code를 localized, non-executing 단계로 변환한다.

1. 원본을 보존하고 덮어쓰지 않는다.
2. signature가 있으면 어떤 rewrite보다 먼저 신뢰 가능한 외부 서명 제품으로 진위를 확인한다.
3. encrypted 문서는 권한이 있을 때 복사본을 Unlock하고 다시 감사한다. Unlock을 정리 기능으로 표현하지 않는다.
4. Info/XMP/page metadata는 Metadata의 strip 기능을 안내하고 결과를 재감사한다.
5. annotation/form은 손실을 이해한 경우 Flatten을 안내한다. Flatten이 JavaScript·첨부를 완전히 제거한다고 주장하지 않는다.
6. JavaScript, attachment, XFA, unknown active content 또는 고신뢰 배포본은 verified raster-only 재구축을 안내한다.
7. 정리 후 접근 제어가 필요하면 마지막에 Protect하고 최종 파일을 다시 감사한다.

현재 선택적 JavaScript/attachment remover가 없으면 `자동 정리 기능 없음`을 명시한다. recipe link는 새 탭에서 기존 도구를 열 뿐 파일이나 password를 자동 전달하지 않는다.

## 브라우저 구성

- `wasm/securityaudit`는 한 Worker 안에서 다음 command를 순차 처리하는 상태형 bridge다. 모든 request는 객체 하나이며 command 사이에 같은 Worker를 유지한다.

```text
sourceStart {command, revision:uint64, size:uint64}
sourceChunk {command, revision:uint64, offset:uint64, data:Uint8Array(1..1MiB)}
sourceFinish {command, revision:uint64}
audit {command, revision:uint64, reportRevision:uint64, passwordProvided:boolean, password?:string}
abort {command, revision:uint64}
```

command `revision`은 browser의 `sourceRevision`과 같다. `sourceStart`는 현재보다 큰 non-zero revision만 받고 이전 source/report/parser reference를 먼저 해제한다. `sourceChunk`의 offset은 직전 `receivedBytes`와 정확히 같아야 하며 chunk는 순차 1개만 허용한다. `sourceFinish`는 수신량이 선언 size와 같을 때만 ready 상태로 전이한다. `audit`는 ready 상태와 직전보다 큰 `reportRevision`에서만 허용하고 처리 중 `auditing`, 성공 또는 typed product error 뒤 다시 `ready`가 된다. `passwordProvided:false`와 password property 존재를 동시에 허용하지 않는다. `passwordProvided:true`이면 password property가 반드시 존재하며 `""`도 제공된 password다. stale revision과 잘못된 state는 source를 사용하지 않고 거부한다. `abort`는 source, parser cache, report와 password reference를 해제하며 여러 번 호출해도 안전하다.

모든 command는 `{json:string}` 안에 다음 exact wire envelope를 반환한다.

```go
type SecurityAuditWireResponse struct {
    SchemaVersion  string                 `json:"schemaVersion"` // security-audit-worker-v1
    OK             bool                   `json:"ok"`
    Command        string                 `json:"command"`
    Revision       uint64                 `json:"revision"`
    State          string                 `json:"state"`
    ReceivedBytes  uint64                 `json:"receivedBytes"`
    ReportRevision uint64                 `json:"reportRevision,omitempty"`
    Report         *SecurityAuditReport   `json:"report,omitempty"`
    Transport      *AuditTransportFacts   `json:"transport,omitempty"`
    Error          *SecurityAuditWireError `json:"error,omitempty"`
}

type AuditTransportFacts struct {
    SourceBytes        uint64 `json:"sourceBytes"`
    ReceivedBytes      uint64 `json:"receivedBytes"`
    ChunkCopies        uint64 `json:"chunkCopies"`
    PeakChunkBytes     uint64 `json:"peakChunkBytes"`
    RetainedChunkBytes uint64 `json:"retainedChunkBytes"`
}

type SecurityAuditWireError struct {
    Code       string `json:"code"`
    MessageKey string `json:"messageKey"`
}
```

`AuditTransportFacts.SourceBytes`는 의도적으로 유지하는 최종 Go source 크기다. `RetainedChunkBytes`는 command 반환 뒤 source 밖에 남은 임시 JS/Go chunk bytes만 세며 source bytes를 0으로 가장하지 않는다.

`State`는 `idle`, `receiving`, `ready`, `auditing` 중 하나다. stable wire error code는 `invalid_request`, `invalid_state`, `stale_revision`, `input_empty`, `input_too_large`, `chunk_too_large`, `chunk_offset`, `source_incomplete`, `password_too_long`, `wrong_password`, `malformed_envelope`, `report_too_large`, `internal`이다. 오류에는 password, filename, object payload나 raw parser message를 넣지 않는다.

- `/securityaudit/`은 `window.securityAuditWasm = boot("./securityaudit.wasm", {requireWorker:true, transferOwnership:true})`로 shared persistent client handle을 만든다. handle은 기존 `boot(...).then(...)`와 `await boot(...)`를 보존하는 `Promise<void>`에 `{run,cancel,dispose}`를 추가한 객체다. `requireWorker` 때문에 fallback closure와 main-thread Go runtime은 instantiate되지 않는다. page module은 handle의 `run`으로 command를 순차 await하고 terminal cleanup에서 `dispose`해 idle Worker도 종료한다.
- catalog descriptor는 `"runtime":{"worker":"required","stateful":true,"chunkedIO":true}`를 가진다. worker-contract 검사는 이 marker의 page에서 exact boot options, main-thread instantiate 부재, required Worker와 stateful command behavior를 별도로 검사한다.
- `createWasmClient`의 `transferOwnership:true`는 typed-array의 원래 buffer를 transfer list에 중복 없이 넣고 사본을 만들지 않는다. `requireWorker:true`는 Worker 부재·constructor/load/`error`/`messageerror`/`postMessage` 실패에서 fallback operation을 호출하지 않고 pending command를 reject하고 Worker를 종료한다.
- 완료된 순차 command는 같은 Worker를 재사용한다. 아직 완료되지 않은 command 중 새 command가 들어오는 경우에만 기존 command를 AbortError로 취소하고 Worker를 종료해 state를 폐기한다. wire envelope의 product error는 transport failure가 아니므로 자동 fallback하거나 Worker를 종료하지 않는다.
- shared `web/wasm-chunks.mjs`의 `uploadBlobChunks({blob,start,commands,run,signal})`는 `run(command,payload)` adapter를 사용한다. default `commands`는 `sourceStart/sourceChunk/sourceFinish/abort`이고 Scan raster page는 `pageStart/pageChunk/pageFinish/abort`를 지정한다. helper는 `start`의 tool-specific revision/stem/limits에 `size`를 더해 start command를 호출하고 normalized `{uploadRevision,nextOffset,maxChunkBytes}`를 받은 뒤 File/Blob을 `WASM_BRIDGE_CHUNK_BYTES` 이하 exact-size slice로 읽어 chunk command를 순차 await하고 finish한다. 전체 `blob.arrayBuffer()` 호출을 금지하고 `{sourceBytes,chunks,peakInFlightBytes,retainedBytes,detachedChunks}` main-thread counter를 반환한다.
- 같은 helper의 `readOutputChunks({outputRevision,run,signal})`는 모든 도구에 `run("outputRead", {outputRevision,maxBytes:WASM_BRIDGE_CHUNK_BYTES})`를 보내 응답 한 chunk씩 yield하고 success/application error/cooperative abort에서 `outputRelease`를 한 번 호출하는 async iterable이다. Scan/Smart protocol은 `maxBytes`가 정확히 1MiB인지 검증한다. Security Audit 결과는 1MiB JSON이라 이 output path를 쓰지 않지만 Scan Cleanup과 Smart Split의 대용량 결과가 같은 계약을 재사용한다. Worker가 이미 사라진 transport failure는 `workerGone` marker를 보고 새 Worker를 release 목적으로 만들지 않는다.
- hash는 보고서와 입력을 연결하기 위한 식별값이며 신뢰 서명으로 표현하지 않는다.
- 결과 DOM은 모든 file-derived 값을 `textContent`로 삽입한다.
- JSON download wrapper는 다음 exact schema를 사용한다.

```ts
type SecurityAuditDownload = {
  schemaVersion: "security-audit-download-v1";
  sourceRevision: number;
  reportRevision: number;
  source: {
    name: string;
    bytes: number;
    sha256: string;
  };
  report: SecurityAuditReport;
};
```

브라우저의 `sourceRevision`은 file input 변경마다 증가하고 `reportRevision`은 감사 요청마다 증가한다. Worker는 두 revision을 그대로 응답한다. 다운로드는 accepted result의 source/report revision이 현재 state와 같고 wrapper source bytes/hash가 `report.file`과 일치할 때만 허용한다. hash는 Go 보고서에서 가져오며 브라우저가 전체 파일을 다시 읽지 않는다.

- wrapper의 `source.name`은 path separator와 control character를 제거하고 Unicode code point 255개로 자른 표시용 basename이다.
- full audit 성공은 report를 main thread에 인수한 뒤 Worker를 폐기한다. password-required/wrong-password는 retry 또는 cancel까지 Worker를 유지한다. 새 실행·입력 변경·취소·`pagehide`는 이전 Worker와 report를 폐기한다.
- source generation은 boot handle과 별도의 monotonically increasing owner token을 가진다. 새 source를 시작하기 전에 이전 generation의 cleanup Promise를 await한다. 모든 `abort`/`dispose` finally는 자신이 current owner일 때만 shared handle에 적용하며, owner가 바뀐 old finally가 fresh Worker를 종료하지 못한다. transport failure로 이미 Worker가 사라진 경우에는 `workerGone`을 보고 abort command를 보내지 않는다.

## 오류 처리

- wrong password는 wire `wrong_password` error, unsupported encryption handler는 `encryption.unsupported_handler` finding과 `encrypted_handler_unsupported` limitation을 가진 partial report로 구분한다.
- malformed xref/trailer로 envelope도 읽지 못하면 보고서가 아니라 명시적 실패다.
- 일부 live object만 읽지 못하면 partial report와 `Complete:false`를 제공한다.
- 한도 초과, unsupported filter, cyclic tree, malformed action은 limitation과 가능한 finding을 함께 제공한다.
- JSON serialization budget을 넘으면 finding을 임의로 잘라 `Complete:true`로 만들지 않고 실패 또는 incomplete aggregate를 반환한다.
- Worker `error`, `messageerror`, load failure와 abort에서 fallback하지 않고 file/password/result reference를 해제한다. 별도 wall-clock timeout은 두지 않고 사용자가 명시적으로 Cancel할 수 있게 한다.

## 접근성·다국어·오프라인

- 범주 filter와 finding 목록은 keyboard만으로 사용할 수 있다.
- severity는 색 외에 label과 stable code를 표시한다.
- 진행은 polite live region, 불완전·실패는 alert와 heading으로 전달한다.
- English/Korean source와 ja/zh/es/fr/de dictionary를 제공한다.
- 첫 온라인 로드 후 페이지·WASM·worker가 캐시되면 오프라인 감사가 가능해야 한다.

## 검증

### Go fixture

- classic xref, xref stream, object stream, hybrid xref
- 공통 `ParseBounded/GetBounded/ResolveBounded/PagesBounded`가 xref 100,001, object 100,001, edge 1,000,001, depth 65, token 8MiB+1, parsed string/name 64MiB+1, oversized object-stream `/N`, page 501에서 map/slice/string/decode/page append 전에 실패하고 `ReadStats`가 limit 안임을 검증
- OpenAction·AA·JavaScript name tree·Next cycle
- EmbeddedFiles·Filespec·EmbeddedFile·FileAttachment·Collection
- AcroForm field inheritance·widget·XFA stream/array
- valid/invalid/out-of-range/overlap/trailing-revision ByteRange와 DocMDP
- Info·XMP·PieceInfo·LastModified·Thumb·ID
- supported password, wrong password, unsupported handler, EncryptMetadata와 permission
- cyclic name/field/action graph, unresolved object, object/edge/depth/finding budget
- page limit, 개별 decoded-stream limit, 누적 decoded-stream limit과 report budget
- `/Prev` repeated offset/cycle/malformed chain의 section·revision typed facts
- encrypted+missing-root, encrypted+bad-root, wrong-password+bad-root error precedence
- finding에 JavaScript source·attachment bytes·password material이 포함되지 않음

### JavaScript

- stable code별 recipe와 caveat
- file-derived HTML/script 문자열이 실행되지 않고 text로 표시됨
- 4KiB truncation과 aggregate summary
- stale input에서 JSON download 차단
- `passwordProvided:false`, explicit empty password, non-empty password의 wire 구분
- pre-abort, 빠른 재실행, 같은 Worker의 순차 command, concurrent command 취소, Worker load/error/messageerror와 `pagehide` cleanup
- `requireWorker`에서 fallback 미호출, original buffer detachment, 최대 1MiB in-flight/retained counter

### E2E

- 실제 TinyGo audit WASM으로 여섯 범주 canary 보고
- 암호화 envelope-only와 password 전체 감사
- no-finding completed 문구가 안전 보증을 하지 않음
- malformed/unsupported fixture의 incomplete banner
- JSON report hash와 schema, 원본 payload 미포함
- 실제 TinyGo transport counter와 browser counter가 동일한 chunk 수·peak·retained 0을 보고함
- 7개 언어 generated page와 Chromium·Firefox·WebKit·mobile-chromium·mobile-webkit 프로젝트

## 제외 범위

- CMS/PKCS#7 서명 진위와 certificate trust 검증
- 바이러스·악성코드 탐지
- JavaScript 의도 분석 또는 sandbox 실행
- attachment payload 해제·검사
- 모든 ISO 32000 구조를 지원한다는 주장
- selective sanitizer 자동 실행
- server 업로드나 원격 reputation 조회

## 완료 조건

1. 여섯 필수 범주가 bounded live-graph scan으로 보고된다.
2. encrypted 문서는 password 없이는 partial, 지원 password가 있으면 전체 검사로 구분된다.
3. unresolved/unsupported/limit 상황이 `Complete:false`로 드러난다.
4. signature는 구조와 coverage만 보고하고 진위라는 표현을 사용하지 않는다.
5. payload·password·JavaScript source가 보고서나 DOM에 노출되지 않는다.
6. recipe는 현재 도구의 실제 보장보다 강한 제거 주장을 하지 않는다.
7. 모든 데이터 처리가 로컬이고 Go 외부 모듈, commit, push를 추가하지 않는다.
8. 입력 ingest가 File backing + Go source + 최대 1MiB chunk 계약과 실제 copy/retained-byte 계측을 만족한다.
9. worker-required stateful page는 어떤 Worker failure에서도 main-thread fallback하지 않는다.
10. 공통 bounded reader가 source xref/object/container/token/decode/page-tree allocation을 선제 제한하고 Scan/Smart가 loose public parser를 우회하지 못한다.
