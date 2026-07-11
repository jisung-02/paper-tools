# 통합 문서 작업공간 및 고급 기능 설계

## 목표

Paper Tools의 기존 개별 도구를 유지하면서 다음 기능을 공통 실행 기반 위에 추가한다.

1. 작업 전후 미리보기와 변경 요약
2. 검색 가능한 OCR PDF
3. 연속 작업 파이프라인
4. 시각적 PDF 비교
5. 안전한 영구 삭제
6. Direct Send 다중 파일·무결성·재개
7. 문서 일괄 자동화

모든 처리는 브라우저 안에서 수행하고 Go 구현에는 외부 모듈을 추가하지 않는다. 기존 38개 도구 URL과 단일 도구 UX는 유지한다.

## 선택한 구조

하이브리드 구조를 사용한다. 기존 도구는 전용 페이지를 유지하고, 공통 연산 카탈로그·실행기·Artifact·미리보기 계층을 공유한다. 여러 작업을 연결하는 `/workflow/`와 같은 처리를 여러 파일에 적용하는 `/batch/`를 별도로 추가한다.

단일 거대 앱은 초기 다운로드와 장애 범위를 키우고, 도구별 구현은 Worker·미리보기·메모리 관리가 중복된다. 하이브리드는 기존 진입점을 보존하면서 공통 실행 계약을 하나로 만든다.

## 선행 결함 수정

현재 `runWasm(input, options)`는 두 번째 이후 위치 인자를 버리고, 일부 페이지는 Promise를 `await`하지 않는다. Worker 하나에 여러 WASM을 로드하면 전역 `pdfRun`이 덮어써질 수도 있다. 신규 기능 전에 다음 불변식을 만든다.

- 연산 호출은 `invoke(operationId, args, options)` 한 형태만 사용한다.
- `args`의 개수와 순서를 보존하고 Worker는 `pdfRun(...args)`를 호출한다.
- 실행 중인 연산은 전용 Worker 하나를 소유한다.
- 취소·연산 변경·치명 오류 시 Worker를 종료한다.
- 모든 호출부는 Promise를 `await`한다.
- Worker 실패 시 원본 입력이 detach되지 않은 상태에서 검증된 main-thread fallback을 사용한다.

## 공통 데이터 모델

### OperationDescriptor

`web/operation-catalog.mjs`가 도구 목록의 단일 원본이다.

```js
{
  id,
  engine: "wasm" | "module" | "transport",
  entry,
  input: { kind, cardinality },
  output: { kind, cardinality },
  params,
  capabilities: { preview, pipeline, batch, terminal },
  build: { wasmPackage, outputPath }
}
```

빌드 스크립트와 다국어 생성기는 이 카탈로그에서 목록을 읽어 중복 목록 drift를 막는다.

### Artifact

`Artifact`는 File·Blob 중심으로 입력과 결과를 표현한다.

```js
{ id, revision, name, kind, mime, size, blob, metadata }
```

`Uint8Array`는 연산 경계에서만 만들고 장기 보관하지 않는다. 미리보기는 Blob URL을 사용하고 해제 시 반드시 revoke한다. 사용자 문서 자체는 자동 영속화하지 않는다.

### OperationRunner

`web/operation-runner.mjs`가 타입·개수·매개변수를 검증하고 `web/operation-worker.js`와 통신한다. 상태는 `queued`, `loading`, `running`, `finalizing`, `done` 단계로 보고한다. 모바일과 기본 데스크톱 동시성은 1이다.

## 작업 전후 미리보기

미리보기는 실제 연산을 한 번 실행하고 결과 Artifact를 캐시한다. 다운로드와 파이프라인은 같은 결과를 재사용한다. 입력 또는 설정 revision이 바뀌면 결과를 stale로 표시하고 재실행한다.

- PDF: PDF.js로 전후 페이지를 동기화해 렌더링한다.
- 이미지: `createImageBitmap`으로 전후 표시한다.
- 텍스트·CSV·JSON: DOM text node 기반 `<pre>`로 표시한다.
- ZIP·암호화 PDF·Office: 형식·크기·항목 수와 유실 가능 구조를 요약한다.
- 모바일: 좌우 대신 전/후 탭을 사용한다.
- 취소 시 PDF.js render task, Worker, object URL을 함께 정리한다.

## 연속 작업 파이프라인

파이프라인은 일반 그래프가 아닌 typed 선형 단계다.

```js
{ id, operationId, params, sidecars }
```

첫 단계만 Merge·Interleave·Images→PDF 같은 복수 입력을 허용한다. 이후 단계는 descriptor의 입출력 타입과 cardinality가 맞아야 한다. Protect처럼 이후 미리보기가 불가능한 연산은 terminal이다. 단계별 입력 revision과 설정 hash로 결과를 캐시해 뒤 단계만 바뀌면 앞 결과를 재사용한다. 설정 JSON만 저장·불러오기 할 수 있다.

## 문서 일괄 자동화

일괄 처리는 동일 파이프라인을 파일 집합에 적용한다.

- independent: 파일마다 별도 결과
- grouped: 입력 전체를 하나의 Merge·Images→PDF 작업으로 처리
- 기본 순차 실행으로 peak memory 제한
- 파일별 실패 격리·재시도·취소
- 중복 없는 출력 이름과 실패 manifest
- 사용자 디렉터리 sink, OPFS, 메모리 ZIP 순으로 기능 탐지
- ZIP writer는 async iterable entry와 async sink를 받아 입력과 출력을 전부 누적하지 않는다.

## 검색 가능한 OCR PDF

기존 vendored Tesseract의 `getTextBoxes("word")`를 사용해 단어 텍스트·confidence·사각형을 얻는다. 좌표는 렌더 viewport 기준 좌상단 원점의 `0..1` 정규화 값으로 Go에 전달한다.

Go API는 `AddOCRTextLayer(file, fontTTF, pages, opts)`로 한다. 단어·문자·페이지 예산과 유한 좌표를 검증하고 NanumGothic subset을 한 번만 embed한다. 페이지별 충돌 없는 Font resource와 `3 Tr` invisible text를 추가한다. `/Rotate`, 비영점 page box, 글자 폭과 상자 폭을 text matrix에 반영한다. 1차 범위는 영어·한국어 수평 인쇄문이다.

이미지 입력은 먼저 이미지 전용 PDF로 만든 뒤 같은 text layer를 적용한다. 원본 영상은 바뀌지 않아야 한다.

## 시각적 PDF 비교

기존 Go 텍스트 diff를 유지하고 PDF.js raster 비교를 추가한다.

- 나란히 보기, 투명도 슬라이더, A/B 깜박임, heatmap
- 변경 픽셀 비율과 bounding box
- 변경 페이지만 필터링
- 현재 페이지 쌍만 고해상도로 유지
- 채널 임계값과 anti-alias tolerance 분리
- 서로 다른 페이지 크기의 바깥 영역도 변경으로 계산

페이지 삽입·삭제는 저해상도 grayscale fingerprint, 페이지 크기, 텍스트 fingerprint를 사용한 bounded sequence alignment로 정렬한다. 한도를 넘으면 페이지 번호 정렬로 fallback하고 UI에 알린다. 브라우저별 raster 차이 때문에 exact PNG golden이 아닌 비율·영역 허용 범위를 검증한다.

## 안전한 영구 삭제

1차 구현은 원본 content operator 수정이 아니라 raster-only 재구축을 사용한다. PDF.js가 페이지를 순차 렌더링하고 선택 사각형을 바깥쪽 반올림한 뒤 안전 여백까지 불투명 검정으로 채운다. 페이지는 lossless PNG로 인코딩한다.

Go의 `BuildRasterOnlyPDF`는 원본 객체를 하나도 import하지 않고 다음 whitelist만 생성한다.

- Catalog: Type, Pages
- Pages: Type, Kids, Count
- Page: Type, Parent, MediaBox, Resources, Contents
- Resources: 새 Image XObject
- Trailer: Root, Size

Info, XMP, annotation, form/XFA, embedded file, JavaScript, outline, 이전 revision은 출력에 존재하지 않는다. 디지털 서명·링크·양식·검색 텍스트·접근성 구조가 제거되고 파일이 커질 수 있음을 실행 전에 명시한다. 출력 byte와 decoded stream에서 canary를 검사하고 object graph whitelist 검사를 통과해야만 완료로 표시한다.

## Direct Send v2

기존 수동 SDP URL 흐름을 유지하면서 versioned protocol을 추가한다.

```text
hello
manifest
ready(resumeOffsets)
chunk-header + binary payload
ack | nack
file-end
transfer-complete
```

파일·총량·frame·offset을 제한하고 각 chunk를 Web Crypto SHA-256으로 검증한다. hash 불일치는 최대 3회 재전송한다. 수신기는 연속 검증 offset을 저장하고 재연결 시 resume offset을 반환한다.

저장은 사용자 디렉터리, OPFS Worker, bounded memory 순으로 선택한다. 저장 공간 부족 시 실패하고 무제한 메모리 fallback은 금지한다. 새 수신기는 v1을 수용하고 새 송신기는 v2 협상 실패 시 단일 파일만 v1로 fallback한다.

## 안전성과 자원 예산

- Go 외부 의존성을 추가하지 않는다.
- 페이지·픽셀·문자·단어·파일 수·총 byte·chunk retry 예산을 명시한다.
- 연산 동시성 기본값은 1이다.
- Object URL, PDF.js document, bitmap, canvas, Worker, OPFS partial session의 수명을 명시적으로 정리한다.
- 암호화 문서는 해제 이후에만 OCR·영구 삭제·시각 비교를 허용한다.
- 모든 오류·진행 상태는 live region에 전달한다.

## 검증 기준

- Worker 다중 인자·await·취소·fallback·연산 격리
- operation catalog와 build/i18n 목록 일치
- 미리보기 결과와 다운로드 bytes 동일
- 파이프라인 타입·terminal·cache·실패 격리
- batch 500개 순차 처리와 메모리 예산
- OCR 한글·영문 추출 왕복, 4종 회전과 비영점 box
- 영구 삭제 canary·decoded stream·whitelist·pixel 검사
- 시각 비교 동일·1px·크기·회전·삽입 페이지
- Direct Send 다중 파일·hash 오류·3회 retry·resume·v1 호환
- Go test/race/vet, Node tests, TinyGo 35+ 신규 WASM 빌드, Chromium/Firefox/WebKit E2E

## 제외 범위

- 서버 기반 OCR·파일 저장·TURN relay
- 완전한 PDF content operator 편집 기반 redaction
- OCR 세로쓰기·RTL·필기체 정확도 보장
- 파이프라인 DAG와 조건 분기
- 브라우저가 제공하지 않는 권한을 우회한 background 전송
