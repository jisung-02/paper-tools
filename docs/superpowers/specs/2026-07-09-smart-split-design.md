# PDF 스마트 분할 설계

## 목적

`/smartsplit/`에 PDF를 규칙으로 분석하고 사용자가 결과 계획을 검토한 뒤 여러 PDF로 분할하는 도구를 추가한다. 첫 완성 범위는 다음 네 규칙을 모두 제공한다.

1. 고정 페이지 수
2. 최상위 로컬 북마크
3. 선택 가능한 텍스트 정규식
4. 검토된 빈 separator 페이지

브라우저는 원본 `File`을 전체 `ArrayBuffer`로 만들지 않고 1MiB 순차 chunk로 Worker에 보낸다. Worker는 chunk를 최종 Go source buffer에 직접 복사해 한 번만 parse한다. 결과는 bounded chunk spool에 한 파일씩 직렬화하고 `outputRead`가 소비한 Go chunk를 즉시 해제하면서 streaming ZIP sink로 기록한다. 전체 결과 `[][]byte`, 전체 Blob 배열, 전체 page Canvas 배열은 만들지 않는다.

선행 Security Audit의 공통 bounded reader와 Scan Cleanup의 bounded serializer·sink wrapper·download cleanup registry를 재사용한다. source parse에는 loose public `Parse/Get/R/Pages`를 사용하지 않는다.

## 선택한 접근

기존 `/split/`을 복잡하게 만들지 않고 별도 상태형 도구를 만든다.

- Go session이 source bytes, parsed `Doc`, page map과 현재 plan을 소유한다.
- 브라우저는 `File.slice(offset, offset+1MiB).arrayBuffer()`로 정확히 한 chunk만 만들고 그 buffer를 전용 Worker에 transfer한다. Worker가 없으면 명시적으로 지원 불가 처리하고 main-thread WASM으로 fallback하지 않는다.
- transfer는 main thread의 chunk 복제를 없앨 뿐이다. Worker의 JS bytes를 Go source에 넣는 JS→Go 복사와 Go output chunk를 JS `Uint8Array`로 내보내는 Go→JS 복사는 남으며, 두 bridge copy는 chunk당 최대 1MiB로 제한한다.
- browser blank analyzer가 필요한 경우 Go session 시작 전에 PDF.js로 순차 분석하고 종료한다.
- page-count, bookmark, selectable-text pattern 규칙은 Go가 bounded plan을 만든다.
- 사용자가 plan의 filename과 연속 separator group 단위 keep/drop을 검토한다.
- export는 한 segment를 allocation-before-rejection이 가능한 bounded spool로 직렬화한다. `outputRead`가 1MiB 이하 chunk를 하나씩 넘기고 ZIP writer가 해당 chunk write를 완료한 뒤 다음 chunk를 요청한다.

기존 `/split/`은 직접 범위 한 개를 추출하는 단순 도구로 유지한다. PDF.js를 writer로 사용하거나 새 JS PDF dependency를 추가하지 않는다.

## 입력과 규칙 선택

- PDF 한 개, 최대 256MiB, 최대 500페이지를 기본으로 한다.
- parser-only page-count/bookmark/text 규칙의 hard page 한도도 500으로 통일해 UI와 output budget을 예측 가능하게 유지한다.
- 암호화 PDF는 `/unlock/`에서 먼저 해제하도록 안내한다.
- 한 실행에서는 규칙 하나만 선택한다. 여러 규칙의 합집합·우선순위 조합은 범위 밖이다.
- 모든 규칙은 파일 생성 전 segment plan을 표시한다.
- plan revision과 input content identity가 바뀌면 기존 export를 stale 처리한다.
- `sourceStart` options는 원본 파일명에서 마지막 `.pdf`를 제거한 UTF-8 source stem을 받는다. 입력 stem은 최대 1,024 bytes이며 Go가 basename·control/path 제거와 180-byte output filename 제한을 다시 적용한다. 빈 stem은 `document`다.
- `SmartSplitLimits`의 `0` field는 자원 한도 표의 default를 사용한다. nonzero field는 양수이면서 해당 hard cap 이하여야 하며 client가 hard cap을 올릴 수 없다.

## plan 모델

```go
type SmartSplitLimits struct {
    MaxInputBytes              uint64 `json:"maxInputBytes"`
    MaxPages                   int    `json:"maxPages"`
    MaxSegments                int    `json:"maxSegments"`
    MaxXRefEntries             int    `json:"maxXRefEntries"`
    MaxObjects                 int    `json:"maxObjects"`
    MaxEdges                   int    `json:"maxEdges"`
    MaxTreeNodes               int    `json:"maxTreeNodes"`
    MaxDepth                   int    `json:"maxDepth"`
    MaxTokenBytes              uint64 `json:"maxTokenBytes"`
    MaxParsedStringBytes       uint64 `json:"maxParsedStringBytes"`
    MaxDecodedStreamBytes      uint64 `json:"maxDecodedStreamBytes"`
    MaxDecodedStreamTotalBytes uint64 `json:"maxDecodedStreamTotalBytes"`
    MaxPatternBytes            int    `json:"maxPatternBytes"`
    MaxPageTextBytes           int    `json:"maxPageTextBytes"`
    MaxTotalTextBytes          int    `json:"maxTotalTextBytes"`
    MaxOutputBytes             uint64 `json:"maxOutputBytes"`
}

type ReviewedSeparatorGroup struct {
    Pages []int `json:"pages"`
    Omit  bool  `json:"omit"`
}

type ReviewedBlankAnalysis struct {
    Groups   []ReviewedSeparatorGroup `json:"groups"`
    Complete bool                     `json:"complete"`
    Warnings []SmartSplitWarning      `json:"warnings"`
}

type SmartSplitRule struct {
    Kind          string                   `json:"kind"`
    PageCount     int                      `json:"pageCount,omitempty"`
    Pattern       string                   `json:"pattern,omitempty"`
    BlankAnalysis *ReviewedBlankAnalysis   `json:"blankAnalysis,omitempty"`
}

type SmartSplitPlanEdit struct {
    Index int    `json:"index"`
    Name  string `json:"name"`
}

type SmartSplitWarning struct {
    Code         string `json:"code"`
    Page         int    `json:"page,omitempty"`
    ObjectNumber int    `json:"objectNumber,omitempty"`
    Location     string `json:"location,omitempty"`
}

type SmartSplitSegment struct {
    Index      int    `json:"index"`
    StartPage  int    `json:"startPage"`
    EndPage    int    `json:"endPage"`
    Name       string `json:"name"`
    ReasonCode string `json:"reasonCode"`
    ReasonPage int    `json:"reasonPage,omitempty"`
}

type SmartSplitPlan struct {
    Revision     uint64              `json:"revision"`
    PageCount    int                 `json:"pageCount"`
    Segments     []SmartSplitSegment `json:"segments"`
    OmittedPages []int               `json:"omittedPages"`
    Warnings     []SmartSplitWarning `json:"warnings"`
    Complete     bool                `json:"complete"`
}
```

계약은 다음과 같다.

- segment는 source page 기준 1-based contiguous range다.
- `SmartSplitSegment.Index`와 `SmartSplitPlanEdit.Index`는 0-based consecutive index다. `StartPage`, `EndPage`, separator `Pages`와 warning `Page`는 source 기준 1-based다.
- 겹치거나 비어 있는 segment는 없다.
- omitted separator 외의 모든 source page가 정확히 한 segment에 포함된다.
- segment는 최대 500개다.
- 모든 output name은 basename만 허용하고 control character, `/`, `\\`, `..` path segment를 제거한다.
- UTF-8 filename은 180 bytes로 제한하고 collision은 `-2`, `-3` suffix로 해결한다.
- plan warning과 reason은 stable code로 전달하고 localized copy는 브라우저가 담당한다. `ReasonCode` allowlist는 `page-count`, `bookmark-preface`, `bookmark`, `text-preface`, `text-match`, `text-no-match`, `blank-section`이다.
- `SmartSplitWarning.Code` allowlist는 `bookmark-cycle`, `bookmark-depth-limit`, `bookmark-node-limit`, `bookmark-duplicate`, `bookmark-backward`, `bookmark-remote`, `bookmark-unresolved`, `text-unsupported-filter`, `text-decode-limit`, `text-parse-incomplete`, `blank-analysis-incomplete`다. `Location`은 빈 문자열 또는 `catalog`, `outline`, `name-tree`, `page-content`, `blank-analysis` 중 하나다. `Page`와 `ObjectNumber`의 `0`, `Location`의 빈 문자열은 해당 위치 정보가 없다는 뜻이다.
- blank rule은 `blankAnalysis`를 반드시 명시하고 다른 rule은 이를 금지한다. `ReviewedBlankAnalysis` JSON은 `groups`, `complete`, `warnings`를 모두 명시한다. warning은 `blank-analysis-incomplete`와 `blank-analysis` location만 허용하며 render/limit 때문에 확인하지 못한 1-based page를 담는다. `ReviewedSeparatorGroup` JSON은 `pages`와 `omit`을 모두 명시해야 한다. `SmartSplitPlanEdit` JSON은 `index`와 `name`만 허용하고 해당 `Index` segment의 `Name`만 바꾼다. range, reason, omission 또는 separator 결정 mutation은 wire와 Go 양쪽에서 거부한다.

## 규칙 의미

### 고정 페이지 수

- `N`은 `1..500`의 안전한 정수다.
- `1-N`, `N+1-2N` 순으로 contiguous chunk를 만든다.
- 마지막 segment는 더 짧을 수 있다.
- 기본 이름은 `<stem>-pages-0001-0010.pdf` 형식이다.
- 모든 segment의 `ReasonCode`는 `page-count`다.

### 북마크

- 최상위 outline item만 section 시작으로 사용한다.
- direct `/Dest` array와 `/A << /S /GoTo /D ... >>`를 지원한다.
- Catalog `/Dests`와 `/Names /Dests`의 named destination을 bounded name-tree로 해석한다.
- destination page Ref를 현재 page index로 변환한다.
- 첫 bookmark 전 preface가 있으면 별도 segment로 만든다.
- 중복, 뒤로 가는 boundary, remote destination, page를 찾을 수 없는 destination은 warning과 함께 무시한다.
- bookmark title은 4KiB까지만 decode한 뒤 filename sanitize를 적용한다.
- 유효한 boundary가 하나도 없으면 plan 생성 실패로 처리하고 page-count 규칙을 자동 적용하지 않는다.
- preface segment의 기본 이름은 `<stem>-preface-pages-0001-0003.pdf`, `ReasonCode`는 `bookmark-preface`다. bookmark segment는 `<stem>-<sanitized-title>-pages-0004-0010.pdf`, `ReasonCode`는 `bookmark`다. 빈/삭제된 title은 `bookmark-001`, `bookmark-002` 순 seed를 사용한다.

북마크는 분할 기준과 filename 제안에만 사용한다. 현재 writer는 topology 변화에서 `Outlines`, `Names`, `Dests`, `PageLabels`, `StructTreeRoot`, `AcroForm`을 제거할 수 있으므로 결과 PDF에 원본 bookmark가 유지된다고 주장하지 않는다.

### 선택 가능한 텍스트 정규식

- Go `regexp`의 RE2 semantics를 사용한다.
- pattern UTF-8 bytes는 최대 1,024다.
- 각 page의 selectable text를 bounded extractor로 읽고 match page 앞에서 분할한다.
- 첫 페이지 match는 빈 preface를 만들지 않는다.
- pattern match 페이지는 새 segment의 첫 페이지로 유지한다.
- page content decode 최대 1MiB, extracted text 최대 1MiB/page, 누적 text 최대 32MiB다.
- 한 page라도 unsupported filter, decode budget 또는 text parse 오류로 검사 불완전하면 `Complete:false`와 해당 page warning을 반환한다.
- image-only scan은 match되지 않으며 OCR을 수행했다고 주장하지 않는다. UI는 `/ocr/` 또는 검색 가능한 OCR PDF를 먼저 만들도록 안내한다.
- 기본 이름은 `<stem>-text-001-pages-0001-0004.pdf`처럼 segment index와 range를 포함한다. 첫 match 전 segment는 `text-preface`, match로 시작한 segment는 `text-match`, match가 하나도 없어 한 segment만 생기면 `text-no-match`다.

### 빈 separator 페이지

- `web/page-analysis.mjs`와 `web/page-pixels.mjs`의 공통 blank detector를 사용한다.
- PDF.js 분석은 한 페이지씩 최대 2,000,000픽셀로 수행한다.
- high-confidence blank만 separator 후보로 preselect한다.
- consecutive blank는 하나의 maximal boundary group으로 접는다. 그룹의 `Pages`는 비어 있지 않고 오름차순으로 연속해야 하며, 그룹끼리는 오름차순·비중첩이고 서로 인접할 수 없다.
- 기본값은 group omission이지만 export 전 `ReviewedSeparatorGroup.Omit`을 group 단위로 바꿀 수 있다. 같은 연속 group 안에서 일부 page만 유지하거나 제거하는 상태는 UI와 Go 모두 거부한다.
- leading/trailing blank도 omission 후보로 표시한다.
- low-confidence와 inconclusive page는 유지하고 boundary로 사용하지 않는다.
- 모든 페이지를 omitted로 만드는 plan은 거부한다.
- retained segment 기본 이름은 `<stem>-section-001-pages-0001-0007.pdf` 형식이고 `ReasonCode`는 `blank-section`이다.

사용자 검토가 끝난 `BlankAnalysis.Groups`를 Go로 전달한다. `BlankAnalysis.Complete:false`이면 retained coverage plan은 만들되 plan도 `Complete:false`이고 typed warnings를 그대로 검증·보존한다. incomplete page를 blank boundary나 omitted page로 추정하지 않는다. 귀속 규칙은 다음과 같다.

- omitted group은 segment에 포함하지 않는다. interior omitted group 앞에서 이전 segment를 닫고 다음 retained page에서 새 segment를 시작한다.
- kept leading group은 첫 segment의 prefix다. leading group만으로 빈 segment를 만들지 않는다.
- kept interior group은 앞 segment의 suffix이며 group 마지막 page 뒤에서 다음 segment를 시작한다.
- kept trailing group은 앞 segment의 suffix다. trailing group 뒤에 빈 segment를 만들지 않는다.
- source 전체가 하나의 kept group이면 하나의 segment로 유지하고, 전체 omitted이면 실패한다.

Go는 group의 maximal/consecutive/order/range 조건과 plan coverage를 다시 검증한다. separator 결정을 바꾸면 새 `SmartSplitRule`로 `plan(rule)`을 다시 호출해 revision을 증가시킨다. `replacePlan(revision, edits)`는 `SmartSplitPlanEdit{Index,Name}`에 의한 filename 변경만 허용한다.

## Go core와 session

`pdf/smart_split.go`는 다음 책임을 가진다.

- source 한 번 parse
- page Ref→index map 생성
- outline/name-tree bounded traversal
- bounded per-page text extraction과 RE2 match
- reviewed blank page facts를 segment boundary로 정규화
- plan coverage와 filename seed 생성
- plan segment 하나를 shared bounded PDF serializer로 chunk spool에 직렬화
- source를 Security Audit의 `ParseBounded`→`PagesBounded`로 한 번 읽고 모든 bookmark/text/output lookup에서 `GetBounded`/`ResolveBounded` 오류를 전파

시작 options는 다음과 같다.

```go
type SmartSplitStartOptions struct {
    Size       uint64           `json:"size"`
    SourceStem string           `json:"sourceStem"`
    Limits     SmartSplitLimits `json:"limits"`
}
```

WASM session protocol은 다음과 같다.

```text
sourceStart(options)                                  -> {sourceRevision,nextOffset:0}
sourceChunk(sourceRevision,offset,data<=1MiB)          -> {nextOffset,copiedBytes}
sourceFinish(sourceRevision)                           -> {sessionRevision,pageCount}
plan(rule)                                             -> SmartSplitPlan
replacePlan(revision,edits)                            -> SmartSplitPlan
outputStart(revision,index,remainingArchiveBytes)      -> {outputRevision,name,size}
outputRead(outputRevision,maxBytes=1MiB)               -> {data:Uint8Array<=1MiB,done,remainingBytes}
outputRelease(outputRevision)                          -> {state:"released"}
bridgeStats()                                          -> SmartSplitBridgeStats
abort()                                                -> {state:"aborted"}
```

`sourceStart`는 공통 wire field `size`만큼 최종 Go source buffer를 한 번 할당한다. `sourceChunk`는 offset이 정확히 직전 `nextOffset`이고 data가 1..1MiB이며 source 끝을 넘지 않을 때에만 `js.CopyBytesToGo`로 최종 buffer의 해당 slice에 직접 기록한다. 별도 Go chunk buffer나 브라우저의 전체 source typed-array는 만들지 않는다. `sourceFinish`는 정확히 `size` bytes를 받은 뒤 한 번만 parse한다.

`sourceFinish`는 `SmartSplitLimits`를 공통 `PDFReadLimits`로 변환하고 `ParseBounded`→`PagesBounded`만 호출한다. xref/object/object-stream/direct-container/token/parsed string/decoded stream/page-tree counter는 source session 전체에서 누적되며 plan 또는 output마다 초기화하지 않는다. bookmark/name-tree/text/output import가 source object를 읽을 때는 `GetBounded`/`ResolveBounded`만 사용하고 typed error를 warning 없는 non-match로 바꾸지 않는다. output의 `BoundedGraphLimits`는 별도 counter다.

- 새 `sourceStart`는 기존 source/doc/plan/output spool을 폐기한다.
- `plan` 오류는 source session을 유지하되 이전 plan을 폐기한다.
- `replacePlan`은 filename만 변경할 수 있고 page coverage를 재검증한다. separator keep/drop 변경은 `plan` 재호출이다.
- `outputStart`는 정확히 현재 plan revision만 허용하며 active output이 있으면 실패한다.
- `outputStart`의 effective cap은 `min(256MiB, remainingArchiveBytes)`다. shared writer가 cap+1 byte를 쓰려는 순간 새 chunk allocation 전에 실패한다.
- `outputRead`는 공통 `maxBytes` field가 정확히 `PDFBridgeChunkBytes`인지 검증하고 spool의 맨 앞 Go chunk ownership을 제거해 그 한 chunk만 JS로 복사한다. ZIP sink가 JS chunk write를 완료한 뒤 다음 `outputRead`를 호출한다.
- 정상 완료, application error와 Worker가 응답 가능한 cooperative abort는 `outputRelease`를 정확히 한 번 호출하며 release 뒤 retained spool bytes는 0이어야 한다. `error`/`messageerror`/load failure 같은 transport fatal에서는 release command 성공을 요구하지 않고 Worker를 terminate해 Go heap 전체를 폐기한다.
- `bridgeStats`는 실제 `js.CopyBytesToGo`/`js.CopyBytesToJS` 직후 call·byte·max transient와 spool retained/peak를 갱신하고 `Doc.ReadStats()`를 nested field로 포함한 read-only 계측이다. UI에는 표시하지 않고 native test와 actual TinyGo E2E가 copy/retention과 parser/page-tree/cache 한도를 함께 검사한다.
- abort와 fatal parse/output 오류는 session을 폐기한다.

catalog의 runtime marker는 `"runtime":{"worker":"required","stateful":true,"chunkedIO":true}`다. `/smartsplit/`은 `window.smartSplitWasm = boot("./smartsplit.wasm", {requireWorker:true, transferOwnership:true})`로 기존 then/await 호환 Promise에 `{run,cancel,dispose}`를 붙인 shared handle을 사용한다. 완료된 순차 command는 같은 Worker를 유지하고, concurrent command만 이전 command와 session을 취소한다. Worker 생성 불가, load failure, `error`, `messageerror`는 main-thread fallback 없이 실패한다.

shared client는 Worker가 이미 terminate된 failure/cancel에 `error.workerGone === true`를 붙인다. chunk helper는 이 marker가 없을 때만 cooperative `outputRelease`를 호출한다. marker가 있으면 새 Worker를 release 명령 때문에 생성하지 않고 terminal disposer로 끝낸다.

`web/wasm-chunks.mjs`는 Security Audit에서 도입한 shared helper를 재사용하며 interface는 다음과 같다.

```js
export const WASM_BRIDGE_CHUNK_BYTES = 1024 * 1024;
export const WASM_SOURCE_UPLOAD_COMMANDS = Object.freeze({ start:"sourceStart", chunk:"sourceChunk", finish:"sourceFinish", abort:"abort" });
export async function uploadBlobChunks({ blob, start, commands = WASM_SOURCE_UPLOAD_COMMANDS, run, signal }) {}
export async function* readOutputChunks({ outputRevision, run, signal }) {}
```

`uploadBlobChunks`는 start command의 `run(command,payload)` adapter에 `{...start,size:blob.size}`를 넘겨 normalized `{uploadRevision,nextOffset,maxChunkBytes}`를 받고, `blob.arrayBuffer()`를 호출하지 않은 채 `blob.slice(offset,end).arrayBuffer()`를 한 번에 하나만 await해 configured chunk→finish를 수행한다. Smart caller의 `start`는 `{sourceStem,limits}`이고 default source commands를 쓴다. 각 chunk는 exact-size buffer이며 `transferOwnership:true`에 의해 caller view가 detach된다. `readOutputChunks`는 한 번에 `outputRead({outputRevision,maxBytes:WASM_BRIDGE_CHUNK_BYTES})` 하나만 await하고 이전 chunk를 consumer가 처리한 다음에만 다음 chunk를 요청하며, 정상·application throw·Worker가 살아 있는 cooperative abort의 `finally`에서 `outputRelease`를 정확히 한 번 호출한다. `workerGone` failure이면 release를 건너뛰고 terminal disposer가 끝낸다.

Smart WASM global은 positional arguments가 아니라 `{command,...payload}` 객체 하나만 받는다. `web/smartsplit/protocol.mjs`가 `handle.run({command,...payload})`의 `{json}` 또는 `{data,json}` 결과를 exact schema로 normalize해 shared helper의 `run(command,payload)`로 제공한다. product error는 typed application error이고 transport reject/`workerGone`과 구분한다.

## bounded serializer와 메모리 계약

`pdf/bounded_output.go`의 shared API는 다음 계약을 사용한다.

```go
const PDFBridgeChunkBytes = 1 << 20

var ErrOutputLimit = errors.New("pdf output limit exceeded")
var ErrSpoolState = errors.New("invalid bounded spool state")

type ChunkAllocator interface {
    Allocate(size int) []byte
    Release(chunk []byte)
}

type HeapChunkAllocator struct{}

func (HeapChunkAllocator) Allocate(size int) []byte
func (HeapChunkAllocator) Release(chunk []byte)

type BoundedGraphLimits struct {
    MaxObjects int
    MaxEdges   int
    MaxDepth   int
}

type SmartSplitBridgeStats struct {
    SourceCopyCalls       uint64 `json:"sourceCopyCalls"`
    SourceCopiedBytes     uint64 `json:"sourceCopiedBytes"`
    OutputCopyCalls       uint64 `json:"outputCopyCalls"`
    OutputCopiedBytes     uint64 `json:"outputCopiedBytes"`
    MaxTransientCopyBytes uint64 `json:"maxTransientCopyBytes"`
    SpoolRetainedBytes    uint64 `json:"spoolRetainedBytes"`
    PeakSpoolRetained     uint64 `json:"peakSpoolRetainedBytes"`
    ReadStats             PDFReadStats `json:"readStats"`
}

type BoundedChunkSpool struct {
    limit       uint64
    size        uint64
    remaining   uint64
    chunkBytes  int
    chunks      [][]byte
    used        []int
    head        int
    checkedOut  []byte
    allocator   ChunkAllocator
    stickyError error
    sealed      bool
    readerActive bool
    drainStarted bool
}

func NewBoundedChunkSpool(limit uint64, chunkBytes int, allocator ChunkAllocator) (*BoundedChunkSpool, error)
func (s *BoundedChunkSpool) Write(p []byte) (int, error)
func (s *BoundedChunkSpool) Reader() io.Reader
func (s *BoundedChunkSpool) Take() (chunk []byte, done bool, err error)
func (s *BoundedChunkSpool) ReleaseTaken(chunk []byte) error
func (s *BoundedChunkSpool) Size() uint64
func (s *BoundedChunkSpool) Remaining() uint64
func (s *BoundedChunkSpool) Release()
func (b *builder) writeTo(root Ref, dst *BoundedChunkSpool) error
func (d *Doc) reachableBounded(roots []int, limits BoundedGraphLimits) ([]int, error)
func (b *builder) importDocBounded(d *Doc, roots []int, limits BoundedGraphLimits) (map[int]Ref, error)
func buildOrderedBounded(docs []*Doc, order []pageSel, mut pageMutator, limits BoundedGraphLimits) (*builder, Ref, error)
func (b *builder) finalizeBounded(root Ref, limits BoundedGraphLimits) (Ref, error)
```

`writeTo`는 현재 `bytes.Buffer` 전용 PDF object serializer를 error-returning writer 경로로 일반화한다. xref offset은 spool의 누적 byte count로 계산하고 최종 `[]byte`를 만들지 않는다. 선행 Scan Cleanup이 구현한 `buildOrderedBounded`는 object/edge/direct-container depth를 queue/map/clone append와 recursive frame 전에 검사해 builder graph가 output cap보다 먼저 폭증하지 않게 한다. `BoundedChunkSpool.Write`는 `current+len(p)`를 overflow-safe하게 검사하고 limit 초과면 어떤 chunk도 할당·복사하지 않는다. `Reader`는 payload를 복사하거나 head/remaining을 바꾸지 않는 순차 view이며 호출 시 spool을 seal한다. 첫 `Take` 전만 허용하고 Reader가 EOF에 도달하기 전 `Take`는 `ErrSpoolState`다. 이미 drain을 시작한 뒤 호출한 `Reader`는 첫 `Read`에서 `ErrSpoolState`를 반환한다. `Take`는 최대 1MiB chunk 하나를 spool에서 제거해 checked-out ownership으로 바꾼다. Go→JS 복사가 끝난 직후 `ReleaseTaken`이 allocator의 `Release`를 호출하며 그 전에는 다음 `Take`를 거부한다. `outputRelease`는 checked-out chunk와 남은 모든 chunk에 `Release`를 호출한다.

dominant payload의 live-byte 목표는 다음이다.

```text
browser File backing
+ Go source
+ (Go output 잔여 chunk spool + ZIP sink 기작성 payload ~= 최종 archive)
+ JS/Go bridge transient chunk <= 1MiB
+ bounded parser/object/xref/central-directory metadata
```

이는 ownership-transfer만으로 zero-copy 또는 정확한 총 heap `1+1`을 주장하지 않는다. parser object graph와 ZIP header metadata는 별도지만, source 전체 JS copy와 Go output 전체+JS output 전체의 중복 생존은 금지한다. memory fallback은 archive hard cap 64MiB이고 directory 또는 OPFS sink를 먼저 사용한다. directory/OPFS의 effective archive cap은 256MiB다.

`HeapChunkAllocator.Release`는 Go reference ownership을 제거하지만 WASM linear memory capacity를 즉시 축소한다고 주장하지 않는다. `bridgeStats`는 reachable payload bytes를 계측한다. export terminal success/error 뒤에는 source session을 abort하고 Worker를 terminate해 Go linear memory 자체를 폐기한다. 사용자가 다시 export하면 보유 중인 원본 `File`을 새 Worker에 1MiB chunk로 다시 ingest한다.

## streaming output

브라우저 export 순서는 다음과 같다.

1. 현재 plan revision을 snapshot한다.
2. directory/OPFS를 우선하고 둘 다 사용할 수 없을 때 64MiB bounded-memory sink를 연다.
3. `zipStoreStream`에 async generator를 전달한다.
4. generator는 남은 모든 entry의 local header·data descriptor·central directory·EOCD bytes를 먼저 예약하고 `archive cap - bytesWritten - reserved overhead`를 `outputStart`에 전달한다.
5. `{name,data: async chunk iterator}`를 yield하고 iterator가 `outputRead`를 한 번씩 호출한다.
6. ZIP writer가 각 chunk를 sink에 완전히 쓴 뒤 JS typed-array 참조를 해제하고 다음 `outputRead`를 요청한다.
7. entry가 끝나면 `outputRelease`를 호출한 뒤에만 `outputStart(i+1)`를 요청한다.
8. central directory와 sink close가 성공한 뒤에만 download 또는 저장 완료를 표시한다.
9. terminal success/error에서 source session을 abort하고 Worker를 terminate한다. 재분할은 원본 `File`에서 새 session을 만든다.

모든 sink는 Scan Cleanup이 먼저 만든 `createByteLimitedSink`를 거친다. OPFS나 directory가 있어도 256MiB를 넘지 않고 memory fallback은 64MiB를 넘지 않는다. 고정 ZIP overhead를 빼기 전 output을 만들지 않으므로 archive에 들어갈 수 없는 entry 전체를 먼저 할당한 뒤 버리지 않는다. 오류·취소 시 active output을 release하고 도구가 소유한 partial file/OPFS entry를 제거하며 사용자가 제공한 다른 파일은 건드리지 않는다.

memory/OPFS close가 Blob/File을 반환하면 anchor click 전에 Scan Cleanup의 shared `createDelayedCleanupRegistry`에 Object URL과 `closedSink.cleanup()`을 등록한다. 10초 timer, explicit cleanup과 `pagehide`는 같은 Promise를 사용해 URL revoke와 OPFS remove를 각각 한 번만 수행한다. user-selected directory close는 `null`을 반환하고 성공 파일은 제거하지 않는다. stale close는 download 없이 즉시 cleanup한다.

## 문서 의미와 경고

각 output은 PDF rewrite이므로 다음을 명시한다.

- 기존 전자서명은 무효화된다.
- topology 변화에서 outline, form, named destination, page label, structure tree가 제거될 수 있다.
- Info와 standard Metadata는 남을 수 있다.
- page-level annotation과 additional action은 남을 수 있다.
- 북마크 기준으로 나눴더라도 결과에 북마크를 보존한다고 보장하지 않는다.

UI는 `/securityaudit/`와 Metadata 도구 링크를 제공하지만 자동 sanitization을 주장하지 않는다.

## 자원 한도

| 자원 | 한도 |
|---|---:|
| source PDF | 256MiB |
| source stem | 1,024 UTF-8 bytes |
| page | 500 |
| segment | 500 |
| xref entry | 100,000 |
| imported object | 100,000 |
| graph edge | 1,000,000 |
| outline/name-tree node | 각 10,000 |
| traversal depth | 64 |
| 단일 token | 8MiB |
| 누적 parsed string/name | 64MiB |
| decoded stream | 개별 64MiB, 누적 256MiB |
| regexp pattern | 1,024 bytes |
| decoded content/text | page당 1MiB, 누적 text 32MiB |
| blank analysis raster | page당 2,000,000픽셀 |
| live blank surface | 합산 16,000,000 RGBA 픽셀 |
| output entry | 256MiB |
| archive | 256MiB |
| memory fallback archive | 64MiB |
| source/output bridge chunk | 1MiB |
| 동시 parse/render/output | 1 |

빈 페이지 분석이 필요한 경우 PDF.js session을 완전히 destroy한 뒤 Go source session을 시작한다. 같은 source를 PDF.js와 Go가 동시에 장기 보유하지 않는다.

## 상태·취소·오류

- input, rule, pattern, blank override 또는 filename이 바뀌면 plan revision이 증가한다.
- export 중 revision이 바뀌면 Worker, ZIP generator와 sink를 abort하고 stale 결과를 저장하지 않는다.
- pre-aborted signal은 source를 읽거나 Worker를 시작하기 전에 실패한다.
- `Worker.error`, `messageerror`, sequence gap, invalid response는 session을 종료한다.
- name-tree/outline cycle, unsupported destination, incomplete text는 warning 또는 `Complete:false`로 나타내고 잘못된 boundary로 바꾸지 않는다.
- plan이 0/1 segment만 만들면 결과를 분명히 표시하고 불필요한 ZIP export를 기본 비활성화한다.
- `pagehide`에서 Worker, PDF.js session, object URL, partial sink를 정리한다.
- source/export generation은 local `{owner,handle,sink,cleanupPromise}`를 캡처한다. 새 generation은 이전 cleanup을 await하고 old finally는 자신이 current owner일 때만 `abort`/`dispose`/partial sink 제거를 수행한다. `workerGone` transport failure에서는 release/abort를 위해 fresh Worker를 만들지 않는다.

## 접근성·다국어·오프라인

- segment plan은 keyboard로 filename 편집과 blank keep/drop을 수행할 수 있다.
- start/end page와 omission은 색 외에 텍스트로 표시한다.
- 진행은 polite live region, fatal error는 assertive alert를 사용한다.
- English/Korean source와 ja/zh/es/fr/de dictionary를 제공한다.
- 첫 온라인 로드 후 page/WASM/PDF.js worker가 캐시되면 모든 rule이 오프라인에서 동작해야 한다.

## 검증

### Go

- page-count chunk와 마지막 짧은 segment
- direct/named destination, preface, duplicate/backward/remote/unresolved bookmark
- cyclic/deep outline·name tree와 node budget
- RE2 pattern compile, first-page match, no match, malformed pattern, page/total text budget
- reviewed blank group의 leading kept→첫 segment, interior/trailing kept→앞 segment, group omission, consecutive group 내부 mixed decision 거부와 전부 제거 거부
- blank analysis incomplete warning이 plan `Complete:false`로 보존되고 해당 page가 boundary/omission으로 추정되지 않음
- plan coverage, overlap, gap, stale revision, filename sanitize/collision
- 모든 rule의 exact `ReasonCode`와 deterministic default filename seed
- `SmartSplitPlanEdit{Index,Name}` 외 mutation 거부와 separator 변경 시 `plan(rule)` 재호출 revision
- source stem `document`, path/control sanitize와 page-count 기본 filename
- source는 한 번 parse되고 segment 하나씩만 build됨을 instrument한 test
- injected `ChunkAllocator`가 cap+1 write 전에 allocation을 거부하고 `Take`마다 retained bytes가 감소하며 release 후 0임을 검증
- bounded graph import가 object/edge/depth cap을 map/queue/clone 추가 전에 적용하고 cap+1에서 output chunk allocation 0임을 검증
- `ParseBounded/GetBounded/ResolveBounded/PagesBounded`가 xref/object/edge/depth/token/parsed/decode/page cap을 allocation 전에 적용하고 여러 plan/output을 거쳐도 session-wide `ReadStats`가 누적되며 cap+1에서 output chunk allocation 0임을 검증
- non-consuming `Reader`가 payload allocation 없이 byte-identical stream을 제공하고 Reader EOF 전 Take와 Take 뒤 Reader를 state error로 거부함
- 각 output에서 `spool retained + sink written payload`의 peak가 output size+ZIP 고정 overhead+1MiB를 넘지 않는 counter test
- output page 순서와 documented catalog/annotation semantics

### JavaScript

- `uploadBlobChunks`가 1MiB `Blob.slice`만 사용하고 full `arrayBuffer`를 호출하지 않으며 offset gap/duplicate를 거부함
- actual Worker transfer 뒤 caller source chunk `ArrayBuffer`가 detach되고 `bridgeStats()`의 JS→Go/Go→JS call·byte 합계가 실제 source/output 크기와 일치하며 max transient가 1MiB 이하임
- async generator가 이전 ZIP chunk write와 `outputRelease` 완료 전 다음 `outputStart`를 호출하지 않음
- directory/OPFS/memory sink 모두 archive byte cap 적용
- directory/OPFS 우선과 memory fallback 64MiB hard cap, ZIP 고정 overhead를 뺀 `remainingArchiveBytes` 전달
- fake sink와 instrumented Worker가 `Go spool retained + sink buffered + transient chunk`의 peak bound와 release 후 retained 0을 검증
- partial sink cleanup과 input file 비삭제
- blank analyzer high/low/inconclusive review defaults
- separator group 변경은 `plan`, filename 변경은 `replacePlan`만 호출함
- plan mutation 중 export abort와 stale output 차단
- stateful sequential command가 Worker 하나를 유지하고 concurrent command만 cancel하며 Worker unavailable/load error/`error`/`messageerror`에서 fallback하지 않음
- runtime worker marker와 `boot("./smartsplit.wasm",{requireWorker:true,transferOwnership:true})`를 worker-contract가 검증함
- pre-abort, Worker error/messageerror, `pagehide` cleanup
- delayed old cleanup이 fresh Worker/sink를 종료·삭제하지 않는 owner-generation test
- 성공 OPFS download의 timer/pagehide/manual race에서 URL revoke와 remove 각 1회, directory remove 0회, pending cleanup 0을 검증

### E2E

- 네 rule 각각으로 실제 TinyGo plan·`outputStart`/`outputRead`/`outputRelease` 실행
- ZIP central directory에서 filename·entry 수·CRC 검사
- 각 entry PDF parse와 예상 page text/order 확인
- 500-page source가 500-segment/500-entry plan을 만드는 synthetic session에서 active output spool 하나, bridge chunk 하나만 live임을 peak counter로 확인
- encrypted/unsupported/malformed PDF 오류
- 7개 언어 generated page asset·번역
- Playwright `chromium`, `firefox`, `webkit`, `mobile-chromium`, `mobile-webkit` project 각각에서 동일 E2E 실행

## 제외 범위

- 여러 규칙 조합과 우선순위 graph
- image-only 페이지의 자동 OCR text rule
- nested bookmark level 선택과 remote destination
- 결과 PDF의 outline/page label/form/structure tree remap
- 기존 전자서명 보존
- 사용자 검토 없는 heuristic blank 삭제
- ZIP64와 256MiB 초과 archive
- server 업로드 또는 background processing

## 완료 조건

1. 네 규칙이 동일한 reviewed segment-plan 모델을 사용한다.
2. source는 1MiB 순차 JS→Go copy로 최종 Go buffer에 기록되고 session당 한 번만 parse된다.
3. 결과 PDF는 bounded chunk spool로 하나씩 생성되며 chunk가 ZIP에 기록될 때 Go ownership이 해제되고 전체 JS output 배열로 누적되지 않는다.
4. blank·text·bookmark의 불완전 분석이 정상 non-match로 숨겨지지 않는다.
5. plan coverage·revision·output byte budget이 Go와 브라우저 양쪽에서 검증된다.
6. 문서 구조 손실과 signature 무효화를 실행 전에 표시한다.
7. instrumented retained-byte/copy counter와 browser detachment test가 dominant payload의 `File + Go source + (Go output 잔여 + sink 기작성분≈archive) + 1MiB transient chunk` 경계를 증명한다.
8. 모든 처리가 로컬이고 Go 외부 모듈, commit, push를 추가하지 않는다.
9. source xref/object/container/token/decode/page-tree/cache counter는 session 전체에서 bounded이고 plan/output마다 초기화되지 않는다.
10. successful OPFS download URL과 임시 archive는 timer 또는 pagehide에서 exactly once 정리되고 directory 결과는 제거되지 않는다.
