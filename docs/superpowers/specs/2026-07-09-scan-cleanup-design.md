# 스캔 문서 정리 설계

## 목적

`/scancleanup/`에 PDF 스캔 문서를 페이지별로 분석하고 검토한 뒤 정리하는 독립 도구를 추가한다. 자동 분석은 제안만 만들며, 불확실한 페이지를 삭제하거나 자르지 않는다. 한 도구 안에 다음 두 출력 모드를 명확히 분리한다.

1. 보존형 정리: 90도 단위 회전, 검토된 빈 페이지 제거, `CropBox` 기반 시각적 테두리 정리
2. 래스터형 정리: 임의 기울기 보정, 실제 테두리 픽셀 제거, 배경 정규화, 새 이미지 전용 PDF 생성

두 모드 모두 브라우저 안에서만 처리한다. Go 코드는 표준 라이브러리만 사용한다.

선행 Security Audit가 `PDFReadLimits`, `PDFReadStats`, `ParseBounded/GetBounded/ResolveBounded/PagesBounded`를 공통 `pdf` API로 제공하며 이 도구는 loose public parser를 사용하지 않는다.

## 선택한 접근

공통 저해상도 분석기와 서로 다른 두 출력기를 사용한다.

- PDF.js는 페이지를 순차적으로 최대 2,000,000픽셀 분석 이미지로 렌더링한다.
- 순수 픽셀 함수가 빈 페이지 점수, 잉크 경계, 가장자리 띠, 90도 단위 방향 후보, `-5.0..+5.0`도 기울기 후보와 신뢰도를 계산한다.
- 사용자가 페이지별 제안을 확인·수정한다.
- 보존형은 Go의 원자적 page-plan 연산으로 원본 content stream과 resource를 재인코딩하지 않는다.
- 래스터형은 페이지 하나씩 변환해 상태형 `RasterPDFEncoder`에 전달한다.
- 대용량 입력과 출력은 1MiB 이하 청크로 Worker 경계를 통과한다. Go 출력은 고정 크기 청크 spool에 기록하고 읽어 간 청크를 즉시 해제한다.

단일 거대 Canvas에서 모든 페이지를 유지하는 방식과 이미지 XObject를 직접 찾아 교체하는 방식은 사용하지 않는다. 전자는 메모리 상한을 지킬 수 없고, 후자는 다중 이미지·mask·Form XObject·overlay 페이지를 정확히 처리하기 어렵다.

## 입력과 사용자 흐름

- 입력은 PDF 한 개이며 최대 256MiB, 최대 500페이지다.
- 암호화 PDF는 처리하지 않고 `/unlock/`에서 권한 있는 사용자가 먼저 해제하도록 안내한다.
- 이미지 파일은 `/img2pdf/`로 PDF를 만든 뒤 사용하도록 안내한다.
- 분석 중에는 현재 페이지, 전체 페이지, 단계와 취소 버튼을 표시한다.
- 검토 화면은 페이지별로 다음 상태를 표시한다.
  - 유지 또는 제거
  - 제안된 0/90/180/270도 방향과 신뢰도
  - 제안된 작은 기울기 각도와 신뢰도
  - 빈 페이지 점수와 판정 이유
  - 제안된 콘텐츠 경계와 가장자리 띠
  - `확실`, `불확실`, `분석 불가` 상태
- `불확실`과 `분석 불가`의 기본값은 항상 원본 유지다.
- 모든 페이지를 제거하는 계획은 실행할 수 없다.
- 실행 전 보존형과 래스터형의 구조 손실 차이를 다시 표시한다.

## 공통 분석 모델

`web/page-pixels.mjs`는 DOM과 PDF.js를 사용하지 않는 순수 함수만 제공한다.

```js
analyzePagePixels({ data, width, height }, options) => {
  blank: { score, confidence, reason },
  background: { luminance, confidence, neutral },
  contentBounds: { left, top, right, bottom } | null,
  edgeBands: { top, right, bottom, left },
  quarterTurn: { degrees, confidence, reason },
  skew: { degrees, confidence, reason },
  inconclusiveReasons: string[]
}
```

좌표는 `0..1` 정규화된 시각 좌표다. 모든 수는 유한해야 하고 경계는 `0 <= left < right <= 1`, `0 <= top < bottom <= 1`을 만족해야 한다.

분석 규칙은 다음과 같다.

- 흰색을 가정하지 않고 가장자리와 저경사 샘플에서 배경 밝기를 추정한다.
- 확인된 가장자리 띠를 제외한 내부 잉크 비율과 대비로 빈 페이지 점수를 계산한다.
- 기울기는 downsample된 이진 edge 표본의 projection score로 계산하고 `-5.0..+5.0`도를 벗어나면 분석 불가로 처리한다.
- 180도 방향은 픽셀 projection만으로 확정하지 않는다. vendored Tesseract의 orientation 값은 선택적 보조 신호로만 사용하고 낮은 신뢰도에서는 원본 방향을 유지한다.
- 사진, 희미한 연필, 장식 테두리, 표, 필기, 여러 쓰기 방향은 불확실 사유가 될 수 있다.
- `background.neutral`은 배경 표본의 RGB channel 범위가 12 이하이고 표본 분산·포화도 한도를 모두 통과했을 때만 true다. `photo_like`, `faint_content`, `colored_paper`가 있으면 배경 정규화 대상이 아니다.

`web/page-analysis.mjs`는 PDF.js 세션과 자원 수명만 관리한다.

- pre-abort와 파일 크기를 먼저 확인한 뒤 `URL.createObjectURL(file)`로 PDF.js를 열고, document destroy가 끝난 뒤 URL을 정확히 한 번 revoke한다. `File.arrayBuffer()`로 전체 입력을 만들지 않는다. object URL을 만들 수 없는 환경은 명시적으로 지원하지 않는다.
- 한 번에 한 페이지만 `await`한다.
- 분석 Canvas와 `ImageData`를 live-pixel budget에 함께 계산한다.
- 페이지 완료 즉시 `ImageData` 참조를 버리고 Canvas를 0×0으로 만들고 PDF.js page cleanup을 수행한다.
- 취소·오류·`pagehide`에서 render task, PDF document, loading task, object URL을 정리한다.
- 한도 초과를 빈 페이지나 정상 페이지로 바꾸지 않고 `inconclusive` 또는 명시적 오류로 반환한다.

## 보존형 출력

Go에 원자적 page-plan API를 추가한다.

```go
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
```

계약은 다음과 같다.

- `edits` 길이는 입력 페이지 수와 정확히 같고 `Page`는 1부터 연속이어야 한다.
- `QuarterTurns`는 `0..3`만 허용한다.
- `CropBox`는 유한하고 양의 면적이며 해당 페이지의 유효 source box 안에 있어야 한다.
- PDF.js viewport의 네 모서리를 PDF 좌표로 변환한 축 정렬 사각형만 Go로 전달한다.
- 선택된 페이지를 한 번의 `buildOrdered` 계열 작업으로 재구축한다.
- Security Audit가 먼저 제공한 `ParseBounded`와 `PagesBounded`로 source xref/object/object-stream/page-tree를 읽는다. source read 기본은 page 500, xref/object 각 100,000, edge 1,000,000, depth 64, token 8MiB, 누적 parsed string/name 64MiB, decoded stream 개별 64MiB·누적 256MiB다. `GetBounded`/`ResolveBounded` 오류를 숨기지 않으며 session-wide source counter를 계속 소비한다.
- `reachableBounded/importDocBounded/finalizeBounded`는 source를 읽을 때 `GetBounded`를 사용하고 별도 output counter로 object 100,000개, edge 1,000,000개, direct-container depth 64를 기본 적용한다. visited/map/queue/dictionary/array에 항목을 추가하거나 recursive frame을 만들기 전에 budget을 소비하며 초과 시 output spool을 쓰기 전에 실패한다.
- `ApplyPagePlan`은 256MiB 기본 hard cap을 적용해 `[]byte`를 반환하는 편의 API이고, WASM은 `WritePagePlan`에 shared `BoundedChunkSpool`을 직접 전달한다. serializer는 cap을 넘길 다음 write를 받기 전에 실패하므로 cap을 넘는 전체 결과를 먼저 만들지 않는다.
- 유지한 페이지의 변경하지 않은 stream은 원본 encoded bytes와 filter를 그대로 사용한다.
- 결과는 최소 한 페이지를 포함해야 한다.
- CropBox는 보이는 영역만 숨기며 원본 바깥 픽셀을 파일에서 삭제하지 않는다.

페이지를 제거하거나 순서를 바꾸면 현재 writer 정책에 따라 `AcroForm`, `Outlines`, `StructTreeRoot`, `PageLabels`, `Names`, `Dests` 같은 page-indexed 구조가 제거될 수 있다. Info·XMP와 페이지 annotation은 남을 수 있다. 실행 전 이 차이를 표시하며 모든 rewrite는 기존 전자서명을 무효화한다.

## 래스터형 출력

래스터형은 임의 deskew와 실제 픽셀 제거를 제공한다.

- 출력 해상도 preset은 150/200/300 DPI지만 페이지당 최대 16,000,000픽셀과 한 변 16,384픽셀을 넘길 수 없다.
- 누적 출력 raster는 최대 64,000,000픽셀이다.
- source Canvas, destination Canvas와 active `ImageData`를 합산한 live surface는 최대 16,000,000 RGBA 픽셀이다. geometry draw가 끝나면 source Canvas를 0×0으로 만든 뒤에만 destination `getImageData()`를 호출하고, 정규화한 pixels를 다시 쓴 뒤 `ImageData` reference를 버린 다음 encode한다. scale은 `max(source+destination, destination+ImageData)`가 cap 안이 되게 낮추고 실제 적용값을 사용자에게 표시한다.
- 작은 각도 회전 뒤 생긴 빈 영역은 흰색으로 채운다.
- 검토된 가장자리 band만 제거하거나 흰색으로 덮는다.
- 선택한 crop 이후 물리 페이지 크기를 계산하고 0보다 큰 크기만 허용한다.
- 기본 `균형` preset은 브라우저에서 흰색 배경 JPEG quality 0.85로 인코딩한다. `무손실` preset은 PNG를 사용한다.
- 페이지 encoded bytes는 최대 64MiB, 누적 image bytes와 최종 PDF는 각각 최대 256MiB다.

배경 정규화는 래스터형의 명시적 opt-in이며 기본은 꺼짐이다. 분석 결과가 `background.confidence == "high"`, `background.neutral == true`이고 `photo_like`, `faint_content`, `colored_paper` 사유가 없을 때만 page별 checkbox를 활성화한다. 활성화한 page는 geometry 변환 뒤 encode 전에 다음 고정 변환을 적용한다.

1. 불투명 pixel의 sRGB luminance `Y`와 분석된 배경 `B`를 사용한다.
2. RGB channel 최대값과 최소값 차이가 12보다 크거나 `Y < B - 32`이면 pixel을 바꾸지 않는다.
3. 나머지는 `t = clamp((Y - (B - 32)) / 32, 0, 1)`, `lift = round((255 - Y) * 0.60 * t)`를 계산하고 R/G/B 각각에 같은 `lift`를 더해 255에서 자른다. alpha는 바꾸지 않는다.

따라서 어두운 잉크와 유색 pixel은 그대로이고 neutral background만 보수적으로 밝아진다. 사용자가 분석 불가를 강제로 정규화하는 control은 제공하지 않는다.

`RasterPDFEncoder`는 PNG와 JPEG를 명시적으로 구분해 받는다. JPEG는 Go `image/jpeg.DecodeConfig`로 크기만 검증한 뒤 원본 JPEG bytes를 `/DCTDecode` 이미지로 직접 embed한다. PNG는 기존처럼 alpha를 흰색에 합성하고 RGB Flate stream으로 기록한다. 두 형식 모두 픽셀·dimension·byte budget을 동일하게 적용한다.

```go
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
```

래스터 출력은 다음 항목을 의도적으로 제거한다.

- 선택 가능한 텍스트와 vector
- 링크, annotation, form/XFA
- 첨부파일, JavaScript, outline, page label
- Info/XMP, 접근성 구조
- 암호화와 전자서명

`RasterPDFEncoder`는 객체를 destination writer에 직접 기록하며 page stream 전체를 `rasterObjectBytes` 같은 중간 buffer로 복사하지 않는다. 완료 전 청크 reader를 받는 canonical `ValidateRasterOnlyPDFStream`을 통과해야 하며 실패하면 다운로드를 제공하지 않는다. validator는 object 번호·generation·xref offset·정확한 `/Size`, 허용 dictionary/filter, stream length와 source canary 부재를 bounded sequential read로 검사한다. 원본 canary가 raw output이나 decoded image 외 구조에 남지 않는 fixture를 사용한다.

## 상태와 경쟁 조건

- 분석 session은 input content identity와 설정 revision을 snapshot한다.
- 입력, threshold, preset 또는 페이지 override가 바뀌면 기존 plan과 output을 stale 처리한다.
- 새 분석·새 export·취소·`pagehide`는 이전 `AbortController`, PDF render task와 WASM Worker를 종료한다.
- Worker protocol은 다음 상태형 명령만 허용한다.

```text
sourceStart({size, limits})              -> {revision, maxChunkBytes}
sourceChunk(revision, offset, data)      -> {received}
sourceFinish(revision)                   -> {state:"ready"}
applyPlan(revision, edits)               -> {outputRevision, size}
rasterStart({pageCount, opts})           -> {revision, maxChunkBytes}
pageStart(revision, {size, format, widthPt, heightPt}) -> {state:"receiving"}
pageChunk(revision, offset, data)         -> {received}
pageFinish(revision)                      -> {pagesAdded}
rasterFinish(revision)                    -> {outputRevision, size}
outputRead(outputRevision, maxBytes=1MiB) -> {data, done}
outputRelease(outputRevision)             -> {state:"released"}
bridgeStats()                             -> ScanCleanupBridgeStats
abort()                                   -> {state:"aborted"}
```

위 표기는 command payload의 shorthand다. 실제 WASM global은 positional argument가 아니라 `{command,...payload}` 객체 하나만 받는다. `web/scancleanup/protocol.mjs`가 boot handle의 `{json}` 또는 `{data,json}` 응답을 exact state/revision schema로 normalize하고 source/page wire revision을 shared helper의 `uploadRevision`으로 map한 `run(command,payload)` adapter를 제공한다. `outputRead.maxBytes`는 정확히 1MiB만 허용하고 product error는 transport reject와 구분한다.

- source와 page chunk는 0부터 strictly contiguous offset이고 각 1MiB 이하다. WASM bridge는 `jsu.Bytes`로 중간 Go slice를 만들지 않고 `js.CopyBytesToGo`로 미리 할당한 destination 범위에 직접 한 번 복사한다.
- `sourceFinish`와 `pageFinish`는 선언한 길이가 정확히 채워진 경우만 성공한다. 새 `sourceStart`/`rasterStart`, 잘못된 sequence, fatal parse/encode 오류는 이전 session과 output을 폐기한다.
- `applyPlan`과 `rasterFinish`는 최대 1MiB chunk를 보관하는 bounded spool로 직렬화한다. `outputRead`는 Go chunk를 새 JS `Uint8Array`로 한 번 복사한 뒤 spool에서 그 범위를 제거하며, `outputRelease`는 unread chunk를 모두 지운다. `bytes.Buffer -> append -> jsu.Out` 전체 결과 복사는 없다.
- 보존형 source와 래스터 page encoded chunk는 전용 Worker에 ownership-transfer한다. 이 transfer는 main-thread 사전 사본만 없애며 JS→Go 복사 자체를 없앤다고 주장하지 않는다. Worker가 없거나 load/error/messageerror가 발생하면 main-thread fallback 없이 실패한다.
- `pageFinish` 응답을 받은 뒤에만 해당 page Blob/Canvas를 해제한다.
- 실제 출력은 검토한 plan revision과 일치할 때만 다운로드할 수 있다.
- export는 directory/OPFS/64MiB-memory sink와 byte-limit wrapper를 먼저 연다. vector `sourceStart.limits.maxOutputBytes`와 raster `rasterStart.opts.maxOutputBytes`는 `min(256MiB, sink.maxBytes)`다. 따라서 memory 환경에서는 serializer 자체가 64MiB+1 write를 allocation 전에 거부하고 70MiB Go spool을 먼저 만들지 않는다.
- 각 export generation은 local `{owner,handle,sink,cleanupPromise}`를 가진다. 새 export는 이전 cleanup을 await하고, old finally는 current owner일 때만 `abort`/`dispose`/partial sink cleanup을 수행한다. old cleanup이 새 Worker나 새 partial sink를 제거하지 못한다.
- Scan Task 5가 `web/output-sinks.mjs`에 공통 `createByteLimitedSink(sink,maxBytes)`와 `createDelayedCleanupRegistry(options)`를 먼저 둔다. 전자는 memory·OPFS·directory 모두 read-only `kind/maxBytes/bytesWritten`을 제공하고 cap 초과 write를 underlying sink 전에 거부한다. 후자는 성공 download의 Object URL과 closed OPFS sink를 10초 timer 또는 `pagehide`에서 같은 idempotent Promise로 revoke/cleanup한다.
- memory/OPFS close가 Blob/File을 반환하면 anchor click 전에 `{url,cleanup:()=>closedSink.cleanup()}`을 registry에 등록한다. timer·명시 cleanup·pagehide 경쟁에서도 URL revoke와 OPFS remove는 각각 한 번이다. user-selected directory close는 `null`이고 성공 파일을 registry에 등록하거나 제거하지 않는다. stale close는 download 없이 즉시 closed sink를 cleanup한다.

새 catalog descriptor는 `"runtime":{"worker":"required","stateful":true,"chunkedIO":true}`를 가진다. 페이지는 `window.scanCleanupWasm = boot("./scancleanup.wasm", {requireWorker:true, transferOwnership:true})`로 기존 then/await 호환 Promise에 `{run,cancel,dispose}`를 붙인 공유 handle을 만들며, `requireWorker` 때문에 main-thread runtime은 instantiate되지 않는다. 순차 명령은 같은 Worker를 유지하고 동시 새 실행·취소만 기존 명령과 Worker를 중단한다.

## 메모리 모델과 output sink

앱이 통제하는 peak 목표는 `원본 1 + 최종 결과 1 + 현재 page/1MiB 청크`다.

- 보존형은 PDF.js 분석 session과 object URL을 먼저 destroy/revoke한 후 File slice를 1MiB씩 Go source로 보낸다. 결과를 spool에 모두 직렬화한 직후 Go source/doc를 해제한다. output drain 중에는 `Go spool의 미전송 bytes + sink에 이미 기록한 bytes`가 결과 한 개 크기 안에서 이동한다.
- 래스터형은 PDF.js source 한 개와 Go output spool을 장기 보유하고, source/destination Canvas, encoded page staging과 bridge chunk만 현재 page 경계로 추가한다. 전체 page Canvas나 Blob 배열은 없다.
- directory, OPFS, bounded-memory 순으로 sink를 선택한다. directory/OPFS를 사용할 수 없는 memory fallback은 64MiB hard cap이며, 더 큰 결과는 전체 memory Blob을 만들기 전에 저장 위치가 필요하다는 오류로 중단한다.
- serializer와 sink 모두 256MiB 최종 cap을 적용한다. serializer가 다음 write를 받기 전에 remaining budget을 검사하므로 oversized 전체 결과를 만든 뒤 거부하지 않는다.

브라우저 엔진과 PDF.js 내부의 구현별 복사까지 0이라고 주장하지 않는다. 대신 Go session의 source/page/output retained bytes, spool의 max retained bytes, JS uploader의 최대 live chunk/Blob/Canvas와 sink byte 수를 계측해 위 앱 소유 불변식을 검증한다.

`ScanCleanupBridgeStats`는 `SourceCopyCalls`, `SourceCopiedBytes`, `PageCopyCalls`, `PageCopiedBytes`, `OutputCopyCalls`, `OutputCopiedBytes`, `MaxTransientCopyBytes`, `SourceRetainedBytes`, `PageRetainedBytes`, `SpoolRetainedBytes`, `PeakSpoolRetainedBytes`와 nested `ReadStats PDFReadStats`의 lowerCamel JSON field를 가진다. 실제 TinyGo `bridgeStats`는 각 `js.CopyBytesToGo/ToJS` 직후 갱신하고 UI에는 표시하지 않는다. vector apply 뒤 source retained, pageFinish 뒤 page retained, outputRelease/terminal cleanup 뒤 spool retained가 각각 0이고 source parser/page-tree/cache stats가 resolved limit 안인지 actual browser E2E가 확인한다.

## 자원 한도

| 자원 | 한도 |
|---|---:|
| 입력 PDF | 256MiB |
| 페이지 | 500 |
| xref entry | 100,000 |
| imported object | 100,000 |
| graph edge | 1,000,000 |
| direct-container depth | 64 |
| 단일 token | 8MiB |
| 누적 parsed string/name | 64MiB |
| decoded stream | 개별 64MiB, 누적 256MiB |
| 분석 raster | 페이지당 2,000,000픽셀 |
| live RGBA surface | 합산 16,000,000픽셀 |
| 출력 raster | 페이지당 16,000,000픽셀 |
| 누적 출력 raster | 64,000,000픽셀 |
| 페이지 encoded image | 64MiB |
| 누적 encoded image | 256MiB |
| 최종 PDF | 256MiB |
| Worker input/output chunk | 각 1MiB |
| memory sink | 64MiB |
| 동시 페이지·연산 | 1 |

메모리 목표는 원본 PDF + 결과 PDF + 현재 페이지의 제한된 raster/encoded buffer다. 전체 페이지 Canvas나 PNG 배열은 보관하지 않으며, Go result와 JS result의 전체 크기 사본을 동시에 만들지 않는다.

## 접근성·다국어·오프라인

- 페이지 제안은 표와 카드 어느 레이아웃에서도 같은 DOM 순서와 keyboard 조작을 제공한다.
- 신뢰도는 색만으로 표현하지 않고 텍스트와 reason code를 함께 표시한다.
- 진행·경고는 polite live region, 실행 실패는 assertive alert로 전달한다.
- English/Korean source copy와 ja/zh/es/fr/de dictionary를 모두 제공한다.
- 첫 온라인 로드 후 필요한 모듈, PDF.js worker, 선택한 OCR model과 WASM이 service worker에 캐시됐을 때 오프라인 재실행이 가능해야 한다. 아직 캐시되지 않은 선택적 OCR model은 정확한 오류와 수동 회전 fallback을 제공한다.

## 검증

### Go

- inherited rotation과 비영점 MediaBox/CropBox에서 page plan 좌표 왕복
- 유효하지 않은 plan 길이·페이지 번호·각도·NaN/Inf·역전 crop 거부
- 유지 페이지 stream bytes 비재인코딩
- bounded serializer가 output cap 직전까지 쓰고 cap+1 write를 받기 전에 실패하며, 전체 output `[]byte`를 만들지 않음을 injected writer와 allocation counter로 검증
- bounded graph import가 object/edge/depth cap을 queue/map/dict/array 추가 전에 적용하고 cap+1에서 output chunk allocation이 0임을 검증
- 빈 페이지 전부 제거 거부
- topology 변화 시 보존·제거 구조를 명시적으로 검증
- JPEG raw embed와 PNG alpha 합성, 각 format budget, 잘못된 header 거부
- raster object 직접 write, chunk spool consume 후 retained-byte 감소, streaming whitelist의 exact xref·`/Size`와 source canary 부재

### JavaScript

- 순백·회색 배경·희미한 글자·작은 점·검은 scanner band·의도적 frame·사진·표 fixture
- 배경 정규화 default-off와 neutral gray 배경 개선, dark stroke byte identity, colored paper·사진·희미한 필기 비활성화 fixture
- 0/90/180/270도 및 `-5..+5`도 synthetic skew
- 낮은 신뢰도는 자동 삭제·crop·rotation을 하지 않음
- 페이지 500개에서 순차 처리와 live resource 최대값 검증
- raster exporter가 source+destination draw 뒤 source를 해제하고 destination+ImageData 단계로 전환해 어느 sample에서도 16,000,000 live pixels를 넘지 않음을 검증
- pre-abort, render 오류, 설정 변경, 빠른 재실행, `pagehide`, Worker 오류의 cleanup
- object URL이 pre-abort에서는 생성되지 않고 success/error/cancel/pagehide에서 생성·revoke 각 한 번임을 검증
- 1MiB slice uploader가 전체 `File.arrayBuffer()`를 호출하지 않고 transferred caller buffer를 detach하며, encoded page 하나만 live이고 `pageFinish` acknowledgement 후 해제됨을 검증
- Go source/page/output 및 JS chunk/sink 계측값으로 `source + result + bounded current page/chunk` 상한을 검증
- memory sink를 먼저 선택해 64MiB effective cap이 Go serializer에 전달되고 64MiB+1 output이 추가 spool allocation 전에 실패함을 검증
- delayed old cleanup과 즉시 새 export에서 owner token이 fresh Worker/sink를 보존함을 검증
- 성공 OPFS download의 timer→pagehide, pagehide→timer, manual→timer 경쟁에서 URL revoke와 remove 각 1회, directory remove 0회, pending cleanup 0을 검증

### 브라우저 E2E

- 보존형에서 text/vector 추출 가능성과 시각 crop/rotation
- 래스터형에서 작은 deskew·border 제거 pixel 검증
- opt-in 배경 정규화 전후 background/dark-ink/control pixel 검증
- 래스터형 구조 손실 경고와 확인 gate
- 취소 후 새 session 실행
- Chromium·Firefox·WebKit·Pixel 5·iPhone 13 에뮬레이션
- 한국어와 비영어 generated URL에서 절대 asset 경로와 번역 확인

## 제외 범위

- 픽셀 분석만으로 180도 방향을 항상 맞춘다는 보장
- 필기·세로쓰기·RTL·사진 문서의 자동 보정 정확도 보장
- 보존형 CropBox로 숨긴 픽셀이 물리적으로 삭제됐다는 주장
- 기존 전자서명 보존
- 사용자 확인 없는 빈 페이지 자동 삭제
- server OCR 또는 파일 업로드

## 완료 조건

1. 두 출력 모드가 같은 분석 plan을 사용하되 손실 차이를 분명히 표시한다.
2. 보존형은 유지 페이지의 원본 stream을 재인코딩하지 않는다.
3. 래스터형은 실제 deskew·border 제거를 수행하고 whitelist 검증을 통과한다.
4. 불확실한 분석 결과는 원본 유지가 기본이며 사용자 검토 없이 삭제되지 않는다.
5. 원본+결과+현재 페이지 경계를 넘는 전체-page 누적 buffer가 없다.
6. vector/raster serializer와 bridge가 cap을 초과한 전체 결과나 JS/Go 전체 결과 사본을 먼저 만들지 않는다.
7. 모든 한도·취소·오류 경로가 자동 테스트로 검증된다.
8. Go 외부 모듈, commit, push를 추가하지 않는다.
9. source parse/page traversal/cache가 공통 bounded reader와 session-wide `ReadStats` 한도 안에서 output allocation 전에 실패한다.
10. 성공한 memory/OPFS download URL과 OPFS 임시 파일은 timer 또는 pagehide에서 정확히 한 번 정리되고 directory 결과는 보존된다.
