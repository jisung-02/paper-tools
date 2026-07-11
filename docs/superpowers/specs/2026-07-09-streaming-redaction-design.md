# 페이지 단위 스트리밍 영구 삭제 설계

## 목표

영구 삭제 처리를 페이지 단위 파이프라인으로 바꾼다. 브라우저는 한 번에 한 페이지를 렌더링하고 가림 영역을 적용한 PNG 하나만 WASM으로 전송한다. Go는 해당 페이지의 PDF 객체를 즉시 `io.Writer`에 기록하고 입력 PNG나 이전 페이지 raster를 보관하지 않는다. UI-WASM 경계에서 최종 PDF `Uint8Array` 하나를 반환하는 것은 허용하지만, 모든 페이지 PNG나 raster를 배열로 누적하지 않는다.

출력 PDF는 원본 PDF 객체를 한 개도 가져오지 않고, 명시적인 raster-only whitelist만 새로 생성한다. 기존 `BuildRasterOnlyPDF`와 `pdfRun(pages, opts)`는 호환 API로 남기되 동일한 incremental encoder와 예산·검증 경로를 재사용한다.

## 범위

- `pdf/rasterpdf.go`에 writer 기반 exported encoder와 수명주기를 추가한다.
- `wasm/redact`에 `start`, `add`, `finish`, `abort` 명령을 추가하고 기존 one-shot 호출을 보존한다.
- `web/redact`를 Blob URL 입력과 페이지 단위 export로 전환하고 취소 UI를 추가한다.
- 페이지, 픽셀, 원본 입력, PNG, 출력 크기에 UI와 Go 양쪽 예산을 적용한다.
- 실제 원본 PDF canary 구조를 사용한 Go 테스트와 Chromium E2E를 추가한다.
- TinyGo WASM 빌드와 관련 Go, Node, Chromium 검증을 수행한다.

일반 PDF builder 변경, 다른 도구의 worker protocol 변경, 외부 Go 의존성 추가, OPFS 기반 PDF 출력은 범위에 포함하지 않는다.

## Go incremental encoder

### 공개 API

```go
func NewRasterPDFEncoder(w io.Writer, expectedPages int, opts RasterPDFOpts) (*RasterPDFEncoder, error)
func (e *RasterPDFEncoder) AddPage(page RasterPage) error
func (e *RasterPDFEncoder) Finish() error
func (e *RasterPDFEncoder) Abort()
func ValidateRasterOnlyPDF(data []byte, expectedPages int) error
```

`expectedPages`는 시작 시 1 이상이어야 하고 resolved `MaxPages` 이하여야 한다. `Finish`는 정확히 `expectedPages`개가 추가된 경우에만 성공한다. `Abort`는 open, poisoned, finished, aborted 어느 상태에서도 반복 호출할 수 있다. `Finish` 중복 호출, finish 이후 add, poison 이후 add 또는 finish는 명시적인 lifecycle 오류를 반환한다.

### 직렬화 방식

constructor는 limits와 writer를 검증한 뒤 PDF header를 기록하고 object 1을 Catalog, object 2를 Pages로 예약한다. 각 `AddPage`는 image XObject, content stream, Page object 번호를 순서대로 배정하고 그 세 객체를 즉시 writer에 쓴다. Page의 `/Parent`는 아직 기록되지 않은 object 2를 가리킬 수 있다. `Finish`는 Pages, Catalog, classic xref, trailer, `startxref`, 최종 `%%EOF`를 기록한다.

메모리에 남는 장기 상태는 다음뿐이다.

- object 번호별 byte offset
- Page reference 목록
- 추가된 페이지 수
- 누적 픽셀·PNG byte·출력 byte 계수
- lifecycle 상태와 resolved limits

현재 `AddPage`가 처리하는 PNG, decoded image, RGB row, compressed stream은 해당 호출 안에서만 존재한다. 성공 또는 실패 뒤 encoder 필드에 저장하지 않는다. writer 오류나 일부 객체 기록 이후 오류가 발생하면 encoder는 poisoned 상태가 되고 재사용할 수 없다.

### whitelist

encoder는 일반 `Dict`를 외부에서 받지 않고 다음 구조만 쓴다.

- trailer: `Root`, `Size`
- Catalog: `Type`, `Pages`
- Pages: `Type`, `Kids`, `Count`
- Page: `Type`, `Parent`, `MediaBox`, `Resources`, `Contents`
- Resources: `XObject`
- XObject map: `Im0`
- Image stream: `Type`, `Subtype`, `Width`, `Height`, `BitsPerComponent`, `ColorSpace`, `Filter`, `Length`
- content stream: `Length`

출력에는 Info, XMP Metadata, Annots, AcroForm/XFA, EmbeddedFiles, JavaScript/OpenAction, Outlines, StructTreeRoot, source text/content stream, incremental revision이 존재하지 않는다. 기존 내부 validator는 exported `ValidateRasterOnlyPDF`로 제공한다. `BuildRasterOnlyPDF`와 WASM buffer 경로는 `Finish` 뒤 이 함수로 최종 bytes를 다시 검증한다.

### 호환 wrapper

`BuildRasterOnlyPDF(pages, opts)`는 bounded `bytes.Buffer`와 `NewRasterPDFEncoder`를 만들고 각 페이지를 `AddPage`한 뒤 `Finish`한다. 따라서 기존 API와 신규 stateful 경로의 출력 규칙, 예산, lifecycle이 분리되지 않는다.

## 예산

기본 UI/Go 예산은 다음과 같다.

| 항목 | 기본 상한 | 적용 위치 |
|---|---:|---|
| 원본 PDF | 256 MiB | UI, PDF.js load 전 |
| 페이지 수 | 500 | UI, Go start |
| 페이지별 픽셀 | 16,777,216 | UI canvas 할당 전, Go PNG config |
| 전체 처리 픽셀 | 67,108,864 | UI, Go 누적 계수 |
| 페이지별 PNG | 64 MiB | UI transfer 전, Go add |
| 전체 PNG 처리량 | 256 MiB | UI, Go 누적 계수 |
| 최종 PDF | 256 MiB | Go writer, UI 결과 수신 후 |
| 페이지 표시 크기 | 14,400 pt/축 | Go add |

전체 PNG 예산은 PNG를 보관하기 위한 공간이 아니라 누적 처리량 상한이다. 페이지 `add`가 완료되면 그 PNG에 대한 JS와 Go 참조는 다음 페이지 처리 전에 모두 제거한다. Go의 hard limit은 기존 정책을 유지하고 페이지별 PNG의 hard limit은 128 MiB로 추가한다.

## WASM 수명주기

worker 안에는 한 개의 session만 둔다. session은 bounded output buffer와 `RasterPDFEncoder`를 소유한다.

```text
{ command: "start", pageCount, opts }
{ command: "add", page: { pngData, widthPt, heightPt } }
{ command: "finish" }
{ command: "abort" }
```

- `start`: 기존 session이 있으면 먼저 `Abort`하고 참조를 해제한 뒤 새 session을 만든다.
- `add`: 현재 PNG를 Go로 복사하고 `AddPage`를 호출한 뒤 즉시 로컬 참조를 버린다. 성공 응답은 page count만 포함하는 작은 acknowledgement다.
- `finish`: `Finish`, 최종 whitelist 검증, JS `Uint8Array` 복사를 수행한 뒤 session을 제거한다. 오류가 나도 session을 abort하고 제거한다.
- `abort`: session이 없어도 성공하며, 있으면 `Abort`하고 buffer와 encoder 참조를 제거한다.

기존 `pdfRun(pages, opts)`는 명령 객체가 아닌 배열을 첫 인자로 받았을 때 선택된다. wrapper는 start, 각 add, finish와 동일한 내부 함수를 호출한다. 별도 builder나 예산 경로를 만들지 않는다.

JS 변환부와 무관한 session 로직은 host Go 테스트가 가능한 파일로 분리한다. `syscall/js` 파일은 명령 파싱과 `jsu.Bytes`/결과 변환만 담당한다.

## 브라우저 페이지 단위 처리

### 입력과 편집

원본 파일은 크기 예산을 먼저 검사한 뒤 `URL.createObjectURL(file)`로 PDF.js에 전달한다. 현재의 `file.arrayBuffer()` 전체 복사본은 만들지 않는다. 새 입력, 오류 복구가 원본을 폐기하는 경우, 성공 완료, `pagehide`에서 loading task/document를 destroy하고 URL을 revoke한다.

편집 화면은 현재 페이지 preview canvas와 normalized selection만 보관한다. export 시작 전에 preview canvas와 overlay를 0×0으로 줄여 export raster와 동시에 남지 않게 한다.

### export 루프

각 페이지는 다음 순서로 처리한다.

1. PDF.js page와 기본 scale-1 viewport를 얻는다.
2. viewport와 export scale로 canvas 크기를 계산하고 페이지·전체 픽셀 예산을 검사한다.
3. export canvas 하나를 만들고 흰 배경에 PDF.js render를 수행한다.
4. normalized selection을 바깥쪽 반올림하고 2px 안전 여백을 더해 불투명 검정으로 채운다.
5. canvas를 lossless PNG Blob으로 만들고 페이지·전체 PNG 예산을 검사한다.
6. PNG bytes 하나를 `add` 명령으로 transfer하고 acknowledgement를 기다린다.
7. `page.cleanup`, render task 해제, canvas 0×0, Blob/ArrayBuffer/Uint8Array 참조 제거를 완료한 뒤에만 다음 페이지를 요청한다.

PDF.js 기본 viewport는 effective CropBox, inherited Rotate, UserUnit을 반영한다. 출력 Page는 viewport의 scale-1 width/height를 0-origin MediaBox로 사용하고 Rotate/UserUnit을 쓰지 않는다. 따라서 회전과 비영점 box는 raster에 bake된다.

### 취소와 오류

export마다 `AbortController`와 현재 render task를 추적한다. Cancel은 controller abort와 `renderTask.cancel()`을 모두 실행한다. WASM 명령이 idle이면 `abort` 명령을 보내고, add/finish가 실행 중이면 worker client를 cancel/dispose하여 worker와 Go heap 전체를 종료한다. 다음 실행은 새 worker를 생성해 새 session으로 시작한다.

오류도 동일한 정리 경로를 사용한다. `pagehide`는 render task를 cancel하고 worker를 terminate하며 PDF.js document/loading task와 Blob URL을 정리한다. 정리 함수는 반복 호출 가능해야 한다.

## 테스트 설계

### Go RED/GREEN

- 각 `AddPage` 뒤 writer byte 수가 증가하고 encoder 상태에 PNG/page slice가 남지 않는다.
- manual lifecycle 출력과 `BuildRasterOnlyPDF` 출력이 동일하다.
- expected page 부족/초과, 중복 finish, finish 이후 add, poisoned 이후 add/finish가 명시적 오류다.
- writer failure가 encoder를 poison하고 `Abort`는 모든 상태에서 반복 가능하다.
- 페이지·픽셀·페이지 PNG·전체 PNG 처리량·출력 예산 경계를 검증한다.
- alpha가 흰색으로 flatten되고 redaction 픽셀이 decoded RGB에서 정확히 검정이다.
- source fixture가 text, link annotation, JavaScript, embedded file, AcroForm/widget, XMP, 구조별 canary를 실제로 포함하는지 파싱해 확인한다.
- 출력 raw bytes와 decoded streams에 canary가 없고 exact whitelist graph만 존재하며 `ExtractText`가 빈 문자열이다.

### WASM/session RED/GREEN

- start/add/finish/abort 정상 순서와 acknowledgement를 검증한다.
- stale start가 이전 session을 abort하고 교체한다.
- add 오류와 finish 오류가 state/buffer를 제거한다.
- abort는 session 유무와 관계없이 반복 가능하다.
- legacy one-shot wrapper가 동일 encoder 경로와 예산을 사용한다.
- TinyGo로 `wasm/redact`를 빌드한다.

### Node RED/GREEN

testable exporter에 fake PDF.js page, canvas, WASM client를 주입한다.

- command 순서는 start, 페이지별 add, finish다.
- 500-page fixture에서도 live page raster/PNG가 최대 1이고 add acknowledgement 뒤 참조가 해제된다.
- 예산 초과는 canvas 할당 또는 transfer 전에 실패한다.
- render 중 cancel은 render task cancel과 worker terminate를 모두 호출한다.
- 취소 뒤 새 실행은 새 worker/session을 사용한다.
- 오류와 pagehide 정리가 idempotent다.

### Chromium E2E

4페이지 source fixture는 rotation 0/90/180/270, nonzero MediaBox/CropBox, UserUnit을 포함한다. Catalog/page graph에는 link annotation, JavaScript action, embedded attachment, AcroForm/widget, XMP metadata, searchable text와 서로 다른 canary를 넣는다.

E2E는 모든 페이지에 normalized redaction을 만들고 실제 TinyGo WASM으로 export한 뒤 다음을 확인한다.

- source에서 PDF.js annotation, metadata, text API가 canary 구조를 실제로 관찰한다.
- output에서 PDF.js annotation이 비어 있고 metadata/text API 결과에 canary와 searchable text가 없다.
- output raw bytes에도 source canary와 금지 구조 이름이 없다.
- output 페이지 scale-1 viewport 크기가 source의 표시 크기와 일치한다.
- 각 페이지를 렌더링했을 때 선택 영역 내부 픽셀이 불투명 검정이다.
- output page count가 4이고 다운로드 PDF가 whitelist Go 검증과 동일한 구조를 갖는다.

## 검증 명령

```sh
go test ./pdf ./wasm/redact
node --test web/redact/*.test.mjs
tinygo build -o web/redact/redact.wasm -target wasm ./wasm/redact
./node_modules/.bin/playwright test tests/e2e/redact.spec.mjs --project=chromium --workers=1 --reporter=line
```

관련 shared contract를 건드린 경우에만 그 contract의 focused test를 추가 실행한다. 전체 build나 무관한 suite는 마지막 통합 검증 전에는 실행하지 않는다.

## 완료 조건

- 신규 UI는 모든 페이지 PNG 배열을 만들지 않는다.
- Go encoder는 페이지별 객체를 즉시 writer에 기록하고 O(page) metadata만 유지한다.
- 최종 PDF 외에는 페이지 raster/PNG가 다음 add 완료 이후 남지 않는다.
- 모든 취소·오류·pagehide 경로가 render, worker session, PDF.js, canvas, URL을 정리한다.
- 기존 Build API와 one-shot WASM API가 동일 encoder를 사용한다.
- 실제 canary source의 민감 구조와 text가 출력에서 제거되고 검정 가림 픽셀과 geometry가 Chromium에서 확인된다.
