# Paper Tools

Privacy-first PDF & file tools that run **entirely in your browser**. Files
are never uploaded — every conversion happens locally on your device.

**Live:** https://papertools.dev

> "Paper Tools" is the product name. `file-utils` is just the Go
> module / repository name used internally by imports (`file-utils/pdf`).

**[English](#english) · [한국어](#한국어)**

---

## English

### What it is

38 client-side tools for PDFs, images, and office documents. Open a tool,
drop a file, get a result — nothing leaves the browser tab. No server, no
uploads, no account.

### Tools

| Group | Tools |
|-------|-------|
| **Organize** | Merge · Interleave · Split & Extract · Remove Pages · Reorder · Insert Blank |
| **Transform** | Rotate · Crop · Resize · N-up |
| **Content** | Images → PDF · Watermark · Page Numbers · Stamp / Signature (draw or upload) / Text · Flatten PDF |
| **Convert** | Image Convert (PNG/JPG/GIF) · Image Resize · PDF → Text · OCR (scanned PDFs/images, English/Korean) · PDF → Images (page ranges, PNG/JPG quality, ZIP) · Extract Images (ZIP) · Text → PDF · Markdown → PDF · Word → PDF · Hangul(.hwpx) → PDF · Old Hangul(.hwp) → PDF · Word ↔ Hangul · PDF → Word · PDF → Hangul · Excel → CSV |
| **Document** | Compress (quality/DPI/grayscale) · Metadata · PDF Info · Protect (AES-256/AES-128) · Unlock · Compare PDFs · Direct Send (device-to-device, never uploaded) |

### Highlights

- **Korean text renders correctly** in generated PDFs, including `Text → PDF`,
  `Markdown → PDF` and the document converters.
- **Legacy `.hwp` files are supported.**
- **Dark mode, on by default.** Follows your OS or saved light/dark
  preference with no flash of the wrong theme; the UI language also
  auto-detects from your browser's locale on first visit.
- **7 UI languages**, English default: English · 한국어 · 日本語 · 中文(简体) ·
  Español · Français · Deutsch. The brand and technical tokens (PDF, Word,
  `.docx`, …) stay untranslated.
- **Offline & installable.** After a tool page has loaded once, it keeps
  working offline; the site can be installed as an app, and on desktop
  Chrome an installed Paper Tools appears in "Open with" for PDF files.
- **Visual page management.** `Reorder`, `Remove Pages` and `Split &
  Extract` show clickable page thumbnails — drag to reorder, click to
  select — while the text inputs keep working as before.
- **Batch processing.** `Compress` and `Image Convert` accept multiple
  files at once and download the results as a single ZIP.
- **Private by default.** No tracking scripts load unless you opt in
  (EthicalAds / Cloudflare Web Analytics are gated behind config flags).

### Build

```sh
./build.sh
```

Compiles one `.wasm` for each Go-backed tool into `web/<tool>/<tool>.wasm`,
copies `wasm_exec.js` into `web/`, and regenerates localized static pages.
Requires a Go toolchain (1.26+), [TinyGo](https://tinygo.org) (0.41+) and
[Binaryen](https://github.com/WebAssembly/binaryen)'s `wasm-opt`, plus Node
for the page generator. The `.wasm` binaries are git-ignored and rebuilt by
CI on every deploy.

### Performance

Each tool ships as its own WebAssembly binary, compiled with **TinyGo** and
post-optimized with Binaryen's `wasm-opt -Oz`. A per-tool binary is
~0.4–0.8 MB on disk — down from ~4 MB with the standard Go toolchain, an
~84% reduction (~24 MB total across all 35 Go-backed tools, down from
~144 MB). Over the wire a tool typically downloads ~220–285 KB thanks to
Brotli compression on the CDN. Output equivalence with the previous
toolchain was verified (byte-identical outputs for representative tools),
and the test suite also runs under TinyGo in CI.

### Run locally

```sh
./build.sh
python3 -m http.server -d web 8000
```

Open http://localhost:8000.

### Deploy

`web/` is fully static. It's hosted on **Cloudflare Pages**, and CI
auto-deploys on every push to `main` (see `.github/workflows/deploy.yml`):
GitHub Actions sets up Go, runs `./build.sh`, then `wrangler pages deploy`.

For CI to deploy, add two repository secrets (Settings → Secrets and
variables → Actions):

- `CLOUDFLARE_API_TOKEN` — a token with the **Cloudflare Pages: Edit**
  permission (Cloudflare dashboard → My Profile → API Tokens).
- `CLOUDFLARE_ACCOUNT_ID` — your Cloudflare account ID.

Any static host works too — just serve `.wasm` as `application/wasm` and
enable brotli/gzip.

### Tests

```sh
go test ./pdf ./imgconv
```

### Limitations

- Encrypted PDFs must go through **Unlock** first; other tools reject
  encrypted input. AES-256, AES-128 and RC4-128 (revisions 2–4) are all
  supported.
- Document → PDF (`.docx` / `.hwpx` / `.hwp`) is a **best-effort text reflow**:
  paragraph text only, no layout, tables, images, or styling.
- `PDF → Word` and `PDF → Hangul` are also **text-only**: the PDF's text is
  extracted and rebuilt as paragraphs, with no layout, images, or tables
  preserved.
- `Word ↔ Hangul`'s `.hwpx` output is structurally valid but **unverified in
  real Hancom Office** (no `.hwpx` import filter was available to test with).
- `Markdown → PDF` is a plain-text subset: headings, lists, blockquotes, code
  blocks and rules are laid out, but tables and images aren't supported, and
  inline bold/italic/link markers are flattened to plain text.
- `Excel → CSV` copies date cells as their raw Excel serial number (e.g.
  `45000`), not a calendar date.
- `Compare PDFs` diffs each file's extracted text only; visual layout,
  images and formatting differences aren't detected.
- `Image Resize` (and `Image Convert`) only read/write the first frame of an
  animated GIF; animation isn't preserved.
- `OCR` supports English and Korean text and works best on clean,
  high-resolution scans of printed text; handwriting recognition is
  hit-or-miss.
- `Direct Send` needs both devices to be reachable from each other directly
  (usually the same Wi-Fi or local network); it moves the file straight from
  device to device with no server relay, and has no way to help two devices
  on different networks find each other.

---

## 한국어

### 개요

브라우저 안에서 완결되는 PDF·이미지·문서 도구 38종. 도구를 열고 파일을
올리면 결과가 나옴 — **아무것도 서버로 나가지 않음**. 서버·업로드·계정 없음.

### 도구

| 분류 | 도구 |
|------|------|
| **구성** | 병합 · 교차 병합 · 분할·추출 · 페이지 삭제 · 순서 변경 · 빈 페이지 |
| **변형** | 회전 · 자르기 · 크기 통일 · N-up |
| **콘텐츠** | 이미지 → PDF · 워터마크 · 페이지 번호 · 도장·서명(직접 그리기 또는 업로드)·텍스트 삽입 · PDF 평면화 |
| **변환** | 이미지 변환(PNG/JPG/GIF) · 이미지 크기 줄이기 · PDF → 텍스트 · OCR(스캔한 PDF·이미지, 영어/한국어) · PDF → 이미지(페이지 범위, PNG/JPG 품질, ZIP) · 이미지 추출(ZIP) · 텍스트 → PDF · 마크다운 → PDF · Word → PDF · 한글(.hwpx) → PDF · 옛한글(.hwp) → PDF · Word ↔ 한글 · PDF → Word · PDF → 한글 · 엑셀 → CSV |
| **파일** | 압축(품질/DPI/흑백) · 메타데이터 · PDF 정보 · 암호 설정(AES-256/AES-128) · 암호 해제 · PDF 비교 · 직접 전송(기기 간 이동, 서버 업로드 없음) |

### 특징

- **한글 출력 정상 지원.** `텍스트 → PDF`, `마크다운 → PDF`를 포함해 생성된
  PDF에서 한글이 제대로 출력됨.
- **레거시 `.hwp` 지원.**
- **다크 모드 기본 지원.** OS 설정 또는 저장된 선택을 즉시 반영해 화면
  깜빡임 없음. UI 언어도 브라우저 로케일에 맞춰 첫 방문 시 자동 선택됨.
- **UI 7개 언어**, 영어 기본: English · 한국어 · 日本語 · 中文(简体) · Español ·
  Français · Deutsch. 브랜드와 기술 용어(PDF, Word, `.docx` 등)는 원문 유지.
- **오프라인 지원 및 설치 가능.** 도구 페이지를 한 번 불러온 뒤로는 오프라인
  에서도 계속 동작함. 사이트를 앱으로 설치할 수 있으며, 데스크톱 크롬에서는
  설치된 Paper Tools가 PDF 파일의 "연결 프로그램" 목록에 나타남.
- **페이지 시각적 관리.** `순서 변경`, `페이지 삭제`, `분할·추출`에서 클릭
  가능한 페이지 썸네일을 제공함 — 드래그로 순서 변경, 클릭으로 선택 가능하며,
  기존 텍스트 입력 방식도 그대로 사용 가능.
- **일괄 처리.** `압축`과 `이미지 변환`은 여러 파일을 한 번에 받아 결과를
  ZIP 하나로 묶어 내려받음.
- **기본값이 프라이버시.** 옵션을 켜기 전엔 어떤 추적 스크립트도 불러오지 않음
  (EthicalAds / Cloudflare Web Analytics는 설정 플래그로 꺼져 있음).

### 빌드

```sh
./build.sh
```

Go 기반 도구마다 `.wasm` 하나를 `web/<tool>/<tool>.wasm`으로 컴파일하고
`wasm_exec.js`를 `web/`에 복사한 뒤, 언어별 정적 페이지를 다시 생성함.
Go 툴체인(1.26+)과 페이지 생성용 Node가 필요함. `.wasm`은 git에 포함하지
않으며 CI가 배포 시 새로 빌드함.

### 성능

각 도구는 독립된 WebAssembly 바이너리로 제공되며, **TinyGo**로 컴파일한 뒤
Binaryen의 `wasm-opt -Oz`로 후처리 최적화함. 도구당 바이너리는 디스크 기준
약 0.4–0.8 MB — 표준 Go 툴체인의 약 4 MB에서 약 84% 감소함(Go 기반 도구
35개 합계 약 24 MB, 기존 약 144 MB). 실제 전송량은 CDN의 Brotli 압축 덕분에
도구당 보통 약 220–285 KB임. 이전 툴체인과의 출력 동등성을 검증했고
(대표 도구들에서 바이트 단위 동일 출력 확인), 테스트 스위트도 CI에서
TinyGo로 함께 실행됨.

### 로컬 실행

```sh
./build.sh
python3 -m http.server -d web 8000
```

http://localhost:8000 접속.

### 배포

`web/`는 완전한 정적 사이트임. **Cloudflare Pages**에 배포되어 있으며, `main`에
push할 때마다 CI가 자동 배포함(`.github/workflows/deploy.yml`). GitHub Actions가
Go를 설정하고 `./build.sh`로 wasm을 빌드한 뒤 `wrangler pages deploy`로 업로드함.

CI 배포에는 저장소 시크릿 2개가 필요함 (Settings → Secrets and variables →
Actions):

- `CLOUDFLARE_API_TOKEN` — **Cloudflare Pages: Edit** 권한 토큰
  (Cloudflare 대시보드 → My Profile → API Tokens).
- `CLOUDFLARE_ACCOUNT_ID` — Cloudflare 계정 ID.

다른 정적 호스트도 가능하며, `.wasm`을 `application/wasm`으로 제공하고
brotli/gzip을 켜면 됨.

### 테스트

```sh
go test ./pdf ./imgconv
```

### 한계

- 암호 걸린 PDF는 먼저 **암호 해제**를 거쳐야 함(다른 도구는 암호 입력을 거부).
  AES-256·AES-128·RC4-128(revision 2–4) 모두 지원함.
- 문서 → PDF(`.docx` / `.hwpx` / `.hwp`)는 **텍스트 재배치(best-effort)**임.
  문단 텍스트만 유지되며 레이아웃·표·이미지·서식은 유지되지 않음.
- `PDF → Word`와 `PDF → 한글`도 **텍스트 전용**임. PDF에서 텍스트를 추출해
  문단으로 재구성하며, 레이아웃·이미지·표는 유지되지 않음.
- `Word ↔ 한글`의 `.hwpx` 출력은 구조적으로는 유효하지만 **실제 한컴 오피스에서
  미검증**(테스트할 `.hwpx` 임포트 필터가 없었음).
- `마크다운 → PDF`는 텍스트 위주 변환임. 제목·목록·인용문·코드 블록은
  지원하지만 표·이미지는 지원하지 않으며, 굵게·기울임·링크 같은 인라인
  서식은 일반 텍스트로 풀어서 처리함.
- `엑셀 → CSV`는 날짜 셀을 달력 날짜가 아닌 엑셀의 원본 일련번호(예:
  `45000`) 그대로 복사함.
- `PDF 비교`는 각 파일에서 추출한 텍스트만 비교함. 시각적 레이아웃·이미지·
  서식 차이는 감지하지 않음.
- `이미지 크기 줄이기`(및 `이미지 변환`)는 애니메이션 GIF의 첫 프레임만
  읽고 씀 — 애니메이션은 유지되지 않음.
- `OCR`은 영어와 한국어 텍스트를 지원하며, 깨끗하고 해상도가 높은 인쇄물
  스캔에서 가장 잘 동작함. 손글씨 인식은 정확도가 들쭉날쭉함.
- `직접 전송`은 두 기기가 서로 직접 통신할 수 있어야 함(보통 같은 와이파이나
  로컬 네트워크). 서버를 거치지 않고 기기 간에 파일을 바로 이동시키며,
  서로 다른 네트워크에 있는 두 기기를 연결해 줄 방법은 없음.
