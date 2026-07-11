# 통합 문서 작업공간 구현 계획

## 원칙

- 설계: `docs/superpowers/specs/2026-07-09-integrated-workspace-design.md`
- Go 외부 의존성 추가 금지
- production code보다 실패 테스트를 먼저 작성
- 각 RED 단계에서 예상 원인으로 실패하는지 확인
- 커밋과 푸시는 사용자 지시에 따라 전부 생략
- 각 기능 완료 후 spec compliance 검토와 code quality 검토를 별도로 수행

## Task 1: Worker 호출 계약 복구

파일:

- 수정: `web/wasm-client.mjs`
- 수정: `web/wasm-worker.js`
- 수정: `web/app.js`
- 수정: 35개 Go WASM 도구 `web/*/index.html`
- 테스트: `web/wasm-client.test.mjs`
- 테스트: `tests/worker-contract.test.mjs`

절차:

1. 다중 위치 인자와 중첩 typed array를 보존하는 실패 테스트를 작성한다.
2. `node --test web/wasm-client.test.mjs tests/worker-contract.test.mjs`가 기존 단일 인자 계약 때문에 실패하는지 확인한다.
3. 요청을 `{id, wasm, args}`로 통일하고 Worker에서 `pdfRun(...args)`를 호출한다.
4. 각 실행은 전용 Worker를 사용하고 취소·오류 시 terminate한다.
5. 모든 HTML 호출부를 `const r = await runWasm(...)`로 바꾼다.
6. main-thread fallback도 `__syncPdfRun(...args)`로 호출한다.
7. 위 테스트와 `node --test`를 실행한다.

완료 조건:

- `pdfdiff` 두 인자와 `stamp` 다중 옵션이 모두 Worker·fallback 경로에서 동일하게 전달된다.
- Promise를 `finish`에 직접 넘기는 페이지가 0개다.
- 같은 Worker에서 다른 WASM 전역이 섞이지 않는다.

## Task 2: Operation catalog와 Artifact

파일:

- 생성: `tools/operation-catalog.json`
- 생성: `web/operation-catalog.mjs`
- 생성: `web/artifact.mjs`
- 수정: `build.sh`
- 수정: `tools/gen-i18n.mjs`
- 테스트: `tests/operation-catalog.test.mjs`
- 테스트: `web/artifact.test.mjs`

절차:

1. 현재 35개 WASM과 38개 도구 목록을 snapshot 테스트로 고정한다.
2. 누락·중복 ID, 잘못된 engine, 입출력 종류, capability 조합이 실패하는 테스트를 작성한다.
3. JSON catalog와 브라우저 adapter를 만든다.
4. `build.sh`는 Node helper가 출력한 WASM 목록을 읽도록 바꾼다.
5. `gen-i18n.mjs`는 같은 catalog에서 도구 slug를 읽는다.
6. Artifact의 Blob 수명, revision, immutable metadata, URL revoke 테스트를 작성하고 구현한다.
7. catalog tests, `./build.sh`, i18n 생성 결과를 검증한다.

완료 조건:

- 도구 목록의 단일 원본은 `tools/operation-catalog.json`이다.
- build·i18n·브라우저 catalog가 같은 ID 집합을 사용한다.

## Task 3: OperationRunner와 공통 PDF renderer

파일:

- 생성: `web/operation-runner.mjs`
- 생성: `web/operation-worker.js`
- 생성: `web/pdf-renderer.mjs`
- 테스트: `web/operation-runner.test.mjs`
- 테스트: `web/pdf-renderer.test.mjs`
- 수정: `web/thumbs.js`
- 수정: `web/pdf2img/pdf2img.mjs`

절차:

1. 타입·cardinality·params 검증, progress, cancel, fallback 실패 테스트를 작성한다.
2. runner가 descriptor를 검증한 뒤 전용 Worker를 만들도록 구현한다.
3. loading/running/finalizing/done 상태를 전달한다.
4. PDF.js load/render/cancel/destroy를 DOM 독립 adapter로 분리한다.
5. object URL, bitmap, render task가 취소·완료 때 정리되는 테스트를 작성한다.
6. thumbs와 pdf2img를 공통 renderer로 이전한다.
7. Node tests와 PDF 관련 Playwright smoke를 실행한다.

## Task 4: 미리보기와 결과 재사용

파일:

- 생성: `web/preview-controller.mjs`
- 생성: `web/preview.css`
- 생성: `web/preview-elements.mjs`
- 수정: `web/app.js`
- 테스트: `web/preview-controller.test.mjs`
- 테스트: `tests/e2e/preview.spec.mjs`

절차:

1. 같은 input/params revision에서 연산이 한 번만 실행되는 실패 테스트를 작성한다.
2. 설정 변경 시 stale 표시와 cache 무효화 테스트를 작성한다.
3. PDF·image·text·summary preview adapter를 구현한다.
4. `finish`를 result normalization과 explicit download로 분리한다.
5. 공통 preview UI를 capability가 있는 도구에 연결한다.
6. 미리보기 bytes와 다운로드 bytes가 같은 E2E를 실행한다.

## Task 5: Typed pipeline과 workflow UI

파일:

- 생성: `web/pipeline.mjs`
- 생성: `web/pipeline.test.mjs`
- 생성: `web/workflow/index.html`
- 생성: `web/workflow/workflow.mjs`
- 생성: `web/workflow/workflow.css`
- 수정: `web/index.html`
- 수정: `tools/meta-i18n.json`
- 수정: `tools/gen-i18n.mjs`
- 테스트: `tests/e2e/workflow.spec.mjs`

절차:

1. 타입 불일치, cardinality, terminal 이후 단계, sidecar 누락 실패 테스트를 작성한다.
2. 단계별 revision cache와 앞 결과 재사용 테스트를 작성한다.
3. 중간 실패·취소 시 이후 단계가 실행되지 않는 테스트를 작성한다.
4. 선형 pipeline validator와 executor를 구현한다.
5. 단계 추가·정렬·삭제·설정 JSON 내보내기/가져오기 UI를 구현한다.
6. Reorder → Compress → Metadata fixture E2E를 실행한다.

## Task 6: Batch executor와 실제 streaming ZIP

파일:

- 생성: `web/batch.mjs`
- 생성: `web/batch.test.mjs`
- 생성: `web/batch/index.html`
- 생성: `web/batch/batch-page.mjs`
- 생성: `web/output-sinks.mjs`
- 수정: `web/zip.js`
- 수정: `web/pdf2img/zip.mjs`
- 테스트: `web/pdf2img/zip.test.mjs`
- 테스트: `tests/e2e/batch.spec.mjs`

절차:

1. async iterable entry가 한 번에 하나만 live인 실패 테스트를 작성한다.
2. sink backpressure, CRC, offset, entry count, 실패 cleanup 테스트를 작성한다.
3. independent/grouped batch, concurrency=1, retry, 이름 충돌 테스트를 작성한다.
4. directory/OPFS/memory sink를 기능 탐지 순서로 구현한다.
5. 500개 fixture를 순차 처리하고 peak live artifact 수가 제한되는지 검증한다.

## Task 7: 시각적 PDF 비교

파일:

- 생성: `web/pdfdiff/pixel-diff.mjs`
- 생성: `web/pdfdiff/page-align.mjs`
- 생성: `web/pdfdiff/diff-worker.mjs`
- 생성: `web/pdfdiff/visual.mjs`
- 수정: `web/pdfdiff/index.html`
- 테스트: `web/pdfdiff/pixel-diff.test.mjs`
- 테스트: `web/pdfdiff/page-align.test.mjs`
- 테스트: `tests/e2e/pdfdiff-visual.spec.mjs`

절차:

1. 동일·1px·threshold·크기·alpha·bbox 실패 테스트를 작성한다.
2. 삽입·삭제·빈 페이지·bounded fallback 정렬 테스트를 작성한다.
3. 순수 RGBA diff와 bounded alignment를 구현한다.
4. PDF.js lazy render와 diff Worker를 연결한다.
5. side-by-side, slider, blink, heatmap과 changed-only filter를 구현한다.
6. 텍스트 보고서와 visual JSON/이미지 ZIP export를 유지한다.

## Task 8: 검색 가능한 OCR PDF

파일:

- 생성: `pdf/ocr.go`
- 생성: `pdf/ocr_test.go`
- 생성: `wasm/ocrpdf/main.go`
- 수정: `web/ocr/ocr.mjs`
- 수정: `web/ocr/index.html`
- 테스트: `tests/e2e/ocr-pdf.spec.mjs`

절차:

1. `OCRWord`, `OCRPage`, `OCRLayerOpts`의 좌표·예산 validation 실패 테스트를 작성한다.
2. 영어·한글, page box, Rotate 0/90/180/270 추출 왕복 테스트를 작성한다.
3. 단일 subset font, collision-free resource, invisible text matrix를 구현한다.
4. Tesseract word boxes를 정규화해 WASM에 전달한다.
5. TXT와 searchable PDF 출력 선택을 UI에 추가한다.
6. 원본 영상 pixel diff 0과 PDF.js text content 위치를 E2E로 검증한다.

## Task 9: 안전한 raster-only 영구 삭제

파일:

- 생성: `pdf/rasterpdf.go`
- 생성: `pdf/rasterpdf_test.go`
- 생성: `wasm/redact/main.go`
- 생성: `web/redact/index.html`
- 생성: `web/redact/redact.mjs`
- 생성: `web/redact/redact.css`
- 테스트: `tests/e2e/redact.spec.mjs`

절차:

1. 페이지별 WidthPt/HeightPt와 pixel·page·PNG 예산 validation 테스트를 작성한다.
2. canary가 content, metadata, annotation, form, embedded file, JavaScript, incremental revision에 있는 fixture를 작성한다.
3. 원본 객체를 import하지 않는 raster-only PDF builder를 구현한다.
4. object graph whitelist와 raw/decoded stream canary 검사를 구현한다.
5. PDF.js 선택 UI와 바깥 반올림+안전 여백 rasterization을 구현한다.
6. 4종 회전·비영점 box·UserUnit과 실제 삭제 pixel을 E2E로 검증한다.

## Task 10: Direct Send v2

파일:

- 생성: `web/send/protocol.mjs`
- 생성: `web/send/integrity.mjs`
- 생성: `web/send/storage.mjs`
- 생성: `web/send/storage-worker.mjs`
- 수정: `web/send/send.mjs`
- 수정: `web/send/index.html`
- 테스트: `web/send/protocol.test.mjs`
- 테스트: `web/send/storage.test.mjs`
- 테스트: `web/send/send.test.mjs`
- 테스트: `tests/e2e/send-v2.spec.mjs`

절차:

1. manifest·chunk·offset·순서·상한 상태 머신 실패 테스트를 작성한다.
2. SHA-256 mismatch, NACK, 최대 3회 retry 테스트를 작성한다.
3. 다중 파일·zero-byte·중복 이름·resume offset 테스트를 작성한다.
4. memory, OPFS, directory ReceiveSink와 quota/abort cleanup을 구현한다.
5. v2 hello 협상과 단일 파일 v1 fallback을 구현한다.
6. 두 탭 DataChannel 전송·disconnect/reconnect·resume E2E를 실행한다.

## Task 11: 통합·다국어·성능·보안 검증

파일:

- 수정: `README.md`
- 수정: `README.ko.md`
- 수정: `PROJECT_REVIEW.md`
- 수정: `.github/workflows/ci.yml`
- 수정: `tools/vendor-manifest.json` 필요 시

절차:

1. 모든 신규 페이지를 landing, metadata, i18n catalog에 추가한다.
2. `go test ./...`, `go test -race ./...`, `go vet ./...`를 실행한다.
3. `node --test`와 Playwright 전체 matrix를 실행한다.
4. TinyGo로 전체 WASM을 빌드하고 `wasm-opt`, size gate, staging을 검증한다.
5. OCR·redaction·pipeline 대용량 fixture로 memory·시간·취소를 측정한다.
6. security headers와 Service Worker cache에 신규 Worker/module을 포함한다.
7. `git diff --check`와 의도하지 않은 생성 파일 변경을 확인한다.
8. spec 요구사항별 증거 표를 보고서에 갱신한다.
