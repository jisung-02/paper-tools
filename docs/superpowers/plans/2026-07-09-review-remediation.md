# 프로젝트 리뷰 개선 실행 계획

## 공통 규칙

각 작업은 다음 순서를 지킨다.

```text
실패 테스트 추가 → 해당 테스트의 실패 확인 → 최소 구현 → 해당 테스트 통과 → 관련 묶음 검증
```

작업자는 커밋·push를 수행하지 않는다. 같은 파일을 동시에 수정하지 않도록 `pdf/ops.go`, `web/app.js`, `web/thumbs.js`, workflow 파일은 각각 하나의 작업 흐름에서만 변경한다.

## 1. 브라우저 E2E 기반과 즉시 결함

1. `package.json`, `playwright.config.mjs`, `tests/e2e/static-server.mjs`, `tests/e2e/tools.spec.mjs`를 추가한다.
   - `.wasm`을 `application/wasm`으로 제공한다.
   - RED: `/crop/`에서 pageerror 없이 run 버튼이 준비된다는 테스트를 작성한다.
   - 구현: `web/crop/index.html`의 `top` 변수를 `topInput`으로 바꾼다.
   - 검증: Chromium 35개 도구 boot smoke와 Crop PDF 다운로드.
2. `web/app.js` URL helper 테스트를 `tests/e2e/language.spec.mjs`에 추가한다.
   - RED: `ko-KR` 신규 context의 `/send/?x=1#r=...`가 `/ko/send/?x=1#r=...`로 이동해야 한다.
   - 구현: URL 객체의 pathname만 변경하고 `detectBrowserLang`이 이동 여부를 반환하도록 한다.
   - 검증: Chrome·Firefox·WebKit에서 query/hash 보존.
3. `web/app.js`, `web/sw.js`, `tests/e2e/pwa.spec.mjs`를 수정한다.
   - RED: 자동 언어 이동에서 unhandled rejection이 없고, persistent profile offline 재실행이 성공해야 한다.
   - 구현: redirect 중 초기화 생략, register rejection 처리, `event.waitUntil(cache.put)`.
   - 검증: `/txt2pdf/` online 실행 후 context 재시작·offline 실행.

## 2. parser와 이미지 경계

4. `pdf/text_test.go`에 CMap count/overflow 예산 RED 테스트를 추가하고 `pdf/text.go`를 수정한다.
   - `<FFFFFFFE> <FFFFFFFF>`은 subprocess timeout으로 확인한다.
   - `uint64` 범위 계산과 1/2-byte code 폭, 전체 mapping budget을 적용한다.
5. `pdf/docx_test.go`, `pdf/hwpx_test.go`, `pdf/hwp_test.go`, 신규 `pdf/office_limits.go`를 추가한다.
   - 작은 주입 limits로 entry, expanded byte, XML token, text output 초과를 RED로 만든다.
   - 구현 후 malformed XML·deflate 실패는 부분 결과가 아닌 오류여야 한다.
6. `xlsx/xlsx_test.go`, `xlsx/xlsx.go`를 수정한다.
   - RED: `XFE1`, row `1048577`, overflow column, malformed shared string, 누락 sheet.
   - 구현: 좌표 validation, 오류 전파, row 단위 CSV writer와 출력 budget.
7. `imgconv/imgconv_test.go`, `imgconv/imgconv.go`를 수정한다.
   - RED: 4105×4105 virtual white image를 1×1로 줄여 white 유지.
   - 구현: `uint64` 누산과 DecodeConfig pixel budget.

## 3. PDF final graph와 구조 보존

8. `pdf/graph_test.go`를 추가하고 `pdf/graph.go`를 구현한다.
   - RED: root/Info/Encrypt 도달성, dead metadata canary 제거, dangling ref 실패, Parent/P cycle 종료.
   - 구현: mark, stable renumber, recursive Ref rewrite, sealed final graph.
9. `pdf/crypt_test.go`, `pdf/crypt.go`, `pdf/ops.go`를 수정한다.
   - RED: dead object 제거 뒤 AES-128/AES-256/RC4 round-trip.
   - 구현: Encrypt dictionary 할당 뒤 finalization, final object number로 암호화, finalization 뒤 번호 변경 금지.
10. `pdf/catalog_test.go`, `pdf/catalog.go`, `pdf/ops.go`, `pdf/meta.go`, `pdf/flatten.go`, `pdf/structure.go`를 수정한다.
    - RED: same-page transform Catalog·Info 보존, metadata strip raw canary 부재, Flatten AcroForm widget canary 부재, Remove 제외 페이지 canary 부재.
    - 구현: 작업별 preserve/remap/prune/remove policy.
11. `pdf/filters_test.go`, `pdf/filters.go`, `pdf/pdf.go`, `pdf/text.go`, `pdf/structure.go`를 수정한다.
    - RED: ASCII85→Flate, DecodeParms alignment, unsupported filter typed error, total limit.
    - 구현: filter pipeline; generic import는 raw stream 유지.
12. `pdf/overlay_test.go`, `pdf/geometry_test.go`, `pdf/overlay.go`, `pdf/stamp.go`, `pdf/transform.go`, `pdf/flatten.go`, `pdf/geometry.go`를 수정한다.
    - RED: 기존 resource 이름 충돌, 두 번 overlay, 0/90/180/270도 visual anchor.
    - 구현: 공통 resource allocator와 geometry matrix.
13. `pdf/size_test.go`, `pdf/compress.go`를 수정한다.
    - RED: unchanged stream hash 동일, dead object 미포함, 짧은/new operator stream의 작은 표현 선택.
    - 구현: size breakdown과 raw/Flate 선택; ObjStm은 추가하지 않는다.

## 4. 웹 상태·전송·Worker

14. `web/thumbs.test.mjs`, `web/thumbs.js`를 수정한다.
    - RED: 199/200/201/500 page order round-trip, hidden tail 보존, 선택 Set 보존.
    - 구현: DOM과 독립한 `order`, `selected`, `totalPageCount` state.
15. `web/thumbs.js`, `web/app.js`, `web/send/index.html`, `web/style.css`, E2E accessibility spec을 수정한다.
    - RED: keyboard 선택·이동, live status, dialog Escape/focus return.
    - 구현: button semantics, aria state, dialog focus management.
16. `web/send/send.test.mjs`, `web/send/send.mjs`를 수정한다.
    - RED: SDP encoded/decoded cap, invalid metadata, early/extra chunk, size mismatch, cancel cleanup.
    - 구현: bounded decoder와 receiver state machine.
17. `web/wasm-client.mjs`, `web/wasm-worker.js`, `web/app.js`, Merge 페이지, `tests/e2e/worker.spec.mjs`를 추가·수정한다.
    - RED: Worker UI heartbeat, cancel and restart, output equality.
    - 구현: request-id client, output transfer, main-thread fallback.
    - 이후 한 도구씩 async client로 이동하고 매 이동 후 35-tool smoke를 실행한다.

## 5. CI·공급망·보안·문서

18. `.github/workflows/ci.yml`, `.github/workflows/deploy.yml`을 수정한다.
    - RED: workflow static assertion으로 PR 검증, main deploy guard, fixed action SHA/checksum을 확인한다.
    - 구현: CI/deploy 분리, deploy `needs`, main ref guard, runner pin.
19. `tools/vendor-manifest.json`, `tools/verify-vendor.mjs`, `web/vendor/tesseract/SOURCES.txt`를 추가·수정한다.
    - RED: 63자리 digest fixture가 실패해야 한다.
    - 구현: 64자리 digest와 실제 file hash 비교, CI 실행.
20. `web/_headers`, CSP E2E spec, `README.md`, `README.ko.md`, `.github/dependabot.yml`을 수정한다.
    - RED: headers와 generated page audit 검사.
    - 구현: 기본 보안 헤더와 CSP Report-Only, 실제 검증 명령·toolchain 문서, dependency update policy.

## 6. 마감 검증

21. `node tools/gen-i18n.mjs`, Node test, Go test/race/vet, TinyGo build/test, `./build.sh`, 브라우저 matrix를 실행한다.
22. WASM bytes와 PDF size corpus를 변경 전 baseline과 비교하고 `PROJECT_REVIEW.md`의 결과를 갱신한다.
23. 명세 준수 검토 후 코드 품질 검토를 각각 독립적으로 수행한다. 커밋·push는 수행하지 않는다.
