# 프로젝트 리뷰 개선 설계

## 목적

`PROJECT_REVIEW.md`의 PT-01부터 PT-27까지의 실행 가능한 결함을 수정하고, 결과 PDF와 WASM 배포 크기를 불필요하게 키우지 않는다. 구현은 기존 공개 Go API와 현재 도구 URL을 유지하며, 커밋과 push는 수행하지 않는다.

## 범위

- 웹 즉시 결함: 언어 전환 URL 보존, Crop 실행, Service Worker 수명·등록 오류, 200페이지 초과 썸네일 상태, 접근성, Direct Send 입력·전송 검증, Worker 전환 기반
- PDF: CMap 예산, stream filter pipeline, Catalog·Info 보존 정책, post-mutation GC, 리소스 이름, 회전 좌표, 크기 계측
- 문서·이미지: Office 압축 예산과 오류 전파, HWPX 숫자 정렬, XLSX 좌표·출력 예산, 이미지 누산과 decode 예산
- 공급망·품질: PR CI, main 배포 guard, action/TinyGo 고정값, vendor manifest, 보안 헤더 Report-Only, 브라우저 E2E·benchmark·문서

루트 라이선스 선택, 실제 Safari 활성화, 실제 모바일 기기 검증, CSP enforce 전환은 저장소 소유자 또는 별도 환경 권한이 필요한 항목으로 남긴다. Report-Only CSP와 자동 검증은 이 작업에서 구현한다.

## 핵심 불변식

1. PDF finalization 순서는 `변형 → Catalog 정책 → Encrypt dictionary 할당 → GC/재번호화 → 최종 번호 암호화 → 직렬화`다. finalization 뒤에는 객체 번호와 참조를 변경하지 않는다.
2. 작업의 명시적 제거 정책은 일반 Catalog 보존보다 우선한다. GC는 올바른 root 정책을 대체하지 않는다.
3. 변경하지 않은 PDF stream의 `Data`, `Filter`, `DecodeParms`는 raw 상태로 보존한다.
4. 크기 최적화 전용 작업은 출력이 더 크면 원본을 반환할 수 있지만, 사용자가 요청한 의미 변환은 크기 때문에 되돌리지 않는다.
5. Web Worker 전환은 기존 동기 `pdfRun`을 한 번에 Promise로 바꾸지 않는다. 대표 도구부터 비동기 client API로 이동하고, 매 단계 35개 도구 smoke를 실행한다.
6. parser의 제한 초과와 손상 입력은 부분 성공이 아니라 typed error로 반환한다.

## 설계

### PDF rewrite/finalization

`pdf` 내부에 final graph 단계를 둔다. page mutation과 작업별 Catalog·Info 정책이 끝난 뒤 root, Info, Encrypt를 mark root로 순회해 살아 있는 객체만 재번호화한다. 모든 `Ref`를 새 번호로 갱신하고 dangling ref를 오류로 처리한다. 최종 번호가 확정된 뒤에만 암호화를 적용한다.

단일 페이지 identity 작업은 page-independent Catalog·Info를 보존한다. Split/Remove는 대상 페이지를 가리키는 outline, destination, form을 prune/remap하고, N-up/Flatten/metadata strip은 명시적 제거 목록을 우선 적용한다. 지원하지 않는 page-dependent 구조는 조용히 보존하거나 유실하지 않고 typed warning/error 경로를 사용한다.

공통 resource allocator는 Font, XObject, ExtGState 이름을 페이지 resource dictionary에서 충돌 없이 선택한다. 공통 geometry helper는 CropBox/MediaBox와 `/Rotate`를 visual 좌표계로 정규화한다.

filter pipeline은 Filter name/array와 DecodeParms를 stage로 정규화하고 ASCIIHex, ASCII85, Flate, RunLength를 순서대로 decode한다. 전체 해제 예산을 적용하며 지원하지 않는 filter는 명시적 오류로 반환한다.

### 크기 `1+1≈2`

크기 기준은 입력 전체가 아니라 정책상 살아 있는 객체다. 일반 병합·회전·재정렬은 기존 stream을 재인코딩하지 않고, mutation 뒤 GC로 고아 객체를 제거한다. 새 operator stream은 raw와 Flate 후보 중 직렬화 크기가 작은 쪽을 선택한다. 동일 overlay asset은 문서 안에서 하나의 Ref를 공유한다.

object stream/xref stream writer와 대형 PDF streaming writer는 finalization·계측 후 크기 병목이 확인될 때만 별도 후속 작업으로 수행한다.

### 웹

언어 이동은 URL 객체의 pathname만 바꾸고 query/hash를 보존한다. 자동 언어 이동이 시작되면 나머지 초기화와 Service Worker 등록을 건너뛰며, 등록 rejection은 처리한다. Service Worker cache write는 `waitUntil`로 수명에 연결한다.

썸네일은 전체 `order`와 `selected`를 상태로 두고 200개는 view limit로만 사용한다. 접근성 조작은 이 상태를 변경한다.

Direct Send는 SDP encoded/decoded 크기를 할당 전에 제한하고, 수신 state machine이 metadata·chunk·done 순서와 정확한 byte count를 검증한다. 파일 전체 해시로 추가 복사하지 않고 chunk digest protocol을 사용한다. File System Access API는 선택적 경로이며 메모리 상한은 모든 브라우저에 적용한다.

Worker는 request-id client와 단일 직렬 runtime으로 구성한다. 첫 단계는 main-thread fallback을 유지하고 output buffer만 transfer한다. input transfer는 buffer 소유권이 명확한 경우에만 적용한다.

### parser·이미지

Office parser는 archive entry, 실제 해제 바이트, XML token, 텍스트 출력의 누적 budget을 공유한다. HWPX section은 숫자 순으로 정렬하고 오류를 상위로 전파한다. XLSX는 행·열·cell 경계를 검증하고 row 단위 CSV writer로 출력한다. 이미지 resize 누산은 `uint64`를 사용하고 DecodeConfig로 pixel budget을 확인한다.

### CI·보안·문서

CI는 PR 검증과 production deploy를 분리한다. deploy는 main ref와 모든 검증 성공에 의존한다. action과 도구 다운로드는 검증 가능한 고정 SHA/checksum을 사용한다. vendor manifest는 실제 파일 hash를 자동 비교한다. 보안 헤더는 Report-Only CSP부터 적용하고, inline script·worker·WASM 경로를 E2E로 검증한 뒤 enforce 여부를 판단한다.

## 검증

- 각 변경은 failing test를 먼저 추가하고 RED 결과를 확인한다.
- PDF: graph reachability, 암호화 round-trip, Catalog 제거 canary, raw stream hash, 크기 계측
- 웹: 35개 도구 WASM smoke, Crop 다운로드, 언어 hash/query, PWA offline, 199/200/201/500 페이지 상태, 키보드 조작, Direct Send protocol, Worker cancel
- parser: 작은 injected budget fixture, malformed/partial 입력, 정상 출력 동등성
- 전체: `go test ./...`, race, vet, Node test, TinyGo build/test, `./build.sh`, Chromium/Firefox/WebKit 대표 E2E

## 단계

1. E2E harness와 즉시 P1 결함
2. parser·이미지 경계
3. PDF final graph와 Catalog/crypto 정책
4. 웹 state·Direct Send·Worker
5. CI·공급망·헤더·문서
6. 전체 검증과 크기 baseline 갱신
