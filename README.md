# Paper Tools

Privacy-first PDF & file tools that run **entirely in your browser**. Files
are never uploaded — every conversion happens locally via Go compiled to
WebAssembly.

**Live:** https://papertools.dev

> "Paper Tools" is the product name. `file-utils` is just the Go
> module / repository name used internally by imports (`file-utils/pdf`).

**[English](#english) · [한국어](#한국어)**

---

## English

### What it is

32 client-side tools for PDFs, images, and office documents. Open a tool,
drop a file, get a result — nothing leaves the browser tab. No server, no
uploads, no account.

### Tools

| Group | Tools |
|-------|-------|
| **Organize** | Merge · Interleave · Split & Extract · Remove Pages · Reorder · Insert Blank |
| **Transform** | Rotate · Crop · Resize · N-up |
| **Content** | Images → PDF · Watermark · Page Numbers · Stamp / Signature |
| **Convert** | Image Convert (PNG/JPG/GIF) · Image Resize · PDF → Text · Extract Images (ZIP) · Text → PDF · Markdown → PDF · Word → PDF · Hangul(.hwpx) → PDF · Old Hangul(.hwp) → PDF · Word ↔ Hangul · Excel → CSV |
| **Document** | Compress · Metadata · PDF Info · Protect (AES-256/AES-128) · Unlock · Compare PDFs |

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
- **Private by default.** No tracking scripts load unless you opt in
  (EthicalAds / Cloudflare Web Analytics are gated behind config flags).

### Build

```sh
./build.sh
```

Compiles one `.wasm` per tool into `web/<tool>/<tool>.wasm` and copies
`wasm_exec.js` into `web/`. Requires a Go toolchain (1.26+); nothing else.
The `.wasm` binaries are git-ignored and rebuilt by CI on every deploy.

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

---

## 한국어

### 개요

브라우저 안에서 완결되는 PDF·이미지·문서 도구 32종. 도구를 열고 파일을
올리면 결과가 나옴 — **아무것도 서버로 나가지 않음**. 서버·업로드·계정 없음.

### 도구

| 분류 | 도구 |
|------|------|
| **구성** | 병합 · 교차 병합 · 분할·추출 · 페이지 삭제 · 순서 변경 · 빈 페이지 |
| **변형** | 회전 · 자르기 · 크기 통일 · N-up |
| **콘텐츠** | 이미지 → PDF · 워터마크 · 페이지 번호 · 도장·서명 삽입 |
| **변환** | 이미지 변환(PNG/JPG/GIF) · 이미지 크기 줄이기 · PDF → 텍스트 · 이미지 추출(ZIP) · 텍스트 → PDF · 마크다운 → PDF · Word → PDF · 한글(.hwpx) → PDF · 옛한글(.hwp) → PDF · Word ↔ 한글 · 엑셀 → CSV |
| **파일** | 압축 · 메타데이터 · PDF 정보 · 암호 설정(AES-256/AES-128) · 암호 해제 · PDF 비교 |

### 특징

- **한글 출력 정상 지원.** `텍스트 → PDF`, `마크다운 → PDF`를 포함해 생성된
  PDF에서 한글이 제대로 출력됨.
- **레거시 `.hwp` 지원.**
- **다크 모드 기본 지원.** OS 설정 또는 저장된 선택을 즉시 반영해 화면
  깜빡임 없음. UI 언어도 브라우저 로케일에 맞춰 첫 방문 시 자동 선택됨.
- **UI 7개 언어**, 영어 기본: English · 한국어 · 日本語 · 中文(简体) · Español ·
  Français · Deutsch. 브랜드와 기술 용어(PDF, Word, `.docx` 등)는 원문 유지.
- **기본값이 프라이버시.** 옵션을 켜기 전엔 어떤 추적 스크립트도 불러오지 않음
  (EthicalAds / Cloudflare Web Analytics는 설정 플래그로 꺼져 있음).

### 빌드

```sh
./build.sh
```

도구마다 `.wasm` 하나를 `web/<tool>/<tool>.wasm`으로 컴파일하고
`wasm_exec.js`를 `web/`에 복사함. Go 툴체인(1.26+)만 있으면 됨. `.wasm`은
git에 포함하지 않으며 CI가 배포 시 새로 빌드함.

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
