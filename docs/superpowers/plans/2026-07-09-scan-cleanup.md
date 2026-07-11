# 스캔 문서 정리 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**목표:** PDF 스캔을 분석해 검토된 quarter-turn·빈 페이지 제거·CropBox를 원본 stream 보존 방식으로 적용하고, 별도 raster-only 모드에서 작은 각도 deskew와 실제 border pixel 제거를 제공한다.

**구조:** Go page-plan core와 PDF.js 기반 bounded page analyzer를 공유한다. 보존형은 bounded writer로 한 번 rewrite하고, 래스터형은 PNG/JPEG를 받는 상태형 RasterPDFEncoder로 페이지 하나씩 출력한다. 두 경로 모두 1MiB 입력 청크와 소비 즉시 해제되는 Go chunk spool을 사용해 전체 결과의 Go→JS 사본을 만들지 않는다.

**기술:** Go 표준 라이브러리 image/png·image/jpeg, TinyGo WASM, PDF.js, vendored Tesseract orientation 보조 신호, Canvas, vanilla ES modules, Node test runner, Playwright

## 전역 제약

- 설계 기준은 docs/superpowers/specs/2026-07-09-scan-cleanup-design.md다.
- Go 외부 모듈, go.mod/go.sum 변경, commit, push를 금지한다.
- 입력 256MiB, 500페이지, xref/imported object 각 100,000개, graph edge 1,000,000개, direct-container depth 64, token 8MiB, parsed string/name 64MiB, decoded stream 개별 64MiB·누적 256MiB, 분석 2,000,000픽셀/page, live RGBA 16,000,000픽셀, 출력 16,000,000픽셀/page, 누적 64,000,000픽셀, 최종 256MiB다.
- 불확실·분석 불가 페이지의 기본은 keep이며 사용자 확인 없는 삭제·crop·rotation을 금지한다.
- 보존형 CropBox는 물리 삭제라고 표현하지 않는다.
- 래스터형은 text/vector/link/form/metadata/signature 손실을 실행 전에 확인시킨다.
- 모든 page/render/export는 concurrency 1이다.
- 보존형 source와 raster page bytes는 전용 Worker에 1MiB 이하 slice를 ownership-transfer하고 main-thread fallback·사전 전체 복사를 금지한다. ownership-transfer는 main-thread 사본만 제거하며 JS→Go 복사는 destination에 직접 한 번 수행한다고 명시한다.
- output은 1MiB Go spool chunk를 하나씩 JS와 sink로 이동하고 소비한 Go chunk를 즉시 해제한다. `bytes.Buffer -> append -> jsu.Out` 전체 결과 복사를 금지한다.
- memory sink hard cap은 64MiB이고 최종 serializer/sink cap은 256MiB다.
- 검증 결과는 .superpowers/sdd/ report에 기록하고 commit/push는 실행하지 않는다.

---

### 작업 1: 원자적 Go page-plan

**파일:**

- 생성: pdf/pageplan.go
- 생성: pdf/pageplan_test.go
- 생성: pdf/bounded_output.go
- 생성: pdf/bounded_output_test.go
- 재사용: pdf/read_limits.go
- 수정: pdf/read_limits_test.go
- 수정: pdf/geometry.go
- 수정: pdf/ops.go

**인터페이스:**

~~~go
type Rect struct {
    X0 float64 `json:"x0"`
    Y0 float64 `json:"y0"`
    X1 float64 `json:"x1"`
    Y1 float64 `json:"y1"`
}

type PagePlanEdit struct {
    Page         int   `json:"page"`
    Keep         bool  `json:"keep"`
    QuarterTurns int   `json:"quarterTurns"`
    CropBox      *Rect `json:"cropBox,omitempty"`
}

type PagePlanLimits struct {
    MaxInputBytes              uint64 `json:"maxInputBytes"`
    MaxOutputBytes             uint64 `json:"maxOutputBytes"`
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

func ApplyPagePlan(file []byte, edits []PagePlanEdit) ([]byte, error)
func WritePagePlan(dst *BoundedChunkSpool, file []byte, edits []PagePlanEdit, limits PagePlanLimits) (uint64, error)

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

type BoundedChunkSpool struct {
    limit        uint64
    size         uint64
    remaining    uint64
    chunkBytes   int
    chunks       [][]byte
    used         []int
    head         int
    checkedOut   []byte
    allocator    ChunkAllocator
    stickyError  error
    sealed       bool
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

type BoundedGraphLimits struct {
    MaxObjects int
    MaxEdges   int
    MaxDepth   int
}

func (d *Doc) reachableBounded(roots []int, limits BoundedGraphLimits) ([]int, error)
func (b *builder) importDocBounded(d *Doc, roots []int, limits BoundedGraphLimits) (map[int]Ref, error)
func buildOrderedBounded(docs []*Doc, order []pageSel, mut pageMutator, limits BoundedGraphLimits) (*builder, Ref, error)
func (b *builder) finalizeBounded(root Ref, limits BoundedGraphLimits) (Ref, error)
~~~

- [ ] **1. plan validation RED 테스트 작성**

~~~go
func TestApplyPagePlanRejectsInvalidCoverage(t *testing.T) {
    input := classicPDF()
    cases := [][]PagePlanEdit{
        nil,
        {{Page: 2, Keep: true}},
        {{Page: 1, Keep: false}, {Page: 2, Keep: false}},
        {{Page: 1, Keep: true, QuarterTurns: 4}, {Page: 2, Keep: true}},
    }
    for _, edits := range cases {
        if _, err := ApplyPagePlan(input, edits); err == nil {
            t.Fatalf("ApplyPagePlan accepted %#v", edits)
        }
    }
}
~~~

- [ ] **2. RED 확인**

실행: go test ./pdf -run '^TestApplyPagePlan' -count=1

예상: undefined: ApplyPagePlan로 실패한다.

- [ ] **3. validation과 immutable geometry 구현**

limit의 0은 input/output 256MiB, page 500, xref/object 100,000, edge 1,000,000, depth 64, token 8MiB, parsed string/name 64MiB, decoded stream 개별 64MiB·누적 256MiB default이고 nonzero는 양수·hard cap 이하여야 하며 source parse/build 전에 검증한다. edit 길이와 1-based 연속 Page, QuarterTurns 0..3, 하나 이상 Keep, `Rect{x0,y0,x1,y1}`의 finite positive area와 inherited effective source box containment를 검증한다. 입력 Doc의 page Dict를 직접 mutate하지 않고 builder mutator에 translated crop/rotate 값을 적용한다.

- [ ] **4. preserved stream RED 테스트 작성**

유지한 page content와 image stream의 encoded Data SHA-256를 입력/출력에서 비교한다. crop/rotate만 한 page는 stream bytes가 동일해야 한다.

- [ ] **5. bounded serializer와 chunk spool RED 테스트**

`PDFBridgeChunkBytes` 단위 spool에 cap 직전까지 쓴 뒤 cap+1 write가 추가 allocation 전에 `ErrOutputLimit`로 실패하고, `Take/ReleaseTaken(data)` 뒤 `Remaining`과 allocator live bytes가 정확히 줄며 `Release`가 unread/taken chunk를 해제하는 test를 작성한다. `Reader`는 첫 `Take` 전에만 허용되고 output을 복사하지 않아야 하며 Reader EOF 전 Take와 drain 뒤 Reader는 `ErrSpoolState`여야 한다. `builder.writeTo(root, spool)`에 tracking allocator를 연결해 큰 source stream도 별도 full result `bytes.Buffer` 없이 순차 write되고 첫 spool error가 반환되는지 검증한다.

source fixture는 xref 100,001번째, object-stream `/N` 100,001, token 8MiB+1, parsed bytes 64MiB+1, page-tree page 501/depth 65를 만들고 `ParseBounded`/`PagesBounded`가 map/slice/string/decode/page append 전에 실패하며 tracking chunk allocator allocation count가 0인지 검증한다. 여러 page/object lookup의 `ReadStats`가 session 전체에서 누적되고 한도 안인지 확인한다.

`reachableBounded/importDocBounded/finalizeBounded` fixture는 별도 output counter의 100,001번째 object, 1,000,001번째 Ref/direct-container child와 65단계 dict/array를 만든다. source access는 `GetBounded`/`ResolveBounded`만 사용하고 오류를 전파한다. queue/map/clone append와 recursive frame 전에 각각 실패하고 tracking chunk allocator allocation count가 0인지 검증한다. 순환 Ref는 visited로 한 번만 세고 direct dict/array traversal은 explicit stack 또는 checked depth로 stack overflow를 막는다.

- [ ] **6. streaming serializer와 buildOrdered 기반 atomic apply 구현**

`writeObj`/`writeName`을 first-error를 보존하는 writer 경로로 일반화하고 `builder.writeTo`는 xref offset을 spool 누적 byte 수로 계산한다. 기존 byte-return API는 같은 serializer를 사용한다. `WritePagePlan`은 input/read/output-graph/destination spool limit을 serialization 전에 해석하고 `ParseBounded`→`PagesBounded`→bounded import/finalize만 사용한다. source `PDFReadLimits`와 output `BoundedGraphLimits`는 같은 ceiling을 별도 counter로 적용한다. 모든 keep page를 한 번 선택하고 page mutator가 effective rotation에 quarter-turn을 더해 0..359로 normalize하고 validated CropBox를 기록한다. 여러 기존 Rotate/Crop/Remove 호출을 연쇄하지 않는다.

- [ ] **7. inherited geometry와 topology GREEN 테스트**

non-zero MediaBox, inherited CropBox, existing 90/180/270 rotation, one-page removal, page annotation, AcroForm/Outlines/Names 존재 fixture에서 결과 page geometry와 설계에 명시한 preserved/dropped key를 검증한다.

- [ ] **8. focused 검증**

~~~sh
go test ./pdf -run 'TestApplyPagePlan|TestWritePagePlan|TestBoundedChunkSpool|TestPageVisualGeometry|TestBuildOrdered|TestParseBounded|TestPagesBounded' -count=1
go test -race ./pdf -run 'TestApplyPagePlan|TestWritePagePlan|TestBoundedChunkSpool|TestPageVisualGeometry|TestParseBounded|TestPagesBounded' -count=1
go vet ./pdf
~~~

예상: 모두 exit 0.

- [ ] **9. 작업 보고 기록**

.superpowers/sdd/scan-cleanup-task-1-report.md에 stream identity, geometry, topology, source `ReadStats`, allocation-before-rejection과 allocator/spool retained-byte 결과를 기록한다. commit/push는 하지 않는다.

### 작업 2: PNG/JPEG raster encoder와 scancleanup WASM

**파일:**

- 수정: pdf/rasterpdf.go
- 수정: pdf/rasterpdf_test.go
- 수정: pdf/bounded_output.go
- 수정: pdf/bounded_output_test.go
- 수정: wasm/redact/main.go
- 수정: wasm/redact/session.go
- 수정: web/redact/exporter.mjs
- 수정: web/redact/exporter.test.mjs
- 생성: wasm/scancleanup/main.go
- 생성: wasm/scancleanup/session.go
- 생성: wasm/scancleanup/session_test.go
- 생성: wasm/scancleanup/request.go
- 생성: wasm/scancleanup/request_test.go
- 생성: web/scancleanup/protocol.mjs
- 생성: web/scancleanup/protocol.test.mjs

**인터페이스:**

~~~go
type RasterPage struct {
    ImageData []byte  `json:"imageData"`
    Format    string  `json:"format"`
    WidthPt   float64 `json:"widthPt"`
    HeightPt  float64 `json:"heightPt"`
}

type RasterPDFOpts struct {
    MaxPages          int    `json:"maxPages"`
    MaxPagePixels     uint64 `json:"maxPagePixels"`
    MaxPixels         uint64 `json:"maxPixels"`
    MaxPageImageBytes uint64 `json:"maxPageImageBytes"`
    MaxImageBytes     uint64 `json:"maxImageBytes"`
    MaxOutputBytes    uint64 `json:"maxOutputBytes"`
}

type ScanCleanupBridgeStats struct {
    SourceCopyCalls       uint64 `json:"sourceCopyCalls"`
    SourceCopiedBytes     uint64 `json:"sourceCopiedBytes"`
    PageCopyCalls         uint64 `json:"pageCopyCalls"`
    PageCopiedBytes       uint64 `json:"pageCopiedBytes"`
    OutputCopyCalls       uint64 `json:"outputCopyCalls"`
    OutputCopiedBytes     uint64 `json:"outputCopiedBytes"`
    MaxTransientCopyBytes uint64 `json:"maxTransientCopyBytes"`
    SourceRetainedBytes   uint64 `json:"sourceRetainedBytes"`
    PageRetainedBytes     uint64 `json:"pageRetainedBytes"`
    SpoolRetainedBytes    uint64 `json:"spoolRetainedBytes"`
    PeakSpoolRetained     uint64 `json:"peakSpoolRetainedBytes"`
    ReadStats             PDFReadStats `json:"readStats"`
}
~~~

WASM command는 모든 payload를 최대 1MiB 청크로 전달한다. 아래 표기는 shorthand이고 실제 `pdfRun`은 `{command,...payload}` 객체 하나만 받는다.

~~~text
sourceStart({size, limits}) -> {revision,maxChunkBytes}
sourceChunk(revision, offset, data) -> {received}
sourceFinish(revision) -> {state:"ready"}
applyPlan(revision, editsJSON) -> {outputRevision,size}
rasterStart({pageCount, opts}) -> {revision,maxChunkBytes}
pageStart(revision, {size, format, widthPt, heightPt}) -> {state:"receiving"}
pageChunk(revision, offset, data) -> {received}
pageFinish(revision) -> {pagesAdded}
rasterFinish(revision) -> {outputRevision,size}
outputRead(outputRevision, maxBytes=1MiB) -> {data,done}
outputRelease(outputRevision) -> {state:"released"}
bridgeStats() -> ScanCleanupBridgeStats
abort() -> {state:"aborted"}
~~~

- [ ] **1. JPEG raw-embed RED 테스트 작성**

브라우저와 같은 opaque RGB JPEG fixture를 Go image/jpeg로 만들고 AddPage 후 output image stream이 /DCTDecode이며 compressed Data가 입력 JPEG와 byte-identical인지 검증한다.

- [ ] **2. RED 확인**

실행: go test ./pdf -run 'TestRasterPDFEncoderJPEG' -count=1

예상: RasterPage에 ImageData/Format이 없어 compile 실패한다.

- [ ] **3. generic image model로 migration**

RasterPage와 RasterPDFOpts를 위 인터페이스로 바꾸고 redaction bridge/exporter가 format png와 새 field 이름을 보내도록 같은 작업에서 수정한다. legacy PNG behavior와 redaction canary tests는 유지한다.

- [ ] **4. PNG/JPEG validation 구현**

PNG는 DecodeConfig, 8-bit 제한, alpha white composite와 RGB Flate를 유지한다. JPEG는 jpeg.DecodeConfig로 positive dimension과 YCbCr/RGB-compatible model을 확인하고 raw bytes를 DeviceRGB DCTDecode stream으로 기록한다. 두 format은 page/total image bytes, pixels와 output estimate를 같은 checked arithmetic으로 제한한다.

- [ ] **5. direct writer와 streaming validator RED/GREEN**

`RasterPDFEncoder.writeObject`가 `rasterObjectBytes`로 object 전체를 만들지 않고 header/dictionary/stream/trailer를 destination writer에 직접 기록하게 한다. `ValidateRasterOnlyPDFStream(io.Reader, expectedPages, limits)`가 canonical object 번호·generation, exact `/Size`, xref offset과 gap, 허용 DeviceRGB FlateDecode/DCTDecode image, stream length와 source canary 부재를 bounded read로 검증하게 한다. malformed JPEG, grayscale/CMYK unsupported model, dimension overflow, page/total image budget, cap+1 writer, mixed PNG/JPEG page와 corrupt xref fixture를 작성한다.

- [ ] **6. chunk protocol request와 session RED 테스트**

source/page declared size, strict sequential offset, 1MiB chunk, exact finish length, applyPlan JSON의 lowerCamel exact page/limits field·unknown field 거부·finite geometry·source 256MiB와 raster lifecycle을 host Go tests로 작성한다. pre-start chunk, duplicate finish, page-before-raster-start, fatal page/serialize 후 session 제거, stale output revision과 duplicate release를 검증한다. injected bridge copy/retention counter는 `ScanCleanupBridgeStats`와 같은 field를 사용하고 `source + output + current page/chunk`를 넘지 않아야 하며 outputRead마다 Go spool retained bytes가 감소해야 한다.

- [ ] **7. scancleanup WASM 구현**

request.go는 build tag 없이 `WritePagePlan`, `RasterPDFEncoder`, `BoundedChunkSpool`과 session manager를 제공한다. main.go는 request object의 command/unknown field/typed array/safe integer를 검증하고 input chunk를 `jsu.Bytes` 임시 slice 없이 preallocated source/page 범위에 `js.CopyBytesToGo`로 직접 복사한다. vector `sourceFinish`는 wire limits를 `PDFReadLimits`로 해석해 `ParseBounded`→`PagesBounded`를 한 번 수행하고 그 checked `Doc`만 session에 보관한다. output은 `outputRead`의 `maxBytes == PDFBridgeChunkBytes`를 요구하고 chunk를 `Take`해 `js.CopyBytesToJS`한 직후 `ReleaseTaken(data)`를 호출한다. 실제 copy 직후 stats call/byte/maxTransient를, source/apply/pageFinish/output release 때 retained field를 갱신하고 `Doc.ReadStats()`를 nested field로 포함한 read-only `bridgeStats`를 반환한다. `web/scancleanup/protocol.mjs`는 `{json}` 또는 `{data,json}`의 revision/state를 검증하고 shared helper용 `run(command,payload)` adapter를 만든다. 새 start는 이전 session을 abort하며 `rasterFinish`는 `ValidateRasterOnlyPDFStream(spool.Reader(), ...)`가 EOF까지 끝난 뒤에만 output revision을 반환한다. full output `append`나 `jsu.Out`을 사용하지 않는다.

- [ ] **8. redaction regression과 TinyGo 검증**

~~~sh
go test ./pdf ./wasm/redact ./wasm/scancleanup -count=1
go test -race ./pdf ./wasm/redact ./wasm/scancleanup -count=1
GOOS=js GOARCH=wasm go build ./wasm/redact ./wasm/scancleanup
tinygo build -target wasm -no-debug -o /tmp/scancleanup.wasm ./wasm/scancleanup
wasm-opt -Oz /tmp/scancleanup.wasm -o /tmp/scancleanup.opt.wasm
wc -c < /tmp/scancleanup.opt.wasm
~~~

예상: 모두 exit 0, optimized WASM 2MiB 미만.

actual TinyGo Playwright fixture는 vector source 2.5MiB와 두 raster page Blob을 ingest하고 output을 끝까지 drain한다. browser detachment/copy counter와 `bridgeStats`의 source/page/output byte 합계, 각 max transient <=1MiB, apply 뒤 source retained 0, pageFinish 뒤 page retained 0, outputRelease 뒤 spool retained 0을 exact assert한다. vector fixture의 nested `readStats`도 xref/object/edge/depth/token/parsed/decode/page ceiling 안이고 terminal Worker dispose 전까지 session-wide 누적값이 감소한 것처럼 보고되지 않아야 한다.

- [ ] **9. 작업 보고 기록**

.superpowers/sdd/scan-cleanup-task-2-report.md에 PNG/JPEG direct-write, streaming validator, bridge copy/retained-byte와 redaction compatibility 증거를 기록한다. commit/push는 하지 않는다.

### 작업 3: 순수 픽셀 분석기

**파일:**

- 생성: web/page-pixels.mjs
- 생성: web/page-pixels.test.mjs

**인터페이스:**

~~~js
export function analyzePagePixels(image, options = {}) {}
export function blankObservation(luminance, width, height, options = {}) {}
export function backgroundObservation(image, width, height, options = {}) {}
export function edgeBandObservation(luminance, width, height, options = {}) {}
export function skewObservation(luminance, width, height, options = {}) {}
export function normalizeNeutralBackground(image, observation) {}
~~~

- [ ] **1. synthetic fixture와 RED 테스트 작성**

순백, neutral gray paper, colored paper, off-white, 한 개의 작은 점, faint gray text, black scanner band, intentional frame, photo-like noise, horizontal lines와 0/90/180/270 및 -5/-2/0/2/5도 fixture를 Uint8ClampedArray로 만든다.

~~~js
test("inconclusive faint content is retained rather than classified blank", () => {
  const image = faintTextFixture(400, 300);
  const result = analyzePagePixels(image);
  assert.notEqual(result.blank.confidence, "high");
  assert.ok(result.inconclusiveReasons.length > 0);
});
~~~

- [ ] **2. RED 확인**

실행: node --test web/page-pixels.test.mjs

예상: module이 없어 실패한다.

- [ ] **3. checked input·luminance 구현**

width/height safe integer, exact RGBA length, 최대 2,000,000픽셀과 finite options를 먼저 검증한다. luminance buffer는 한 개만 만들고 반환 결과에 pixel buffer를 포함하지 않는다.

- [ ] **4. blank·background와 edge band 구현**

border/low-gradient sample로 background luminance/confidence/neutral을 추정하고 confirmed edge band를 제외한 interior ink/variance를 계산한다. channel range가 12를 넘거나 sample variance·saturation threshold를 넘으면 neutral=false다. high blank는 충분한 background sample과 매우 낮은 ink일 때만 반환한다. frame/photo/faint mark/colored paper는 stable inconclusive reason을 가진다.

- [ ] **5. quarter-turn과 skew 구현**

projection score를 bounded sample과 -5..5 degree grid로 계산한다. second-best와 차이가 threshold보다 작으면 confidence low다. 180도는 pixel score만으로 high가 될 수 없게 한다.

- [ ] **6. opt-in background normalization RED/GREEN**

기본 disabled에서는 input과 byte-identical이어야 한다. eligible neutral background에만 `Y >= B-32`, channel range <=12인 pixel을 `t=clamp((Y-(B-32))/32,0,1)`, `lift=round((255-Y)*0.60*t)`로 밝히고 dark stroke·alpha는 byte-identical이어야 한다. colored paper, photo_like, faint_content와 low confidence는 enabled 요청도 명시적 ineligible result로 돌려 원본을 유지하는 test를 작성한다.

- [ ] **7. property·budget GREEN 테스트**

무작위 dimension/invalid length/NaN option, result normalized bounds, deterministic output과 input non-mutation을 검증한다.

- [ ] **8. focused 검증과 보고**

~~~sh
node --test web/page-pixels.test.mjs
node --check web/page-pixels.mjs
~~~

.superpowers/sdd/scan-cleanup-task-3-report.md에 fixture별 관측값, background 전후 pixel과 false-delete/false-normalize 방지 근거를 기록한다.

### 작업 4: bounded sequential page-analysis orchestration

**파일:**

- 생성: web/page-analysis.mjs
- 생성: web/page-analysis.test.mjs
- 재사용: web/pdf-renderer.mjs
- 재사용: web/ocr/budget.mjs

**인터페이스:**

~~~js
export const PAGE_ANALYSIS_LIMITS = Object.freeze({
  maxInputBytes: 256 * 1024 * 1024,
  maxPages: 500,
  maxAnalysisPixels: 2_000_000,
  maxLivePixels: 16_000_000,
  maxDimension: 16_384,
});

export async function analyzePDFPages({
  file, renderer, analyzePixels, orientationClient, signal, onProgress, limits,
  createObjectURL, revokeObjectURL,
}) {}
~~~

- [ ] **1. sequential lifecycle RED 테스트 작성**

fake renderer가 live page/canvas/ImageData counter를 기록하게 하고 500페이지에서 max live page가 1, 다음 getPage가 이전 dispose 후 호출되며 abort에서 document destroy가 한 번 실행되는지 검증한다.

- [ ] **2. RED 확인**

실행: node --test web/page-analysis.test.mjs

예상: module이 없어 실패한다.

- [ ] **3. File/Blob open·URL lifecycle RED/GREEN**

pre-aborted signal과 256MiB file size는 URL 생성 전 실패해야 한다. `createObjectURL(file)`을 정확히 한 번 호출하고 `renderer.open({url})`로 연다. success, open/render error, cancel과 pagehide-equivalent cleanup에서 renderer destroy가 끝난 뒤 같은 URL을 정확히 한 번 revoke한다. `file.arrayBuffer()`는 호출하지 않고 URL API 부재는 unsupported 오류다.

- [ ] **4. source/page budget와 scale 계산 구현**

base viewport에서 maxAnalysisPixels와 maxDimension을 만족하는 scale을 계산하고 overflow를 검사한다.

- [ ] **5. sequential renderer와 cleanup 구현**

for loop로 한 page씩 render, getImageData, analyzePixels, optional orientation signal을 적용하고 finally에서 ImageData 참조, canvas, page와 renderer session을 정리한다. Promise.all page render를 사용하지 않는다.

- [ ] **6. orientation 보조 신호 구현**

orientationClient가 없거나 offline asset 실패면 pixel observation과 manual override를 유지한다. Tesseract orientation은 confidence threshold를 넘을 때만 quarterTurn 후보를 강화하며 180도 자동 확정을 보장하지 않는다.

- [ ] **7. 오류·경쟁 GREEN 테스트**

render error, getImageData error, analysis throw, orientation failure, rapid abort, pagehide-equivalent abort와 limit overflow가 blank observation으로 변환되지 않고 모든 resource를 정리하는지 검증한다.

- [ ] **8. focused 검증과 보고**

~~~sh
node --test web/page-analysis.test.mjs web/page-pixels.test.mjs web/pdf-renderer.test.mjs
node --check web/page-analysis.mjs
~~~

.superpowers/sdd/scan-cleanup-task-4-report.md에 max live counter, object URL 생성/revoke 수와 cleanup 결과를 기록한다.

### 작업 5: 검토 UI와 두 export 경로

**파일:**

- 생성: web/scancleanup/index.html
- 생성: web/scancleanup/scancleanup.mjs
- 생성: web/scancleanup/review-state.mjs
- 생성: web/scancleanup/review-state.test.mjs
- 생성: web/scancleanup/exporter.mjs
- 생성: web/scancleanup/exporter.test.mjs
- 생성: web/scancleanup/scancleanup.css
- 재사용: web/wasm-chunks.mjs
- 재사용: web/wasm-chunks.test.mjs
- 수정: web/output-sinks.mjs
- 수정: web/output-sinks.test.mjs
- 수정: web/pdfdiff/visual-state.mjs
- 수정: web/pdfdiff/visual-state.test.mjs
- 수정: web/pdfdiff/visual.mjs
- 생성: tests/e2e/scancleanup.spec.mjs

**인터페이스:**

~~~js
export function createReviewState(observations) {}
export function updatePageDecision(state, page, patch) {}
export function vectorPlan(state, geometries) {}
export async function exportRasterCleanup(options) {}

export const WASM_BRIDGE_CHUNK_BYTES = 1 << 20;
export const WASM_SOURCE_UPLOAD_COMMANDS = Object.freeze({ start:"sourceStart", chunk:"sourceChunk", finish:"sourceFinish", abort:"abort" });
export const WASM_PAGE_UPLOAD_COMMANDS = Object.freeze({ start:"pageStart", chunk:"pageChunk", finish:"pageFinish", abort:"abort" });
export async function uploadBlobChunks({ blob, start, commands = WASM_SOURCE_UPLOAD_COMMANDS, run, signal }) {}
export async function* readOutputChunks({ outputRevision, run, signal }) {}
export function createByteLimitedSink(sink, maxBytes) {}
export function createDelayedCleanupRegistry(options = {}) {}
~~~

- [ ] **1. review state RED 테스트 작성**

low/inconclusive observation이 keep=true, crop=null, rotation=0, normalizeBackground=false인 default를 만들고 high blank만 proposedRemoval=true이지만 keep는 사용자 toggle 전 true임을 검증한다. normalize control은 high-confidence neutral background에만 enabled이고 photo/faint/colored/inconclusive page는 항상 false여야 한다. 모든 페이지 removal과 stale revision을 거부한다.

- [ ] **2. RED 확인**

실행: node --test web/scancleanup/*.test.mjs

예상: module이 없어 실패한다.

- [ ] **3. review state와 vector plan 구현**

PDF.js viewport convertToPdfPoint를 사용해 normalized visual bounds 네 모서리를 PDF 좌표로 변환하고 axis-aligned Rect를 만든다. filename/setting/page decision mutation마다 revision을 증가시킨다.

- [ ] **4. shared sink limit·성공 download cleanup RED 테스트**

`web/output-sinks.test.mjs`에 memory, OPFS-like, directory-like sink를 두고 `createByteLimitedSink`가 read-only `kind/maxBytes/bytesWritten`을 노출하며 cap+1 chunk를 underlying `write` 전에 거부하는지 검증한다. allowed write는 한 번만 전달하고 repeated `close/abort/cleanup`에서 underlying operation은 최대 한 번이어야 한다. directory/OPFS wrapper cap은 256MiB, memory는 `min(requested,64MiB)`다.

Task 7의 `createDelayedCleanupRegistry` test를 shared module contract로 옮겨 timer→pagehide, pagehide→timer, manual→timer, repeated run과 captured old timer 뒤 fresh entry를 검증한다. URL revoke와 OPFS remove는 각 1회, directory remove는 0회, pending은 0이어야 한다. cleanup rejection은 own completion에만 남고 fresh registry entry를 지우지 않는다.

- [ ] **5. shared sink wrapper·cleanup registry 구현**

`web/output-sinks.mjs`가 `createByteLimitedSink`와 `createDelayedCleanupRegistry`를 export한다. wrapper는 overflow-safe cumulative check 뒤에만 write하고 close/abort/cleanup Promise를 memoize한다. registry는 Object URL, timer handle, closed sink cleanup을 한 entry로 소유하고 모든 trigger가 같은 idempotent `run()`을 호출한다. `web/pdfdiff/visual.mjs`도 shared registry를 import해 Task 7과 Scan/Smart가 같은 exactly-once 구현을 사용하게 한다.

- [ ] **6. vector chunk exporter RED 테스트**

fake Blob과 Worker로 `sink open -> sourceStart -> ordered sourceChunk -> sourceFinish -> applyPlan -> outputRead* -> outputRelease` 순서를 검증한다. chunk는 모두 1MiB 이하이고 caller ArrayBuffer가 postMessage 뒤 detach되며, 전체 `File.arrayBuffer()`·full output `Uint8Array`는 만들어지지 않아야 한다. memory sink의 64MiB가 `sourceStart.limits.maxOutputBytes`에 먼저 전달되어 64MiB+1에서 추가 spool allocation 전에 실패하는지, directory/OPFS는 256MiB인지 검증한다. stale revision, abort와 Worker error/messageerror에서 unread spool과 partial sink가 해제되는지 검증한다.

- [ ] **7. vector exporter 구현**

securityaudit에서 확장한 `window.scanCleanupWasm = boot("./scancleanup.wasm", {requireWorker:true, transferOwnership:true})` augmented-Promise handle, `web/scancleanup/protocol.mjs`와 `web/wasm-chunks.mjs`를 사용한다. `requireWorker` page는 boot의 main-thread runtime을 instantiate하지 않는다. raw sink를 directory/OPFS 256MiB 또는 memory 64MiB의 `createByteLimitedSink`로 먼저 감싸고 `effectiveMaxOutputBytes = sink.maxBytes`를 계산한다. `uploadBlobChunks({blob,start:{limits:{...limits,maxOutputBytes:effectiveMaxOutputBytes}},run:protocol.run,signal})`은 File을 slice별 `arrayBuffer()`로 읽어 원래 chunk buffer를 transfer하고, apply 결과는 `readOutputChunks({...,run:protocol.run})`으로 sink에 쓴다. raw `handle.run`을 shared helper에 직접 전달하지 않는다. current input identity와 revision이 일치하고 sink close 뒤 다시 stale/abort를 확인한 경우만 완료한다. Worker가 없거나 load/error/messageerror이면 fallback하지 않는다.

각 export는 monotonically increasing owner token과 local `{handle,sink,cleanupPromise}`를 캡처한다. 새 export는 이전 cleanupPromise를 await하고 old finally는 owner가 current일 때만 `abort`/`dispose`/partial sink cleanup을 수행한다. 성공한 memory/OPFS close 결과는 anchor click 전에 shared delayed registry에 등록하고 pagehide가 `cleanupAll()`을 호출한다. directory close 결과는 등록하지 않는다. delayed old cleanup 뒤 바로 시작한 fresh Worker/sink가 종료·삭제되지 않는 behavior test를 추가한다.

- [ ] **8. raster exporter RED 테스트**

500-page fake doc에서 다음 phase별 live counter를 검증한다: draw 중 `source Canvas + destination Canvas`, draw 후 source 0×0, normalize 중 `destination Canvas + ImageData`, encode 전 ImageData release, encode 중 `destination Canvas + encoded Blob`, bridge 중 Blob + 한 1MiB transfer chunk. 어떤 sample도 16,000,000 RGBA pixels를 넘지 않고 `pageFinish` acknowledgement 후 page 자원을 해제해야 한다. JPEG 0.85와 lossless PNG request, background normalization eligible/ineligible/default-off, cumulative bytes/output cap+1, outputRead backpressure와 render cancellation을 포함한다. actual/fake Go·JS retained-byte counter는 source document + final sink bytes + current page/spool chunk 경계를 검증한다.

- [ ] **9. raster exporter 구현**

raster도 sink를 먼저 열고 `rasterStart.opts.maxOutputBytes = min(256MiB,sink.maxBytes)`로 시작한다. 한 page씩 full render하고 white destination canvas에 quarter-turn+deskew와 reviewed border/crop을 적용한다. draw 완료 직후 source Canvas를 0×0으로 만든 뒤에만 destination `getImageData`를 만들고, eligible이며 사용자가 opt-in한 page만 Task 3의 exact neutral-background transform을 적용한다. `putImageData` 뒤 ImageData reference를 버리고 나서 canvas.toBlob을 balanced=image/jpeg quality 0.85 또는 lossless=image/png로 호출한다. Blob은 `uploadBlobChunks({blob,start:pageMeta,commands:WASM_PAGE_UPLOAD_COMMANDS,run:protocol.run,signal})`로 보내고 acknowledgement 뒤 Blob과 Canvas를 해제한다. `rasterFinish` 뒤 `readOutputChunks`의 chunk를 sink가 완전히 쓴 다음에만 다음 chunk를 요청한다. abort/outputRelease와 current revision을 모든 await 뒤 확인한다.

- [ ] **10. accessible source page 구현**

분석 Run/Cancel, mode 선택, loss warning confirmation, page review cards, keep/remove, manual 0/90/180/270, skew override, crop/border toggle, eligible page의 default-off background normalization, retained count, progress live region과 alert를 제공한다. confidence는 text와 reason을 함께 표시하고 ineligible normalize control에는 이유를 연결한다.

- [ ] **11. Chromium E2E RED/GREEN**

보존형 text/stream identity, low-confidence keep, blank review, raster deskew/border와 background/dark-ink/control pixel, loss confirmation, cancel 후 재실행, settings race와 stale download 차단을 실제 WASM으로 검증한다. 70MiB 결과 fixture는 memory sink에서 64MiB+1 serializer write 전에 실패하고 full Go spool/Blob을 만들지 않아야 한다. vector source 2.5MiB와 두 raster page를 actual optimized TinyGo Worker에 넣어 browser counter와 `bridgeStats`의 copy byte/call/max transient, source `ReadStats`와 retained 0을 대조한다. 정상 OPFS download 직후 timer 전 pagehide에서 remove 1/abort 0, URL revoke 1이고 directory success file은 remove 0인지 확인한다.

- [ ] **12. focused 검증과 보고**

~~~sh
node --test web/scancleanup/*.test.mjs web/page-analysis.test.mjs web/page-pixels.test.mjs web/wasm-chunks.test.mjs web/output-sinks.test.mjs web/pdfdiff/visual-state.test.mjs
npx playwright test tests/e2e/scancleanup.spec.mjs --project=chromium
node --check web/scancleanup/scancleanup.mjs
git diff --check
~~~

.superpowers/sdd/scan-cleanup-task-5-report.md에 Node/E2E, transfer detachment, source/result/current-page retained-byte peak counters, sink cap과 성공 OPFS URL/pagehide exactly-once cleanup 결과를 기록한다.

### 작업 6: catalog·다국어·문서·전체 통합

**파일:**

- 수정: tools/operation-catalog.json
- 수정: web/operation-catalog.json
- 수정: web/index.html
- 수정: tools/meta-i18n.json
- 수정: web/i18n/{ja,zh,es,fr,de}.js
- 수정: tests/operation-catalog.test.mjs
- 수정: tests/release-integration.test.mjs
- 수정: tests/worker-contract.test.mjs
- 수정: README.md
- 수정: README.ko.md
- 수정: web/llms.txt
- 수정: web/manifest.webmanifest
- 생성물: web/{ko,ja,zh,es,fr,de}/scancleanup/index.html
- 생성물: web/sitemap.xml

**선행 상태:** securityaudit 완료 후 catalog 43, WASM 38, visible 42
**완료 수치:** catalog 44, WASM 39, visible 43

- [ ] **1. release RED 테스트 수정**

catalog/count를 44/39/43으로 바꾸고 landing/README/llms/manifest, metadata 7-language, absolute assets, 모든 새 module translation key와 generated page를 요구한다.

- [ ] **2. descriptor와 product copy 추가**

~~~json
{"id":"scancleanup","engine":"wasm","entry":"/scancleanup/scancleanup.wasm","runtime":{"worker":"required","stateful":true,"chunkedIO":true},"input":{"kind":"pdf","min":1,"max":1},"output":{"kind":"pdf"},"capabilities":{"preview":true,"pipeline":false,"batch":false,"terminal":true},"build":{"package":"./wasm/scancleanup","output":"web/scancleanup/scancleanup.wasm"}}
~~~

landing card, ItemList, metadata, README, llms와 manifest를 43 visible tools로 갱신한다. 보존형과 raster-only 손실을 모두 설명한다.

worker-contract는 legacy WASM page에는 기존 `boot/runWasm` 계약을 유지하고, `runtime.worker == "required"` page에는 `boot(...,{requireWorker:true,transferOwnership:true})`, main-thread instantiate 부재, 같은 Worker의 순차 command 재사용, no-fallback과 error/messageerror cleanup을 behavior test로 요구한다. source regex만으로 runtime 보장을 주장하지 않는다.

- [ ] **3. dictionary와 generator GREEN**

English source key 전부를 다섯 dictionary에 번역하고 다음을 실행한다.

~~~sh
cp tools/operation-catalog.json web/operation-catalog.json
node tools/gen-i18n.mjs
~~~

예상: 0 missing I18N keys encountered, generated page 수 270.

- [ ] **4. 전체 검증**

~~~sh
go test ./pdf ./wasm/redact ./wasm/scancleanup -count=1
go test -race ./pdf ./wasm/redact ./wasm/scancleanup -count=1
go vet ./pdf ./wasm/redact ./wasm/scancleanup
node --test tests/operation-catalog.test.mjs tests/release-integration.test.mjs web/page-*.test.mjs web/scancleanup/*.test.mjs
GOOS=js GOARCH=wasm go build ./wasm/redact ./wasm/scancleanup
JOBS=1 ./build.sh
node tools/check-wasm-size.mjs web
npx playwright test tests/e2e/scancleanup.spec.mjs tests/e2e/redact.spec.mjs --project=chromium
npx playwright test tests/e2e/scancleanup.spec.mjs --project=firefox --project=webkit --project=mobile-chromium --project=mobile-webkit
git diff --check
~~~

예상: 모두 exit 0, redaction regression 없음, WASM size gate 통과.

- [ ] **5. 작업 보고 기록**

.superpowers/sdd/scan-cleanup-task-6-report.md에 count, generated pages, binary size, tests와 정확도 한계를 기록한다. commit/push는 하지 않는다.

## 계획 자체 검토

- 보존형과 raster-only 둘 다 구현 작업과 E2E가 있으며 임의 deskew를 vector preservation으로 축소하지 않는다.
- `Rect`, PagePlanEdit, PagePlanLimits, RasterPage, chunk protocol, analyzePDFPages와 exportRasterCleanup interface가 작업 사이에서 일관된다.
- 불확실 keep, default-off background normalization, source/output/live pixel·byte budget, URL cleanup과 file-size 경로가 모두 검증된다.
- bounded source parser/page traversal, serializer와 output spool이 선행 metadata 폭증과 JS↔Go 전체 payload 복사를 막고 `ReadStats`를 포함해 `원본 + 결과 + 현재 page/1MiB chunk` 앱 소유 메모리 경계를 계측한다.
- Scan이 byte-limit sink wrapper와 successful OPFS URL/timer/pagehide cleanup registry를 먼저 만들고 Smart와 기존 visual export가 같은 exactly-once 구현을 재사용한다.
- securityaudit 다음에 실행할 count와 smart split이 재사용할 page-analysis interface를 명시했다.
- placeholder, 외부 Go dependency, commit/push 단계가 없다.
