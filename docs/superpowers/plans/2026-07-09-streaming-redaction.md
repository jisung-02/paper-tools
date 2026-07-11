# 페이지 단위 스트리밍 영구 삭제 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 영구 삭제를 한 페이지씩 PDF.js → PNG → WASM → writer 기반 PDF encoder로 전달하고, 원본의 민감 구조와 검색 텍스트가 없는 raster-only PDF를 만든다.

**Architecture:** Go encoder는 Catalog/Pages object 번호를 예약하고 각 페이지 객체를 `io.Writer`에 즉시 기록하며 offset과 page ref만 유지한다. WASM worker는 start/add/finish/abort session을 유지하고, 브라우저 exporter는 한 페이지의 canvas와 PNG만 live 상태로 둔다. 기존 Go Build API와 WASM one-shot API는 같은 lifecycle을 호출하는 wrapper다.

**Tech Stack:** Go 표준 라이브러리, TinyGo `syscall/js`, PDF.js, 브라우저 Canvas/Blob/Worker API, Node test runner, Playwright Chromium

## Global Constraints

- `/Users/chaejiseong/Desktop/yaho/.worktrees/review-remediation`에서만 작업한다.
- git commit과 git push를 실행하지 않는다.
- 외부 Go 의존성을 추가하지 않는다.
- 기존 변경을 보존하고 redaction 관련 파일만 수정한다.
- 기본 예산은 원본 256 MiB, 500 pages, page 16M pixels, total 64M pixels, page PNG 64 MiB, total PNG throughput 256 MiB, output 256 MiB다.
- 페이지 PNG와 raster는 `add` acknowledgement 뒤 다음 페이지로 넘어가기 전에 참조를 해제한다.
- TDD 순서는 실패 테스트 작성 → 예상 실패 확인 → 최소 구현 → focused pass 확인이다.

---

### Task 1: Writer 기반 Go encoder와 lifecycle

**Files:**
- Modify: `pdf/rasterpdf.go`
- Modify: `pdf/rasterpdf_test.go`

**Interfaces:**
- Consumes: 기존 `RasterPage`, `RasterPDFOpts`, `validateRasterOnlyPDF`, PNG decode/alpha flatten 코드
- Produces: `NewRasterPDFEncoder(io.Writer, int, RasterPDFOpts)`, `(*RasterPDFEncoder).AddPage`, `Finish`, `Abort`, `ValidateRasterOnlyPDF`

- [ ] **Step 1: lifecycle과 즉시 write 실패 테스트 작성**

`pdf/rasterpdf_test.go`에 다음 테스트를 추가한다.

```go
type failingRasterWriter struct {
	bytes.Buffer
	failAfter int
}

func (w *failingRasterWriter) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.failAfter {
		return 0, errors.New("writer stopped")
	}
	return w.Buffer.Write(p)
}

func TestRasterPDFEncoderStreamsPagesAndEnforcesLifecycle(t *testing.T) {
	pages := []RasterPage{
		{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 200},
		{PNGData: testPNG(t, 3, 2), WidthPt: 200, HeightPt: 100},
	}
	var out bytes.Buffer
	encoder, err := NewRasterPDFEncoder(&out, len(pages), RasterPDFOpts{})
	if err != nil { t.Fatal(err) }
	headerBytes := out.Len()
	if headerBytes == 0 { t.Fatal("constructor did not stream the PDF header") }
	for i, page := range pages {
		before := out.Len()
		if err := encoder.AddPage(page); err != nil { t.Fatalf("AddPage %d: %v", i+1, err) }
		if out.Len() <= before { t.Fatalf("AddPage %d wrote no page objects", i+1) }
	}
	if err := encoder.Finish(); err != nil { t.Fatal(err) }
	if err := ValidateRasterOnlyPDF(out.Bytes(), 2); err != nil { t.Fatal(err) }
	if err := encoder.Finish(); !errors.Is(err, ErrRasterPDFLifecycle) { t.Fatalf("duplicate Finish = %v", err) }
	if err := encoder.AddPage(pages[0]); !errors.Is(err, ErrRasterPDFLifecycle) { t.Fatalf("Add after Finish = %v", err) }
	encoder.Abort()
	encoder.Abort()
}

func TestRasterPDFEncoderPageCountAndPoisoning(t *testing.T) {
	page := RasterPage{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 100}
	var short bytes.Buffer
	encoder, err := NewRasterPDFEncoder(&short, 2, RasterPDFOpts{})
	if err != nil { t.Fatal(err) }
	if err := encoder.AddPage(page); err != nil { t.Fatal(err) }
	if err := encoder.Finish(); !errors.Is(err, ErrRasterPDFLifecycle) { t.Fatalf("short Finish = %v", err) }

	writer := &failingRasterWriter{failAfter: 32}
	poisoned, err := NewRasterPDFEncoder(writer, 1, RasterPDFOpts{})
	if err != nil { t.Fatal(err) }
	if err := poisoned.AddPage(page); err == nil { t.Fatal("writer failure was accepted") }
	if err := poisoned.AddPage(page); !errors.Is(err, ErrRasterPDFLifecycle) { t.Fatalf("Add after poison = %v", err) }
	if err := poisoned.Finish(); !errors.Is(err, ErrRasterPDFLifecycle) { t.Fatalf("Finish after poison = %v", err) }
	poisoned.Abort()
	poisoned.Abort()
}
```

- [ ] **Step 2: RED 확인**

Run: `go test ./pdf -run 'TestRasterPDFEncoder(StreamsPagesAndEnforcesLifecycle|PageCountAndPoisoning)' -count=1`

Expected: FAIL because `NewRasterPDFEncoder`, `ErrRasterPDFLifecycle`, and `ValidateRasterOnlyPDF` do not exist.

- [ ] **Step 3: 최소 writer encoder 구현**

`pdf/rasterpdf.go`에 다음 상태와 API를 추가하고 기존 image encode 코드를 builder 독립 함수로 옮긴다.

```go
var ErrRasterPDFLifecycle = errors.New("invalid raster PDF encoder lifecycle")

type rasterPDFState uint8

const (
	rasterPDFOpen rasterPDFState = iota
	rasterPDFPoisoned
	rasterPDFFinished
	rasterPDFAborted
)

type RasterPDFEncoder struct {
	w             io.Writer
	limits        resolvedRasterLimits
	expectedPages int
	pageCount     int
	offsets       []uint64
	kids          []Ref
	written       uint64
	totalPixels   uint64
	totalPNGBytes uint64
	estimatedOut  uint64
	state         rasterPDFState
}

func NewRasterPDFEncoder(w io.Writer, expectedPages int, opts RasterPDFOpts) (*RasterPDFEncoder, error)
func (e *RasterPDFEncoder) AddPage(page RasterPage) error
func (e *RasterPDFEncoder) Finish() error
func (e *RasterPDFEncoder) Abort()
func ValidateRasterOnlyPDF(data []byte, expectedPages int) error
```

구현 규칙은 다음과 같이 고정한다.

```go
const rasterPDFHeader = "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n"

func (e *RasterPDFEncoder) writeRaw(data []byte) error {
	if uint64(len(data)) > e.limits.outBytes-e.written {
		e.state = rasterPDFPoisoned
		return fmt.Errorf("%w: output bytes exceed %d", ErrRasterPDFBudget, e.limits.outBytes)
	}
	n, err := e.w.Write(data)
	e.written += uint64(n)
	if err != nil || n != len(data) {
		e.state = rasterPDFPoisoned
		if err == nil { err = io.ErrShortWrite }
		return err
	}
	return nil
}

func rasterObjectBytes(number int, value any) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%d 0 obj\n", number)
	writeObj(&out, value)
	out.WriteString("\nendobj\n")
	return out.Bytes()
}
```

object 번호는 Catalog 1, Pages 2, page index `i`의 Image `3+i*3`, Contents `4+i*3`, Page `5+i*3`로 고정한다. `AddPage`는 config/budget을 먼저 검사하고 현재 PNG를 opaque RGB Flate stream으로 만든 뒤 세 object를 기록한다. `Finish`는 object 2, object 1, xref, trailer를 기록한다. writer error는 poison 상태로 바꾼다. `Abort`는 state를 aborted로 바꾸고 `offsets`와 `kids`를 nil로 만든다.

- [ ] **Step 4: GREEN 확인**

Run: `go test ./pdf -run 'TestRasterPDFEncoder(StreamsPagesAndEnforcesLifecycle|PageCountAndPoisoning)' -count=1`

Expected: PASS.

- [ ] **Step 5: `BuildRasterOnlyPDF`를 lifecycle wrapper로 전환하는 실패 테스트 작성**

```go
func TestBuildRasterOnlyPDFMatchesManualEncoder(t *testing.T) {
	pages := []RasterPage{
		{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 200},
		{PNGData: testPNG(t, 3, 2), WidthPt: 200, HeightPt: 100},
	}
	var manual bytes.Buffer
	encoder, err := NewRasterPDFEncoder(&manual, len(pages), RasterPDFOpts{})
	if err != nil { t.Fatal(err) }
	for _, page := range pages {
		if err := encoder.AddPage(page); err != nil { t.Fatal(err) }
	}
	if err := encoder.Finish(); err != nil { t.Fatal(err) }
	built, err := BuildRasterOnlyPDF(pages, RasterPDFOpts{})
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(built, manual.Bytes()) { t.Fatal("Build used a different serialization path") }
}
```

- [ ] **Step 6: RED 후 wrapper 최소 구현**

Run: `go test ./pdf -run TestBuildRasterOnlyPDFMatchesManualEncoder -count=1`

Expected: FAIL because the old builder output differs.

`BuildRasterOnlyPDF`를 `bytes.Buffer` + constructor + add loop + finish + `ValidateRasterOnlyPDF` 호출로 바꾼다. add/finish 오류 시 `Abort`를 호출한다.

- [ ] **Step 7: Task 1 focused GREEN**

Run: `go test ./pdf -run 'Test(RasterPDFEncoder|BuildRasterOnlyPDF)' -count=1`

Expected: 기존 raster tests와 신규 tests 모두 PASS.

---

### Task 2: 페이지 PNG 예산과 실제 source canary 검증

**Files:**
- Modify: `pdf/rasterpdf.go`
- Modify: `pdf/rasterpdf_test.go`

**Interfaces:**
- Consumes: Task 1 encoder API
- Produces: `RasterPDFOpts.MaxPagePNGBytes`, exact source/output security regression

- [ ] **Step 1: 페이지 PNG/누적 처리량 RED 작성**

```go
func TestRasterPDFEncoderPNGByteBudgets(t *testing.T) {
	page := RasterPage{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 100}
	for _, tc := range []struct {
		name string
		opts RasterPDFOpts
	}{
		{name: "per page", opts: RasterPDFOpts{MaxPagePNGBytes: uint64(len(page.PNGData) - 1)}},
		{name: "total", opts: RasterPDFOpts{MaxPNGBytes: uint64(len(page.PNGData)*2 - 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			count := 1
			if tc.name == "total" { count = 2 }
			encoder, err := NewRasterPDFEncoder(&out, count, tc.opts)
			if err != nil { t.Fatal(err) }
			if tc.name == "total" {
				if err := encoder.AddPage(page); err != nil { t.Fatal(err) }
			}
			if err := encoder.AddPage(page); !errors.Is(err, ErrRasterPDFBudget) { t.Fatalf("error = %v", err) }
		})
	}
}
```

- [ ] **Step 2: RED 확인**

Run: `go test ./pdf -run TestRasterPDFEncoderPNGByteBudgets -count=1`

Expected: compile FAIL because `MaxPagePNGBytes` does not exist.

- [ ] **Step 3: 예산 필드 구현**

`RasterPDFOpts`와 resolved limits에 `MaxPagePNGBytes uint64`를 추가한다. default는 64 MiB, hard limit은 128 MiB다. `AddPage`가 PNG decode 전에 page bytes와 누적 bytes를 검사한다.

- [ ] **Step 4: 예산 GREEN 확인**

Run: `go test ./pdf -run 'TestRasterPDFEncoderPNGByteBudgets|TestBuildRasterOnlyPDFValidationAndBudgets' -count=1`

Expected: PASS.

- [ ] **Step 5: 실제 source PDF canary RED 작성**

`pdf/rasterpdf_test.go`에 `rasterSourceCanaryPDF(t)` helper를 추가한다. classic PDF object graph는 Catalog의 `/OpenAction`, `/Names << /JavaScript ... /EmbeddedFiles ... >>`, `/AcroForm`, `/Metadata`, Page의 `/Annots`, Widget/Link annotation, embedded file stream, XMP stream, Helvetica text content를 포함한다. canary 문자열은 다음 상수로 고정한다.

```go
var rasterSourceCanaries = []string{
	"REDACT-SOURCE-TEXT",
	"REDACT-LINK-CANARY",
	"REDACT-JS-CANARY",
	"REDACT-ATTACHMENT-CANARY",
	"REDACT-FORM-CANARY",
	"REDACT-XMP-CANARY",
}
```

테스트는 source를 `Parse`하고 Catalog/Page/API 구조에 canary가 실제 존재함을 먼저 확인한다. 그 뒤 black center를 가진 PNG 한 장을 encoder에 넣고 출력에서 모든 canary와 `/Annots`, `/OpenAction`, `/JavaScript`, `/EmbeddedFiles`, `/AcroForm`, `/Metadata`가 없고 `ExtractText`가 빈 문자열이며 `assertRasterOnlyGraph`를 통과하는지 확인한다.

- [ ] **Step 6: RED/GREEN 확인**

Run: `go test ./pdf -run TestRasterPDFEncoderDropsActualSourceCanaryGraph -count=1`

Expected RED: helper 또는 assertion 미구현으로 FAIL.

source fixture와 assertions만 테스트 코드로 완성한다. production encoder는 source PDF를 받지 않으므로 Task 1 whitelist 구현이 올바르면 GREEN이어야 한다.

- [ ] **Step 7: Task 2 focused GREEN**

Run: `go test ./pdf -run 'RasterPDF|RasterOnly' -count=1`

Expected: PASS.

---

### Task 3: Host-testable WASM session과 JS command adapter

**Files:**
- Create: `wasm/redact/session.go`
- Create: `wasm/redact/session_test.go`
- Modify: `wasm/redact/main.go`

**Interfaces:**
- Consumes: Task 1 `RasterPDFEncoder`, Task 2 `MaxPagePNGBytes`, `ValidateRasterOnlyPDF`
- Produces: `redactSessionManager.Start`, `Add`, `Finish`, `Abort`, `Build`; JS start/add/finish/abort protocol and legacy wrapper

- [ ] **Step 1: session lifecycle RED 작성**

```go
func TestRedactSessionLifecycleAndLegacyWrapper(t *testing.T) {
	page := pdf.RasterPage{PNGData: sessionPNG(t), WidthPt: 100, HeightPt: 200}
	manager := &redactSessionManager{}
	if err := manager.Start(1, pdf.RasterPDFOpts{}); err != nil { t.Fatal(err) }
	if count, err := manager.Add(page); err != nil || count != 1 { t.Fatalf("Add = %d, %v", count, err) }
	out, err := manager.Finish()
	if err != nil { t.Fatal(err) }
	if manager.active != nil { t.Fatal("Finish retained the session") }
	if err := pdf.ValidateRasterOnlyPDF(out, 1); err != nil { t.Fatal(err) }
	manager.Abort()
	manager.Abort()
	legacy, err := manager.Build([]pdf.RasterPage{page}, pdf.RasterPDFOpts{})
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(out, legacy) { t.Fatal("legacy wrapper used a different encoder path") }
}

func TestRedactSessionClearsErrorsAndReplacesStaleStart(t *testing.T) {
	manager := &redactSessionManager{}
	if err := manager.Start(2, pdf.RasterPDFOpts{}); err != nil { t.Fatal(err) }
	first := manager.active
	if err := manager.Start(1, pdf.RasterPDFOpts{}); err != nil { t.Fatal(err) }
	if manager.active == first { t.Fatal("Start retained stale state") }
	if _, err := manager.Add(pdf.RasterPage{}); err == nil { t.Fatal("invalid page accepted") }
	if manager.active != nil { t.Fatal("Add error retained state") }
	if _, err := manager.Finish(); err == nil { t.Fatal("Finish without state succeeded") }
}
```

- [ ] **Step 2: RED 확인**

Run: `go test ./wasm/redact -count=1`

Expected: FAIL because host-testable session types do not exist.

- [ ] **Step 3: session 최소 구현**

`session.go`는 build tag 없이 package main으로 작성한다.

```go
type redactSession struct {
	buffer  *bytes.Buffer
	encoder *pdf.RasterPDFEncoder
	pages   int
}

type redactSessionManager struct {
	active *redactSession
}

func (m *redactSessionManager) Start(pageCount int, opts pdf.RasterPDFOpts) error
func (m *redactSessionManager) Add(page pdf.RasterPage) (int, error)
func (m *redactSessionManager) Finish() ([]byte, error)
func (m *redactSessionManager) Abort()
func (m *redactSessionManager) Build(pages []pdf.RasterPage, opts pdf.RasterPDFOpts) ([]byte, error)
```

`Start`는 `Abort` 후 새 buffer/encoder를 만든다. `Add` 오류와 `Finish` 성공/오류는 모두 active를 nil로 하고 encoder를 abort한다. `Finish`는 encoder finish 뒤 `pdf.ValidateRasterOnlyPDF(buffer.Bytes(), pages)`를 호출하고 성공 bytes를 독립 slice로 복사한다. `Build`는 Start/Add/Finish를 순서대로 호출한다.

- [ ] **Step 4: session GREEN 확인**

Run: `go test ./wasm/redact -count=1`

Expected: PASS.

- [ ] **Step 5: JS adapter 명령 구현**

`main.go`는 package global manager를 만들고 첫 인자가 `{command: string}`이면 다음 dispatch를 사용한다.

```go
var sessions redactSessionManager

func command(args []js.Value) any {
	request := args[0]
	switch request.Get("command").String() {
	case "start":
		err := sessions.Start(int(number(request, "pageCount")), options(request.Get("opts")))
		return jsu.JSONOut(map[string]any{"state": "started"}, err)
	case "add":
		count, err := sessions.Add(page(request.Get("page")))
		return jsu.JSONOut(map[string]any{"pages": count}, err)
	case "finish":
		return jsu.Out(sessions.Finish())
	case "abort":
		sessions.Abort()
		return jsu.JSONOut(map[string]any{"state": "aborted"}, nil)
	default:
		return jsu.JSONOut(nil, fmt.Errorf("unknown redact command"))
	}
}
```

기존 첫 인자가 Array이면 `sessions.Build`을 호출한다. options parser는 `MaxPagePNGBytes`를 포함한다. args 길이와 JS type을 검사해 panic 대신 error object를 반환한다.

- [ ] **Step 6: TinyGo compile GREEN**

Run: `tinygo build -o /tmp/redact-task9.wasm -target wasm ./wasm/redact`

Expected: exit 0 and `/tmp/redact-task9.wasm` exists.

---

### Task 4: Testable 페이지 exporter와 UI cleanup

**Files:**
- Create: `web/redact/exporter.mjs`
- Create: `web/redact/exporter.test.mjs`
- Modify: `web/redact/redact.mjs`
- Modify: `web/redact/index.html`
- Modify: `web/redact/redact.css`
- Test: `web/redact/geometry.test.mjs`

**Interfaces:**
- Consumes: WASM command protocol, `selectionPixels`, PDF.js page/render APIs
- Produces: `REDACT_LIMITS`, `validateRedactSource`, `streamRedactedPDF`

- [ ] **Step 1: one-live-page와 command 순서 RED 작성**

`exporter.test.mjs`는 500개의 fake page를 만들고 각 page의 render task, canvas allocation/dispose, PNG Blob 생성, command call을 계수한다.

```js
test("stream exporter keeps one page raster live and releases it after add", async () => {
  const calls = [];
  let live = 0;
  let peak = 0;
  const doc = fakeDocument(500, {
    allocate() { live++; peak = Math.max(peak, live); },
    dispose() { live--; },
  });
  const result = await streamRedactedPDF({
    doc,
    selections: new Map(),
    invoke: async (request) => {
      calls.push(request.command);
      if (request.command === "finish") return { data: new Uint8Array([1]) };
      return { json: "{}" };
    },
    terminateWorker() {},
    createCanvas: fakeCanvasFactory,
    encodePNG: async () => new Blob([new Uint8Array([1])], { type: "image/png" }),
    limits: { ...REDACT_LIMITS, maxPages: 500, maxTotalPixels: 1000 },
  });
  assert.deepEqual(calls, ["start", ...Array(500).fill("add"), "finish"]);
  assert.equal(result.data[0], 1);
  assert.equal(peak, 1);
  assert.equal(live, 0);
});
```

- [ ] **Step 2: budget/cancel RED 작성**

```js
test("source and canvas budgets fail before allocation or transfer", async () => {
  assert.throws(() => validateRedactSource({ size: REDACT_LIMITS.maxInputBytes + 1 }), /input/i);
  let allocations = 0;
  await assert.rejects(streamRedactedPDF({
    doc: fakeDocument(1, { width: 5000, height: 5000 }),
    selections: new Map(),
    invoke: async () => ({ json: "{}" }),
    terminateWorker() {},
    createCanvas() { allocations++; return fakeCanvasFactory(1, 1); },
    encodePNG: async () => new Blob([new Uint8Array([1])]),
  }), /pixel/i);
  assert.equal(allocations, 0);
});

test("render cancellation cancels the task and terminates the worker", async () => {
  const controller = new AbortController();
  let renderCancelled = 0;
  let workerTerminated = 0;
  const pending = streamRedactedPDF({
    doc: blockingDocument(() => { renderCancelled++; }),
    selections: new Map(),
    invoke: async () => ({ json: "{}" }),
    terminateWorker() { workerTerminated++; },
    createCanvas: fakeCanvasFactory,
    encodePNG: async () => new Blob(),
    signal: controller.signal,
  });
  controller.abort();
  await assert.rejects(pending, /Abort/);
  assert.equal(renderCancelled, 1);
  assert.equal(workerTerminated, 1);
});
```

- [ ] **Step 3: RED 확인**

Run: `node --test web/redact/exporter.test.mjs`

Expected: FAIL because `exporter.mjs` and exported functions do not exist.

- [ ] **Step 4: exporter 최소 구현**

`exporter.mjs`는 다음 limits를 export한다.

```js
export const REDACT_LIMITS = Object.freeze({
  maxInputBytes: 256 * 1024 * 1024,
  maxPages: 500,
  maxPagePixels: 16 * 1024 * 1024,
  maxTotalPixels: 64 * 1024 * 1024,
  maxPagePNGBytes: 64 * 1024 * 1024,
  maxTotalPNGBytes: 256 * 1024 * 1024,
  maxOutputBytes: 256 * 1024 * 1024,
  exportScale: 2,
});
```

`streamRedactedPDF`은 start, page loop, finish를 수행한다. 각 page helper의 `finally`에서 render abort listener 제거, page cleanup, canvas dispose를 실행한다. signal abort listener는 현재 render task cancel과 `terminateWorker`를 모두 호출한다. non-abort error는 best-effort `{command:"abort"}`를 호출한다. add가 성공한 뒤 helper scope가 끝난 다음에만 다음 `doc.getPage`를 호출한다.

- [ ] **Step 5: exporter GREEN 확인**

Run: `node --test web/redact/exporter.test.mjs web/redact/geometry.test.mjs`

Expected: PASS.

- [ ] **Step 6: UI를 exporter에 연결**

`redact.mjs`는 원본 file 크기를 `validateRedactSource`로 검사한 뒤 Blob URL을 PDF.js `getDocument({url})`에 전달한다. export 시작 전에 preview/overlay canvas를 0×0으로 만든다. browser canvas adapter는 흰 배경, PDF.js render, `selectionPixels(..., 2)` black fill, PNG `toBlob`, `canvas.width = canvas.height = 0` dispose를 제공한다.

Cancel button handler는 active controller abort, current render cancel, `window.__wasmClient.cancel()`을 호출한다. 다음 run은 같은 client object가 새 worker를 생성한다. 성공 시 output 크기를 검사하고 `window.finish`, verified 표시, source document destroy, Blob URL revoke를 수행한다. error와 pagehide cleanup은 idempotent 함수 하나를 사용한다.

`index.html`에 다음 버튼을 run button 옆에 추가한다.

```html
<button id="redactCancel" type="button" disabled data-i18n data-en="Cancel" data-ko="취소">Cancel</button>
```

- [ ] **Step 7: UI contract GREEN 확인**

Run: `node --test web/redact/*.test.mjs tests/worker-contract.test.mjs`

Expected: PASS.

---

### Task 5: 실제 canary Chromium E2E와 최종 빌드

**Files:**
- Create: `tests/e2e/redact.spec.mjs`
- Modify: `pdf/rasterpdf_test.go` only if E2E fixture exposes a missing whitelist assertion
- Generated: `web/redact/redact.wasm`

**Interfaces:**
- Consumes: actual PDF.js, actual TinyGo redact WASM, UI exporter
- Produces: end-to-end security, geometry, pixel, text evidence

- [ ] **Step 1: Chromium RED fixture 작성**

`redact.spec.mjs`의 `canaryPDF()`는 classic xref PDF를 생성한다. 네 Page는 nonzero MediaBox `[10 20 210 120]`, CropBox `[20 30 180 110]`, rotations 0/90/180/270을 사용하고 세 번째와 네 번째 페이지는 `/UserUnit 2`를 사용한다. Catalog/Page graph에 다음을 넣는다.

- `/OpenAction` JavaScript `REDACT-JS-CANARY`
- `/Names` JavaScript와 EmbeddedFiles name tree
- embedded file stream `REDACT-ATTACHMENT-CANARY`
- `/AcroForm`과 Widget `REDACT-FORM-CANARY`
- Link annotation URI `REDACT-LINK-CANARY`
- Metadata XML `REDACT-XMP-CANARY`
- 각 content stream의 Helvetica text `REDACT-SOURCE-TEXT-N`

E2E는 source bytes를 page context의 PDF.js로 열어 `getAnnotations`, `getMetadata`, `getTextContent`에서 canary가 관찰되는지 먼저 확인한다. UI에서 각 페이지 중앙 20%를 pointer drag로 선택하고 Run/Download를 수행한다.

- [ ] **Step 2: RED 확인**

Run: `./node_modules/.bin/playwright test tests/e2e/redact.spec.mjs --project=chromium --workers=1 --reporter=line --output=/tmp/redact-task9-red`

Expected: FAIL because current UI sends all PNG pages in one call or stateful commands/export cleanup are not implemented.

- [ ] **Step 3: output security/geometry/pixel assertions 완성**

download bytes를 다시 PDF.js로 열어 다음을 검사한다.

```js
expect(output.numPages).toBe(4);
for (let number = 1; number <= 4; number++) {
  const page = await output.getPage(number);
  expect(await page.getAnnotations()).toEqual([]);
  expect((await page.getTextContent()).items).toEqual([]);
  const viewport = page.getViewport({ scale: 1 });
  expect([viewport.width, viewport.height]).toEqual(sourceDisplaySizes[number - 1]);
  const black = await renderCenterPixel(page);
  expect(black.r).toBeLessThanOrEqual(2);
  expect(black.g).toBeLessThanOrEqual(2);
  expect(black.b).toBeLessThanOrEqual(2);
  expect(black.a).toBe(255);
}
```

`getMetadata` 결과와 raw output text에는 source canary가 없어야 한다. raw output에는 `/Annots`, `/OpenAction`, `/JavaScript`, `/EmbeddedFiles`, `/AcroForm`, `/Metadata`, `/XFA`, `/StructTreeRoot`가 없어야 한다.

- [ ] **Step 4: 실제 WASM 빌드**

Run: `tinygo build -o web/redact/redact.wasm -target wasm ./wasm/redact`

Expected: exit 0.

- [ ] **Step 5: focused GREEN 검증**

Run: `go test ./pdf ./wasm/redact -count=1`

Expected: PASS.

Run: `node --test web/redact/*.test.mjs tests/worker-contract.test.mjs`

Expected: PASS.

Run: `./node_modules/.bin/playwright test tests/e2e/redact.spec.mjs --project=chromium --workers=1 --reporter=line --output=/tmp/redact-task9-results`

Expected: PASS.

- [ ] **Step 6: 변경 범위와 생성물 확인**

Run: `git status --short -- docs/superpowers/specs/2026-07-09-streaming-redaction-design.md docs/superpowers/plans/2026-07-09-streaming-redaction.md pdf/rasterpdf.go pdf/rasterpdf_test.go wasm/redact web/redact tests/e2e/redact.spec.mjs`

Expected: redaction 관련 파일과 두 문서만 표시되고 commit은 생성되지 않는다.

## Self-review 결과

- Spec coverage: writer streaming, lifecycle, compatibility wrapper, budgets, worker commands, one-page UI, cleanup, canary whitelist, rotations/boxes/UserUnit, black pixels, empty text extraction, TinyGo/Go/Node/Chromium 검증이 Task 1–5에 모두 연결되어 있다.
- Placeholder scan: 미정 항목과 후속 구현 표시는 없다.
- Type consistency: Go API는 모든 task에서 `NewRasterPDFEncoder(w, expectedPages, opts)`와 `AddPage/Finish/Abort`를 사용한다. WASM command는 `start/add/finish/abort`, UI exporter는 `streamRedactedPDF`, limits는 `REDACT_LIMITS`로 통일했다.
- Commit constraint: 모든 commit step을 제외하고 focused verification checkpoint로 대체했다.
