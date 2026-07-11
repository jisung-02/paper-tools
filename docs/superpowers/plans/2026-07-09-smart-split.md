# PDF 스마트 분할 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**목표:** 페이지 수·최상위 북마크·선택 가능한 텍스트 RE2 pattern·검토된 빈 separator group의 네 규칙으로 segment plan을 만들고, 1MiB 순차 source ingest와 bounded output protocol을 사용하는 상태형 Worker에서 결과 PDF를 streaming ZIP으로 기록한다.

**구조:** `pdf.SmartSplitSession`이 source Doc, page map, plan revision과 active bounded chunk spool 하나를 소유한다. 브라우저는 `web/wasm-chunks.mjs`로 `File`을 1MiB slice씩 ingest하고 blank group 검토·filename edit를 담당한다. exporter는 ZIP 남은 payload budget으로 `outputStart`를 호출하고 `outputRead` chunk write와 `outputRelease`가 끝난 뒤에만 다음 output을 시작한다.

**기술:** Go 표준 라이브러리 regexp, TinyGo WASM, PDF.js shared page analyzer, zipStoreStream, OPFS/directory/bounded memory sinks, Node test runner, Playwright

## 전역 제약

- 설계 기준은 docs/superpowers/specs/2026-07-09-smart-split-design.md다.
- 선행 구현은 docs/superpowers/plans/2026-07-09-scan-cleanup.md의 page-analysis/page-pixels다.
- Go 외부 모듈, go.mod/go.sum 변경, commit, push를 금지한다.
- source 256MiB, page 500, segment 500, xref/object 100,000, edge 1,000,000, depth 64, token 8MiB, parsed string/name 64MiB, decoded stream 개별 64MiB·누적 256MiB, pattern 1,024 bytes, page text 1MiB, total text 32MiB, archive 256MiB다.
- shared graph import는 object 100,000개, edge 1,000,000개, direct-container depth 64를 queue/map/clone 추가 전에 제한한다.
- source는 브라우저 전체 `ArrayBuffer`를 만들지 않고 1MiB `Blob.slice` 순차 chunk를 최종 Go source buffer에 직접 복사한 뒤 session당 한 번만 parse한다.
- source chunk transfer는 main-thread 복제를 제거하지만 JS→Go와 Go→JS bridge copy는 남는다. bridge payload는 항상 1MiB 이하이고 Worker가 없으면 main-thread fallback 없이 실패한다.
- dominant payload live-byte 목표는 `File + Go source + (Go output 잔여 spool + ZIP sink 기작성분≈최종 archive) + 1MiB transient chunk`다. parser/object/xref/ZIP metadata는 별도 bounded overhead다.
- directory/OPFS sink를 memory보다 우선하고 memory fallback archive는 64MiB hard cap을 적용한다. directory/OPFS archive cap은 256MiB다.
- heuristic blank는 사용자 검토 없이 삭제하지 않는다.
- incomplete text/bookmark/blank 분석을 정상 non-match로 숨기지 않는다.
- output signature 무효화와 page-indexed 구조 손실을 실행 전에 표시한다.
- 각 작업 검증은 .superpowers/sdd/ report에 기록하고 commit/push는 하지 않는다.
- catalog runtime은 `{"worker":"required","stateful":true,"chunkedIO":true}`이고 page는 `window.smartSplitWasm = boot("./smartsplit.wasm", {requireWorker:true, transferOwnership:true})` handle을 사용한다.

---

### 작업 1: page-count와 reviewed-blank plan core

**파일:**

- 생성: pdf/smart_split.go
- 생성: pdf/smart_split_test.go
- 재사용: pdf/read_limits.go
- 수정: pdf/read_limits_test.go

**인터페이스:**

~~~go
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
    Kind          string                 `json:"kind"`
    PageCount     int                    `json:"pageCount,omitempty"`
    Pattern       string                 `json:"pattern,omitempty"`
    BlankAnalysis *ReviewedBlankAnalysis `json:"blankAnalysis,omitempty"`
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

type SmartSplitStartOptions struct {
    Size       uint64           `json:"size"`
    SourceStem string           `json:"sourceStem"`
    Limits     SmartSplitLimits `json:"limits"`
}
~~~

`SmartSplitSegment.ReasonCode`의 exact allowlist는 `page-count`, `bookmark-preface`, `bookmark`, `text-preface`, `text-match`, `text-no-match`, `blank-section`이다. 구현과 Go·브라우저 schema 검증은 이 목록 밖의 값을 거부한다.

- [ ] **1. page-count RED 테스트 작성**

~~~go
func TestPlanPageCountCreatesContiguousCoverage(t *testing.T) {
    session := newSmartSplitFixtureSession(t, 23, "quarterly-report")
    plan, err := session.Plan(SmartSplitRule{Kind: "page-count", PageCount: 10})
    if err != nil {
        t.Fatalf("Plan: %v", err)
    }
    got := [][2]int{}
    for _, segment := range plan.Segments {
        got = append(got, [2]int{segment.StartPage, segment.EndPage})
    }
    want := [][2]int{{1, 10}, {11, 20}, {21, 23}}
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("segments = %#v, want %#v", got, want)
    }
}
~~~

- [ ] **2. RED 확인**

실행: go test ./pdf -run '^TestPlanPageCount' -count=1

예상: session/types가 없어 compile 실패한다.

- [ ] **3. limit·plan model과 page-count 구현**

`NewSmartSplitSession(file, sourceStem, limits)`는 input bytes, UTF-8 source stem 1,024 bytes와 모든 read/output limit을 allocation 전에 검증한다. Security Audit의 `ParseBounded`→`PagesBounded`로 Doc/Page slice를 한 번 만들고 bookmark/text/output lookup도 `GetBounded`/`ResolveBounded`만 사용한다. source counter는 session 전체에서 누적되고 output graph counter는 별도다. nonzero limit은 양수·hard cap 이하여야 한다. page-count N은 1..500이며 final short chunk를 포함한다. 기본 이름은 `quarterly-report-pages-0001-0010.pdf` 형식이고 빈/sanitized-empty stem은 `document`다. revision은 plan 성공 때 증가한다.

- [ ] **4. reviewed blank RED 테스트 작성**

`ReviewedBlankAnalysis{Groups:...,Complete:true,Warnings:[]}` table test에 다음 expected output page 배열을 정확히 넣는다.

~~~go
tests := []struct {
    name     string
    pages    int
    groups   []ReviewedSeparatorGroup
    segments [][]int
    omitted []int
    wantErr  bool
}{
    {"leading-kept", 6, []ReviewedSeparatorGroup{{Pages: []int{1, 2}, Omit: false}}, [][]int{{1, 2, 3, 4, 5, 6}}, nil, false},
    {"leading-omitted", 6, []ReviewedSeparatorGroup{{Pages: []int{1, 2}, Omit: true}}, [][]int{{3, 4, 5, 6}}, []int{1, 2}, false},
    {"interior-kept-belongs-before", 8, []ReviewedSeparatorGroup{{Pages: []int{4, 5}, Omit: false}}, [][]int{{1, 2, 3, 4, 5}, {6, 7, 8}}, nil, false},
    {"interior-omitted", 8, []ReviewedSeparatorGroup{{Pages: []int{4, 5}, Omit: true}}, [][]int{{1, 2, 3}, {6, 7, 8}}, []int{4, 5}, false},
    {"trailing-kept-belongs-before", 6, []ReviewedSeparatorGroup{{Pages: []int{5, 6}, Omit: false}}, [][]int{{1, 2, 3, 4, 5, 6}}, nil, false},
    {"mixed-groups", 12, []ReviewedSeparatorGroup{{Pages: []int{3, 4}, Omit: true}, {Pages: []int{8, 9}, Omit: false}}, [][]int{{1, 2}, {5, 6, 7, 8, 9}, {10, 11, 12}}, []int{3, 4}, false},
    {"non-consecutive-group", 8, []ReviewedSeparatorGroup{{Pages: []int{3, 5}, Omit: true}}, nil, nil, true},
    {"adjacent-groups-must-merge", 8, []ReviewedSeparatorGroup{{Pages: []int{3}, Omit: true}, {Pages: []int{4}, Omit: false}}, nil, nil, true},
    {"all-omitted", 2, []ReviewedSeparatorGroup{{Pages: []int{1, 2}, Omit: true}}, nil, nil, true},
}
~~~

low-confidence/inconclusive page가 `BlankAnalysis.Groups`에 자동으로 들어가지 않는 browser-state test는 작업 5에서 별도로 작성한다. render/limit failure는 `BlankAnalysis.Complete:false`와 page warning으로 전달되고 plan이 이를 `Complete:false`로 보존해야 한다.

별도 row는 page 4 render failure를 `Complete:false`, `Warnings:[{Code:"blank-analysis-incomplete",Page:4,Location:"blank-analysis"}]`로 전달하고 page 4가 segment에 유지되며 boundary/omission이 아니고 plan도 incomplete인지 검증한다. blank rule의 missing `blankAnalysis`, 다른 rule의 non-nil `blankAnalysis`, warning page/range/code/location 오류를 거부한다.

- [ ] **5. blank normalization 구현**

`ReviewedBlankAnalysis`의 `groups`, `complete`, `warnings`는 모두 wire에 존재해야 한다. 각 group JSON은 `pages`와 `omit`을 모두 명시하고, group은 non-empty, strictly consecutive, sorted, disjoint, non-adjacent maximal group이어야 한다. omitted interior group은 제외하고 양쪽을 나눈다. kept leading group은 첫 segment, kept interior/trailing group은 앞 segment에 귀속한다. omitted 외 모든 page는 정확히 한 segment에 포함되고 각 segment 내부 page는 contiguous하다. segment/edit index는 0-based, page/group index는 1-based다. group 내부 page별 mixed keep/drop, 0-page segment와 all-omitted plan은 거부한다. incomplete warning page는 retained coverage에만 포함하고 heuristic boundary를 만들지 않는다.

- [ ] **6. filename sanitize·collision RED/GREEN**

control, slash, backslash, dot-dot segment, source stem 1,024-byte rejection, UTF-8 180-byte truncation과 같은 이름의 -2 suffix를 검증한다. page-count, bookmark preface/title, text preface/match/no-match, blank section의 설계 exact filename과 `ReasonCode` allowlist를 table test로 고정한다. output은 basename만 허용한다. request wire decoder는 `ReviewedSeparatorGroup.omit`과 `ReviewedBlankAnalysis.complete`를 임시 pointer로 decode해 missing field를 거부하고, `SmartSplitPlanEdit{Index,Name}` 외 JSON field는 `DisallowUnknownFields`로 거부한다.

- [ ] **7. bounded source session RED/GREEN**

xref 100,001, object-stream `/N` 100,001, direct child 1,000,001, depth 65, token 8MiB+1, parsed string/name 64MiB+1, decoded stream cumulative cap+1과 page 501 fixture를 `NewSmartSplitSession`에 넣는다. 각 case는 `ParseBounded`/`PagesBounded`의 해당 allocation 전에 실패하고 plan/output spool allocation은 0이어야 한다. 여러 bookmark/text plan과 segment output을 순차 실행해 `ReadStats.CachedObjects/ContainerEdges/DecodedStreamBytes`가 session 전체에서 누적되고 plan/output마다 0으로 초기화되지 않으며 cap+1에서 typed error인지 검증한다.

- [ ] **8. focused 검증과 보고**

~~~sh
go test ./pdf -run 'TestPlanPageCount|TestPlanReviewedBlank|TestSmartSplitName|TestSmartSplitBoundedSource|TestParseBounded|TestPagesBounded' -count=1
go test -race ./pdf -run 'TestPlanPageCount|TestPlanReviewedBlank|TestSmartSplitBoundedSource' -count=1
~~~

.superpowers/sdd/smart-split-task-1-report.md에 coverage invariant, bounded source allocation-before-rejection, session-wide `ReadStats`와 RED/GREEN 결과를 기록한다.

### 작업 2: bounded bookmark와 selectable-text rules

**파일:**

- 수정: pdf/smart_split.go
- 수정: pdf/smart_split_test.go
- 수정: pdf/text.go
- 수정: pdf/text_test.go

**인터페이스:**

~~~go
type PageTextLimits struct {
    MaxContentBytes int
    MaxTextBytes    int
}

type PageTextResult struct {
    Text        string
    Complete    bool
    Limitations []SmartSplitWarning
}

func (d *Doc) extractPageTextBounded(page Page, limits PageTextLimits) (PageTextResult, error)
~~~

- [ ] **1. bookmark fixture RED 테스트 작성**

builder로 preface page, direct Dest, GoTo action, Catalog Dests와 Names/Dests named destination, duplicate/backward/remote/unresolved item을 만든다. valid top-level boundaries와 warning code를 검증한다.

- [ ] **2. RED 확인**

실행: go test ./pdf -run 'TestSmartSplitBookmark' -count=1

예상: bookmark rule이 없어 실패한다.

- [ ] **3. bounded outline/name-tree resolver 구현**

top-level First/Next chain을 object visited set, depth 64와 node 10,000으로 순회한다. direct/named local page destination만 page map으로 변환한다. cycle, duplicate, backward, remote와 unresolved destination은 `SmartSplitWarning`으로 반환한다. `Code`는 설계 allowlist만 허용하고 `Page`, `ObjectNumber`, `Location`은 확인된 위치만 채운다.

- [ ] **4. text budget RED 테스트 작성**

content/text 1MiB per page, total 32MiB, unsupported filter, malformed content와 CMap budget fixture를 작성한다. limit hit page가 Complete=false warning이 되고 silent non-match가 아님을 검증한다.

- [ ] **5. bounded page text extraction 구현**

기존 extractPageText 로직을 bounded writer와 decoded-content budget으로 일반화하고 public ExtractText는 기존 behavior를 유지한다. Smart session의 extractor와 outline/name-tree resolver는 source lookup마다 `GetBounded`/`ResolveBounded`를 사용해 같은 `Doc` read/decode counter를 소비한다. text rule은 Go regexp.Compile을 사용하고 pattern bytes 1..1024를 검증한다.

- [ ] **6. text boundary RED/GREEN**

first page match, consecutive matches, no match, prefix-identical 뒤쪽 match, invalid pattern과 image-only empty text를 검증한다. match page는 새 segment 시작이고 empty preface는 만들지 않는다.

- [ ] **7. bookmark/text focused 검증**

~~~sh
go test ./pdf -run 'TestSmartSplitBookmark|TestSmartSplitText|TestExtractPageTextBounded' -count=1
go test -race ./pdf -run 'TestSmartSplitBookmark|TestSmartSplitText' -count=1
go vet ./pdf
~~~

예상: 모두 exit 0.

- [ ] **8. 작업 보고 기록**

.superpowers/sdd/smart-split-task-2-report.md에 supported destination, warning, RE2와 text budget 증거를 기록한다.

### 작업 3: shared bounded serializer와 상태형 WASM chunk session

**파일:**

- 수정: pdf/bounded_output.go (선행 Scan Cleanup 계획에서 생성)
- 수정: pdf/bounded_output_test.go (선행 Scan Cleanup 계획에서 생성)
- 수정: pdf/ops.go
- 수정: pdf/ops_test.go
- 수정: pdf/smart_split.go
- 수정: pdf/smart_split_test.go
- 생성: wasm/smartsplit/main.go
- 생성: wasm/smartsplit/session.go
- 생성: wasm/smartsplit/session_test.go
- 생성: wasm/smartsplit/request.go
- 생성: wasm/smartsplit/request_test.go
- 수정: wasm/jsu/jsu.go
- 수정: web/app.js
- 수정: web/wasm-client.mjs
- 수정: web/wasm-client.test.mjs
- 수정: web/wasm-chunks.mjs
- 수정: web/wasm-chunks.test.mjs
- 생성: web/smartsplit/protocol.mjs
- 생성: web/smartsplit/protocol.test.mjs
- 수정: tests/worker-contract.test.mjs

**인터페이스:**

~~~go
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

type SmartSplitOutputInfo struct {
    OutputRevision uint64 `json:"outputRevision"`
    Name           string `json:"name"`
    Size           uint64 `json:"size"`
}

func NewSmartSplitSession(file []byte, sourceStem string, limits SmartSplitLimits) (*SmartSplitSession, error)
func (s *SmartSplitSession) Plan(rule SmartSplitRule) (SmartSplitPlan, error)
func (s *SmartSplitSession) ReplacePlan(revision uint64, edits []SmartSplitPlanEdit) (SmartSplitPlan, error)
func (s *SmartSplitSession) StartOutput(revision uint64, index int, remainingArchiveBytes uint64) (SmartSplitOutputInfo, error)
func (s *SmartSplitSession) TakeOutputChunk(outputRevision uint64) (chunk []byte, done bool, remaining uint64, err error)
func (s *SmartSplitSession) ReleaseOutputChunk(outputRevision uint64, chunk []byte) error
func (s *SmartSplitSession) ReleaseOutput(outputRevision uint64) error
func (s *SmartSplitSession) Close()
~~~

WASM protocol:

~~~text
sourceStart({size,sourceStem,limits}) -> {sourceRevision,nextOffset:0,maxChunkBytes:1MiB}
sourceChunk(sourceRevision,offset,data<=1MiB) -> {nextOffset,copiedBytes}
sourceFinish(sourceRevision) -> {sessionRevision,pageCount}
plan(rule) -> SmartSplitPlan
replacePlan(revision,edits:[{index,name}]) -> SmartSplitPlan
outputStart(revision,index,remainingArchiveBytes) -> {outputRevision,name,size}
outputRead(outputRevision,maxBytes=1MiB) -> {data:Uint8Array<=1MiB,done,remainingBytes}
outputRelease(outputRevision) -> {state:"released"}
bridgeStats() -> SmartSplitBridgeStats
abort() -> {state:"aborted"}
~~~

- [ ] **1. allocation-before-rejection RED 테스트 작성**

`pdf/bounded_output_test.go`에 exact tracking allocator를 둔다. 선행 Scan Cleanup의 bounded graph tests도 그대로 통과해야 한다.

~~~go
type trackingChunkAllocator struct {
    allocations int
    retained    uint64
    peak        uint64
}

func (a *trackingChunkAllocator) Allocate(size int) []byte {
    a.allocations++
    a.retained += uint64(size)
    if a.retained > a.peak { a.peak = a.retained }
    return make([]byte, size)
}

func (a *trackingChunkAllocator) Release(chunk []byte) {
    a.retained -= uint64(cap(chunk))
}

func TestBoundedChunkSpoolRejectsCapPlusOneBeforeAllocation(t *testing.T) {
    alloc := &trackingChunkAllocator{}
    spool, err := NewBoundedChunkSpool(3, 2, alloc)
    if err != nil { t.Fatal(err) }
    if _, err := spool.Write([]byte{1, 2, 3}); err != nil { t.Fatal(err) }
    before := alloc.allocations
    if _, err := spool.Write([]byte{4}); !errors.Is(err, ErrOutputLimit) {
        t.Fatalf("err = %v, want ErrOutputLimit", err)
    }
    if alloc.allocations != before {
        t.Fatalf("cap+1 allocated %d new chunks", alloc.allocations-before)
    }
}
~~~

`Reader`가 allocation/retained/remaining을 바꾸지 않고 전체 bytes를 읽으며, EOF 전 `Take` 거부, EOF 뒤 첫 `Take`가 같은 첫 chunk를 반환함, 첫 `Take` 뒤 새 Reader의 첫 Read가 `ErrSpoolState`임을 검증한다. `Take` 뒤 `ReleaseOutputChunk`마다 retained가 최대 1MiB씩 감소하고 `Release` 뒤 0이며, `writeTo`가 xref offset을 정확히 쓰고 기존 `b.bytes(root)`와 byte-identical임을 검증한다.

- [ ] **2. RED 확인**

실행: `go test ./pdf -run 'TestBoundedChunkSpool|TestBuilderWriteTo' -count=1`

예상: bounded serializer type과 `writeTo`가 없어 compile 실패한다.

- [ ] **3. shared bounded serializer 구현**

`writeObj`/`writeName`을 error-returning writer path로 일반화하고 `builder.writeTo`가 PDF header, object, xref, trailer를 `BoundedChunkSpool`에 직접 쓴다. spool은 `size+len(p)`를 overflow-safe하게 먼저 검사하고 초과면 allocation/copy 없이 sticky `ErrOutputLimit`을 반환한다. chunk는 최대 1MiB이고 한 번에 checked-out chunk 하나만 허용한다. `Reader`는 chunk payload를 복사하지 않는 private cursor로 spool을 seal하며 head/remaining을 보존한다. Reader EOF 전 Take, Take 뒤 Reader와 seal 뒤 Write는 `ErrSpoolState`다. 기존 `builder.bytes`는 같은 object serializer의 memory writer adapter로 public 결과를 byte-identical하게 보존한다.

- [ ] **4. one-parse/bounded-output RED 테스트 작성**

private test constructor `newSmartSplitSessionWithHooks(file, stem, limits, parse, allocator)`에 counting parse function과 tracking allocator를 주입한다. source session당 `parseCount=1`, 여러 `StartOutput`에서도 parse count가 증가하지 않음, active output 두 개 거부, stale plan/output revision, `remainingArchiveBytes`가 entry hard cap보다 작을 때 그 값을 allocation 전에 적용함을 검증한다. 각 successful output에서 다음 식을 sample한다.

~~~text
spoolRetained + simulatedSinkWritten + bridgeTransient
<= outputSize + ZIPFixedOverhead + PDFBridgeChunkBytes
~~~

- [ ] **5. segment output core 구현**

`StartOutput`은 `min(limits.MaxOutputBytes, remainingArchiveBytes)`로 새 spool을 만들고 selected page를 `buildOrderedBounded`→`writeTo`한다. source access는 `GetBounded`/`ResolveBounded`만 사용해 session-wide read counter를 계속 소비하고, `MaxObjects/MaxEdges/MaxDepth`의 별도 output counter는 builder queue/map/clone append 전에 적용한다. 어느 쪽이든 초과 시 spool allocation은 0이어야 한다. full `[]byte`와 `bytes.Buffer → append` 경로는 만들지 않는다. `TakeOutputChunk`는 chunk ownership을 adapter에 넘기고 다음 take를 막는다. adapter가 Go→JS copy를 끝낸 직후 `ReleaseOutputChunk`를 호출한다. `ReleaseOutput`과 `Close`는 checked-out/remaining chunk를 모두 release하고 retained counter를 0으로 만든다.

- [ ] **6. plan mutation contract RED/GREEN**

`ReplacePlan` request decoder는 `SmartSplitPlanEdit{Index,Name}`만 받고 unknown JSON field, range/reason/omission/separator mutation을 거부한다. filename 성공은 revision을 증가시켜 old output revision을 stale 처리한다. separator group 결정을 바꿀 때는 `Plan(SmartSplitRule{Kind:"blank", BlankAnalysis:&ReviewedBlankAnalysis{Groups:[]ReviewedSeparatorGroup{{Pages:[]int{4,5},Omit:true}},Complete:true,Warnings:[]SmartSplitWarning{}}})`처럼 complete rule을 다시 호출하며 `ReplacePlan`을 호출하지 않는다.

- [ ] **7. source chunk lifecycle RED 테스트 작성**

`wasm/smartsplit/session_test.go`와 `request_test.go`에서 source size 2MiB+17 bytes에 대해 1MiB, 1MiB, 17-byte 순서만 성공하고 zero/oversize chunk, duplicate/gap/backward offset, early finish, overflow, stale source revision을 거부하는지 검증한다. JS number는 safe integer/finite여야 하고 limits의 0은 compiled default, nonzero는 양수·hard cap 이하여야 한다. validation은 source allocation 전에 수행한다. invalid `sourceStart`도 old source/doc/plan/output을 먼저 폐기해야 한다. `SourceStem`은 options에 필수로 전달하되 빈 문자열은 `document`로 정규화한다.

- [ ] **8. WASM direct-copy session 구현**

`sourceStart`가 common `size` field로 최종 Go source slice를 정확한 크기로 한 번 할당한다. `wasm/jsu/jsu.go`에 `CopyBytesInto(dst []byte, value js.Value, maxBytes int) (int,error)`를 추가해 `sourceChunk`가 `jsu.Bytes` 임시 Go slice 없이 destination subslice로 직접 `js.CopyBytesToGo` 한다. `sourceFinish`는 wire limits를 `PDFReadLimits`로 변환해 `ParseBounded`→`PagesBounded`를 한 번만 호출하고 checked `Doc`을 session에 보관한다. `outputRead`는 `maxBytes == PDFBridgeChunkBytes`를 요구하고 `jsu.Out`의 Go→JS copy가 끝난 직후 `ReleaseTaken`으로 checked-out Go chunk를 release한다. 실제 copy 직후 `SmartSplitBridgeStats`의 source/output call·bytes·max transient와 spool retained/peak를 갱신하고 `Doc.ReadStats()`를 nested field로 포함한 read-only `bridgeStats` command로 반환한다. native fake copier test와 actual TinyGo Playwright test가 각 copy 1MiB 이하, 총 source bytes 일치, parser/page/cache ceiling과 release 뒤 retained 0을 같은 field로 검증한다.

- [ ] **9. shared browser client와 `web/wasm-chunks.mjs` RED 테스트**

`web/wasm-client.test.mjs`, `web/wasm-chunks.test.mjs`, `tests/worker-contract.test.mjs`에 다음 behavior를 작성한다.

- `uploadBlobChunks`는 `blob.arrayBuffer()`를 호출하지 않고 exact 1MiB `slice`만 순차 await한다.
- `transferOwnership:true`의 exact-buffer typed array는 실제 Worker post 뒤 caller `ArrayBuffer.byteLength===0`이다. subview는 전체 backing buffer를 잘못 detach하지 않도록 거부한다.
- 완료된 `sourceStart`→여러 `sourceChunk`→`sourceFinish`→`plan`→output command는 Worker instance 하나를 유지한다.
- concurrent command만 active command를 abort하고 Worker를 terminate한다.
- `requireWorker:true`는 Worker unavailable, constructor/load failure, `error`, `messageerror`에서 fallback operation call count 0으로 reject한다.
- client cancel/transport fatal error는 `workerGone===true`이고 `readOutputChunks`가 release를 위해 새 Worker를 만들지 않는다.
- runtime marker가 있는 `/smartsplit/` page는 `boot("./smartsplit.wasm", {requireWorker:true,transferOwnership:true})`를 사용하며 기존 `runWasm` regex-only 경로의 예외가 아니라 shared client contract 대상이다.

- [ ] **10. shared browser client와 chunk helper 구현**

`boot(wasmFile, clientOptions={})`가 options를 `createWasmClient`에 전달하고 기존 `window.runWasm`을 유지한다. 반환값은 기존 `.then/.catch`와 `await`를 보존하는 ready Promise에 non-enumerable `{run,cancel,dispose}`를 추가한 augmented handle이다. ready 전 dispose, 기존 5개 `.then` page와 OCR await regression을 Security Task 3 tests가 고정한다. `dispose` 뒤 다음 `run`은 같은 config의 fresh Worker를 만든다. `createWasmClient(operation,{worker,requireWorker=false,transferOwnership=false})`는 opt-in transfer 시 exact backing buffer를 복사하지 않고 transfer list에 한 번만 넣으며, `requireWorker`에서는 어떤 transport/load failure도 main-thread fallback하지 않는다. `messageerror`도 fatal transport error로 처리하고 terminate가 끝난 cancel/failure error에는 `workerGone=true`를 붙인다. `web/wasm-chunks.mjs`의 기존 Security Audit helper를 다음 interface로 재사용한다.

~~~js
export const WASM_BRIDGE_CHUNK_BYTES = 1024 * 1024;
export const WASM_SOURCE_UPLOAD_COMMANDS = Object.freeze({ start:"sourceStart", chunk:"sourceChunk", finish:"sourceFinish", abort:"abort" });
export async function uploadBlobChunks({ blob, start, commands = WASM_SOURCE_UPLOAD_COMMANDS, run, signal }) {}
export async function* readOutputChunks({ outputRevision, run, signal }) {}
~~~

uploader는 common `size` field와 `Blob.slice` source protocol을 수행한다. output iterable은 이전 yield를 consumer가 resume한 뒤에만 `outputRead({outputRevision,maxBytes:WASM_BRIDGE_CHUNK_BYTES})`를 호출하고 정상·application throw·Worker가 살아 있는 cooperative abort의 `finally`에서 `outputRelease`를 정확히 한 번 호출한다. `workerGone` error는 release를 건너뛰어 새 Worker 생성을 막고 원래 error를 유지한다.

`wasm/smartsplit/main.go`는 positional argument가 아니라 `{command,...payload}` 객체 하나만 받고 `web/smartsplit/protocol.mjs`가 handle 결과의 `{json}` 또는 `{data,json}`를 검증한다. protocol은 `const run = (command,payload={}) => normalize(handle.run({command,...payload}))` adapter를 shared helper에 제공하고 wire `sourceRevision`을 generic `uploadRevision`으로 명시적으로 map한다. source start의 common `size`, output read의 exact 1MiB `maxBytes`, request unknown field, response revision/state mismatch, product error와 transport reject를 구분하는 test를 `web/smartsplit/protocol.test.mjs`에 둔다.

- [ ] **11. lifecycle·TinyGo·race 검증**

~~~sh
go test ./pdf ./wasm/smartsplit -count=1
go test -race ./pdf ./wasm/smartsplit -count=1
node --test web/wasm-client.test.mjs web/wasm-chunks.test.mjs tests/worker-contract.test.mjs
GOOS=js GOARCH=wasm go build ./wasm/smartsplit
tinygo build -target wasm -no-debug -o /tmp/smartsplit.wasm ./wasm/smartsplit
wasm-opt -Oz /tmp/smartsplit.wasm -o /tmp/smartsplit.opt.wasm
wc -c < /tmp/smartsplit.opt.wasm
~~~

예상: 모두 exit 0, optimized WASM byte count가 2,097,152 미만이다.

- [ ] **12. 작업 보고 기록**

`.superpowers/sdd/smart-split-task-3-report.md`에 source parse count, exact bridge copy counts/max chunk, allocator allocation-before-rejection, retained-byte samples, Worker lifecycle와 `wc -c` binary size를 기록한다. logical call-order counter만으로 메모리 완료를 주장하지 않는다.

### 작업 4: plan state와 byte-limited streaming ZIP

**파일:**

- 생성: web/smartsplit/plan-state.mjs
- 생성: web/smartsplit/plan-state.test.mjs
- 생성: web/smartsplit/exporter.mjs
- 생성: web/smartsplit/exporter.test.mjs
- 재사용: web/output-sinks.mjs
- 테스트 재사용: web/output-sinks.test.mjs
- 수정: web/zip.js
- 수정: web/pdf2img/zip.mjs
- 수정: web/pdf2img/zip.test.mjs

**인터페이스:**

~~~js
export function createByteLimitedSink(sink, maxBytes) {}
export function createDelayedCleanupRegistry(options = {}) {}
export function createPlanState(plan, inputIdentity) {}
export function replacePlanState(state, plan) {}
export function zipFixedOverhead(names, nextIndex) {}
export function remainingArchivePayload({ maxBytes, bytesWritten, names, nextIndex }) {}
export async function exportSmartSplit({ plan, handle, run, createSink, zip, signal, isCurrent, onProgress }) {}
~~~

- [ ] **1. Scan shared sink foundation 회귀 확인**

Scan Task 5의 memory, OPFS-like와 directory-like tests가 `createByteLimitedSink`의 pre-write cap, read-only `kind/maxBytes/bytesWritten`, memoized close/abort/cleanup과 `createDelayedCleanupRegistry`의 timer/pagehide exactly-once behavior를 이미 고정한다. `createOutputSink`는 directory→OPFS→memory 순서이고 memory의 effective cap은 `min(requestedArchiveCap,64*1024*1024)`여야 한다.

- [ ] **2. RED 확인**

실행: node --test web/output-sinks.test.mjs web/smartsplit/exporter.test.mjs

예상: Scan shared foundation tests는 exit 0이고 Smart exporter module이 없어 Smart test만 실패한다.

- [ ] **3. Smart sink integration RED 테스트**

fake raw OPFS sink가 `maxBytes`를 노출하지 않아도 exporter가 먼저 256MiB byte wrapper를 만들고 그 값에서 ZIP overhead를 뺀 budget만 `outputStart`에 전달하는지 검증한다. memory는 64MiB다. 성공 close가 Blob/File을 반환하면 anchor click 전에 shared cleanup registry에 등록하고 timer 전 pagehide에서 URL revoke/remove 각 1회, abort 0인지 검증한다. directory close `null`은 download/cleanup registry에 등록하지 않고 remove 0이어야 한다.

- [ ] **4. ZIP fixed-overhead budget RED 테스트**

UTF-8 encoded name마다 local header `30+nameBytes`, data descriptor `16`, central record `46+nameBytes`, archive EOCD `22`를 계산한다. `nextIndex=i`일 때 아직 쓰지 않은 local header/descriptor는 `i..end`, 아직 쓰지 않은 central record는 `0..end`, EOCD는 한 번 예약해야 한다. `remainingArchivePayload`가 `maxBytes - bytesWritten - fixedOverhead`를 overflow-safe하게 반환하고 음수면 `outputStart` 전에 실패하는지 table test로 검증한다.

- [ ] **5. sequential output protocol RED 테스트**

fake `run`은 `outputStart`, 반복 `outputRead`, `outputRelease`와 retained spool bytes를 기록하고 fake sink는 buffered/written bytes를 기록한다. 두 entry의 exact call order가 다음인지 검증한다.

~~~text
outputStart(0,budget0)
outputRead(0,1MiB) -> sink.write(chunk0) -> outputRead(0,1MiB) -> sink.write(chunk1)
outputRelease(0)
outputStart(1,budget1)
outputRead(1,1MiB) -> sink.write(chunk0)
outputRelease(1)
sink.close
~~~

모든 sample에서 `spoolRetained + sinkBuffered + bridgeTransient <= finalArchiveBytes + 1MiB`이고 active output spool과 bridge chunk가 각각 최대 1인지 검증한다.

- [ ] **6. exporter 구현**

current plan revision/name snapshot과 monotonically increasing owner를 만들고 directory/OPFS/64MiB-memory `createSink`→Scan shared byte wrapper→`zipStoreStream` 순서로 연결한다. `isCurrent()`는 input identity와 plan revision/name snapshot이 모두 현재인지 반환하며 모든 await와 sink close 뒤에 검사한다. 각 entry 전에 ZIP fixed overhead를 제외한 `remainingArchiveBytes`로 `outputStart`를 호출하고, `{name,data:readOutputChunks(...)}`를 yield한다. `readOutputChunks`가 chunk write별 backpressure와 `outputRelease`를 담당한다. output 하나의 `Uint8Array`나 output 배열은 만들지 않는다. central directory write와 sink close 뒤에도 signal과 `isCurrent()`를 다시 확인한 뒤에만 완료를 commit한다. memory/OPFS close 결과는 anchor click 전에 shared delayed cleanup registry에 등록하고 pagehide가 `cleanupAll()`을 호출한다. directory success는 사용자 파일을 보존한다. local `{owner,handle,sink,cleanupPromise}`를 보관하고 새 export는 이전 cleanup을 await한다. terminal success/error `finally`는 owner가 아직 current이고 Worker가 responsive일 때만 `abort`를 보내고 해당 handle을 dispose해 WASM linear memory를 폐기한다. old finally와 old partial/closed-sink cleanup은 fresh Worker/sink에 적용되지 않는다. 재분할은 보유 중인 원본 `File`을 새 Worker에 다시 ingest한다.

- [ ] **7. abort/race/error GREEN 테스트**

pre-abort, abort during `outputStart`/`outputRead`/write/central-directory/close, input/filename/separator revision mutation, Worker error/messageerror, sink cleanup failure와 pagehide-equivalent abort를 검증한다. Worker가 응답 가능한 path는 active `outputRelease`가 정확히 한 번 호출되고 retained spool bytes가 0이다. transport fatal path는 release 성공 대신 Worker terminate 1회, fallback call 0과 새 session 필요 상태를 검증한다. terminal success도 Worker terminate 1회이고 다음 export가 새 source ingest부터 시작한다. successful OPFS timer/pagehide/manual cleanup은 URL revoke/remove 각 1회이고 directory remove는 0이다. delayed old abort/dispose/sink removal 또는 captured old timer 뒤 즉시 새 export를 시도해 cleanup 완료 전 새 sourceStart가 없고 fresh handle/sink가 보존되는지 검증한다. stale archive는 download/complete callback을 호출하지 않는다.

- [ ] **8. plan-state validation 구현**

WASM plan의 schema, coverage, filename, typed `SmartSplitWarning{code,page,objectNumber,location}`, exact `ReasonCode` allowlist와 `Complete`를 브라우저에서도 검증한다. blank rule request는 exact `ReviewedBlankAnalysis{groups,complete,warnings}`를 만들고 render/limit failure page를 retained 상태와 incomplete warning으로 보존한다. `SmartSplitPlanEdit`는 exact `{index,name}`만 생성한다. input identity가 다르거나 separator group 결정을 바꿔 `plan(rule)` revision이 갱신되면 이전 plan/export를 사용할 수 없다.

- [ ] **9. focused 검증과 보고**

~~~sh
node --test web/output-sinks.test.mjs web/smartsplit/*.test.mjs web/pdf2img/zip.test.mjs web/wasm-chunks.test.mjs
node --check web/smartsplit/exporter.mjs
~~~

`.superpowers/sdd/smart-split-task-4-report.md`에 exact output call order, ZIP overhead budget, sink별 cap, successful OPFS URL/pagehide exactly-once cleanup, actual copy count와 retained-byte peak/release를 기록한다.

### 작업 5: 규칙·검토 UI와 실제 E2E

**파일:**

- 생성: web/smartsplit/index.html
- 생성: web/smartsplit/smartsplit.mjs
- 생성: web/smartsplit/smartsplit.css
- 생성: tests/e2e/smartsplit.spec.mjs
- 재사용: web/page-analysis.mjs
- 재사용: web/page-pixels.mjs

- [ ] **1. source page와 accessibility RED E2E 작성**

PDF dropzone, rule radios/select, page-count/pattern fields, Analyze/Cancel, completeness warning, segment list, filename edit, blank group keep/drop, retained/omitted counts, Export ZIP와 live regions를 keyboard로 사용할 수 있어야 한다. 연속 separator group은 하나의 control만 가지며 group 안 page별 mixed 결정 UI를 만들지 않는다.

- [ ] **2. stateful client orchestration 구현**

`window.smartSplitWasm = boot("./smartsplit.wasm", {requireWorker:true,transferOwnership:true})` augmented handle로 Worker 하나를 열고 `web/smartsplit/protocol.mjs`의 normalized `run`을 사용한다. `uploadBlobChunks({blob,start:{sourceStem,limits},run:protocol.run,signal})`의 common `size` sourceStart→1MiB sourceChunk→sourceFinish 뒤 plan/output command를 순차 호출한다. 원본 filename의 마지막 `.pdf`를 제거한 stem을 start options에 넣는다. input/rule/pattern mutation은 owned cleanup을 거쳐 session을 abort한다. blank rule만 PDF.js analysis를 먼저 끝내고 renderer를 destroy한 뒤 chunk upload를 시작하며 render/limit failure는 `BlankAnalysis.Complete:false` warning으로 보존한다. separator group keep/drop은 새 `plan(rule)`을 호출하고 filename edit만 `replacePlan(revision,[{index,name}])`을 호출한다.

- [ ] **3. 네 규칙 실제 WASM E2E**

synthetic PDF로 page-count, direct/named top-level bookmark, selectable text match와 high-confidence blank separator group을 실행한다. leading kept group은 첫 segment, interior/trailing kept group은 앞 segment에 들어가고 omitted group은 실제 output page 배열에서 빠지는지 확인한다. low/inconclusive blank는 default keep이고 boundary group으로 전달되지 않는다. 한 page render failure는 `blank-analysis-incomplete`, plan `Complete:false`, 해당 page retained를 실제 WASM response에서 확인한다. 네 rule의 exact default filename과 `ReasonCode`를 모두 assert한다.

- [ ] **4. ZIP 결과 검증 E2E**

download ZIP central directory의 filename, entry 수와 CRC를 읽고 각 entry bytes를 `/vendor/pdfjs/pdf.mjs`의 `getDocument({data})`로 열어 page count, 순서와 selectable text를 확인한다. source `Quarterly Report.pdf`가 `Quarterly Report-pages-0001-0010.pdf` seed를 만드는지 검증한다. 500-page source의 page-count 1 plan이 500 segment/500 ZIP entry를 만들 때 `bridgeStats()`와 sink counter로 active output spool 1, bridge chunk 1이고 `Go spool retained + sink written payload`가 final archive+1MiB bound 안인지 검사한다. nested `readStats`는 500 output 전체에서 session-wide 누적되지만 xref/object/edge/depth/token/parsed/decode/page ceiling 안이어야 한다.

- [ ] **5. loss·stale·cleanup E2E**

signature/page-indexed structure warning, input 교체 후 old export disabled, delayed sink close cancel, archive remaining budget보다 큰 output의 pre-allocation rejection, memory fallback 64MiB rejection, Worker fatal error 뒤 새 session, pagehide active output release/partial cleanup과 encrypted input 오류를 검증한다.

- [ ] **6. locale·offline E2E**

한국어와 비영어 generated URL의 absolute module/CSS/WASM, translated rule/warning/error를 확인하고 첫 online load 뒤 offline page-count·blank flow를 실행한다.

- [ ] **7. focused 검증과 보고**

~~~sh
node --test web/smartsplit/*.test.mjs web/page-analysis.test.mjs web/page-pixels.test.mjs
tinygo build -target wasm -no-debug -o /tmp/smartsplit-e2e.wasm ./wasm/smartsplit
wasm-opt -Oz /tmp/smartsplit-e2e.wasm -o web/smartsplit/smartsplit.wasm
npx playwright test tests/e2e/smartsplit.spec.mjs --project=chromium
npx playwright test tests/e2e/smartsplit.spec.mjs --project=firefox
npx playwright test tests/e2e/smartsplit.spec.mjs --project=webkit
npx playwright test tests/e2e/smartsplit.spec.mjs --project=mobile-chromium
npx playwright test tests/e2e/smartsplit.spec.mjs --project=mobile-webkit
node --check web/smartsplit/smartsplit.mjs
git diff --check
~~~

`.superpowers/sdd/smart-split-task-5-report.md`에 네 rule, reviewed group의 exact output page arrays, ZIP semantic, five Playwright project 결과와 retained/copy counter를 기록한다.

### 작업 6: catalog·다국어·문서·최종 release 통합

**파일:**

- 수정: tools/operation-catalog.json
- 수정: web/operation-catalog.json
- 수정: web/index.html
- 수정: tools/meta-i18n.json
- 수정: web/i18n/{ja,zh,es,fr,de}.js
- 수정: tests/operation-catalog.test.mjs
- 수정: tests/release-integration.test.mjs
- 수정: README.md
- 수정: README.ko.md
- 수정: web/llms.txt
- 수정: web/manifest.webmanifest
- 생성물: web/{ko,ja,zh,es,fr,de}/smartsplit/index.html
- 생성물: web/sitemap.xml

**선행 상태:** securityaudit와 scancleanup 완료 후 catalog 44, WASM 39, visible 43
**최종 수치:** catalog 45, WASM 40, visible tool 44

- [ ] **1. release RED 테스트 수정**

catalog/count를 45/40/44로 바꾸고 landing/README/llms/manifest, 7-language metadata, absolute assets, module translation key, generated pages와 required-stateful Worker runtime contract를 요구한다.

- [ ] **2. descriptor와 product copy 추가**

~~~json
{"id":"smartsplit","engine":"wasm","entry":"/smartsplit/smartsplit.wasm","runtime":{"worker":"required","stateful":true,"chunkedIO":true},"input":{"kind":"pdf","min":1,"max":1},"output":{"kind":"zip"},"capabilities":{"preview":false,"pipeline":false,"batch":false,"terminal":true},"build":{"package":"./wasm/smartsplit","output":"web/smartsplit/smartsplit.wasm"}}
~~~

landing card, ItemList, metadata, README, llms와 manifest를 44 visible tools로 갱신한다. 네 rule과 구조 손실·서명 무효화를 설명한다.
`tests/worker-contract.test.mjs`는 runtime marker가 있는 entry가 generated page에서 shared `boot`의 `requireWorker:true`, `transferOwnership:true` options를 사용하고 sequential stateful behavior test 대상임을 검증한다.

- [ ] **3. dictionary와 generator GREEN**

새 HTML/JS English key를 다섯 dictionary에 번역하고 다음을 실행한다.

~~~sh
cp tools/operation-catalog.json web/operation-catalog.json
node tools/gen-i18n.mjs
~~~

예상: 0 missing I18N keys encountered, generated page 수 276.

- [ ] **4. 전체 기능 검증**

~~~sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
node --test
GOOS=js GOARCH=wasm go build ./wasm/...
JOBS=1 ./build.sh
node tools/check-wasm-size.mjs web
node tools/verify-vendor.mjs
npx playwright test tests/e2e/securityaudit.spec.mjs tests/e2e/scancleanup.spec.mjs tests/e2e/smartsplit.spec.mjs --project=chromium
npx playwright test tests/e2e/securityaudit.spec.mjs tests/e2e/scancleanup.spec.mjs tests/e2e/smartsplit.spec.mjs --project=firefox
npx playwright test tests/e2e/securityaudit.spec.mjs tests/e2e/scancleanup.spec.mjs tests/e2e/smartsplit.spec.mjs --project=webkit
npx playwright test tests/e2e/securityaudit.spec.mjs tests/e2e/scancleanup.spec.mjs tests/e2e/smartsplit.spec.mjs --project=mobile-chromium
npx playwright test tests/e2e/securityaudit.spec.mjs tests/e2e/scancleanup.spec.mjs tests/e2e/smartsplit.spec.mjs --project=mobile-webkit
git diff --check
~~~

예상: 모두 exit 0, i18n warning 0, WASM size gate 통과.

- [ ] **5. 최종 작업 보고 기록**

`.superpowers/sdd/smart-split-task-6-report.md`에 final counts, generated pages, `wc -c` binary sizes, five Playwright project 결과, tests와 명시적 정확도 한계를 기록한다. commit/push는 하지 않는다.

## 계획 자체 검토

- page-count, bookmark, selectable text, reviewed blank의 네 rule이 모두 core·UI·E2E에 연결된다.
- 1MiB source direct ingest, source one parse와 one-bounded-output-at-a-time invariant가 Go allocator/copy counter와 JS ZIP backpressure test에 모두 있다.
- source one parse는 `ParseBounded/GetBounded/ResolveBounded/PagesBounded`만 사용하며 session-wide `ReadStats`가 여러 plan/output의 parser/page-tree/cache 누적을 cap 안으로 고정한다.
- SmartSplitPlan revision과 input identity가 session, UI와 exporter에서 일관된다.
- separator group decision은 `plan(rule)`, exact filename edit `{index,name}`은 `replacePlan`만 사용하고 kept group 귀속이 actual output page array로 고정된다.
- output은 archive remaining budget으로 allocation 전에 제한되고 spool retained+sink written+bridge transient 경계를 instrumented test로 증명한다.
- Scan의 byte-limit sink와 successful OPFS URL/timer/pagehide cleanup registry를 재사용해 cap과 exactly-once remove/revoke를 보장하고 directory success file은 보존한다.
- scan cleanup의 page-analysis 재사용 시 PDF.js를 완전히 닫고 Go session을 시작하는 경계가 명시됐다.
- runtime worker marker, no-fallback shared client와 `web/wasm-chunks.mjs` protocol이 worker-contract 및 browser detachment test에 연결된다.
- 최종 catalog 45, WASM 40, visible 44와 generated 276 page가 순차 계획 수치와 맞는다.
- 미정 type/API, 외부 Go dependency, commit/push 단계가 없다.
