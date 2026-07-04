# Paper Tools · 종이도구

Privacy-first PDF & file tools that run **entirely in your browser**. Files
are never uploaded — every conversion happens locally via Go compiled to
WebAssembly.

**Live:** https://paper-tools-4iq.pages.dev

> "Paper Tools" (종이도구) is the product name. `file-utils` is just the Go
> module / repository name used internally by imports (`file-utils/pdf`).

**[English](#english) · [한국어](#한국어)**

---

## English

### What it is

27 client-side tools for PDFs, images, and office documents. Open a tool,
drop a file, get a result — nothing leaves the browser tab. No server, no
uploads, no account.

The entire PDF engine is a **from-scratch, dependency-free Go package**
(`pdf/`): a hand-written PDF 1.7 reader/writer, a TrueType font subsetter, a
CFB/OLE parser for legacy `.hwp` files — no C libraries, no third-party Go
modules. Each tool ships as its own small `.wasm` binary, so visiting
`/merge/` downloads only the merge code, not all 27 tools.

### Tools

| Group | Tools |
|-------|-------|
| **Organize** | Merge · Interleave · Split & Extract · Remove Pages · Reorder · Insert Blank |
| **Transform** | Rotate · Crop · Resize · N-up |
| **Content** | Images → PDF · Watermark · Page Numbers |
| **Convert** | Image Convert (PNG/JPG/GIF) · PDF → Text · Extract Images (ZIP) · Text → PDF · Word → PDF · Hangul(.hwpx) → PDF · Old Hangul(.hwp) → PDF · Word ↔ Hangul |
| **Document** | Compress · Metadata · PDF Info · Protect (AES-128) · Unlock |

### Highlights

- **Zero third-party dependencies.** Go standard library only (`go.mod` has no
  `require`s). The PDF parser/writer, image handling, encryption, and font
  subsetting are all hand-written.
- **Korean-capable text rendering.** A NanumGothic (OFL) TrueType subset is
  embedded into generated PDFs (CIDFontType2 / Identity-H), so `Text → PDF`
  and the document converters render Hangul correctly.
- **Legacy `.hwp` support.** A minimal Compound File Binary reader + HWP record
  decoder, validated by hand against 6 real Hancom files (16 KB–2.1 MB).
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

The `pdf` package is where all PDF semantics live; the wasm and web layers
are thin wrappers.

### Limitations

- Encrypted PDFs must go through **Unlock** first; other tools reject
  encrypted input. Only AES-128 / RC4-128 (revisions 3–4) is supported —
  not AES-256.
- Document → PDF (`.docx` / `.hwpx` / `.hwp`) is a **best-effort text reflow**:
  paragraph text only, no layout, tables, images, or styling.
- `Word ↔ Hangul`'s `.hwpx` output is structurally valid but **unverified in
  real Hancom Office** (no `.hwpx` import filter was available to test with).
- Watermark text is Latin-1 only; N-up ignores per-page `/Rotate`.

---

## 한국어

### 개요

브라우저 안에서 완결되는 PDF·이미지·문서 도구 27종. 도구를 열고 파일을
올리면 결과가 나와 — **아무것도 서버로 나가지 않아**. 서버도, 업로드도,
계정도 없어.

PDF 엔진 전체가 **서드파티 없이 처음부터 짠 Go 패키지**(`pdf/`)야: 직접 만든
PDF 1.7 리더/라이터, TrueType 폰트 서브세터, 레거시 `.hwp`용 CFB/OLE 파서 —
C 라이브러리도, 외부 Go 모듈도 안 써. 각 도구는 자기 `.wasm` 하나로 배포돼서
`/merge/`에 들어가면 병합 코드만 받고 27개 전부는 안 받아.

### 도구

| 분류 | 도구 |
|------|------|
| **구성** | 병합 · 교차 병합 · 분할·추출 · 페이지 삭제 · 순서 변경 · 빈 페이지 |
| **변형** | 회전 · 자르기 · 크기 통일 · N-up |
| **콘텐츠** | 이미지 → PDF · 워터마크 · 페이지 번호 |
| **변환** | 이미지 변환(PNG/JPG/GIF) · PDF → 텍스트 · 이미지 추출(ZIP) · 텍스트 → PDF · Word → PDF · 한글(.hwpx) → PDF · 옛한글(.hwp) → PDF · Word ↔ 한글 |
| **파일** | 압축 · 메타데이터 · PDF 정보 · 암호 설정(AES-128) · 암호 해제 |

### 특징

- **서드파티 의존성 0.** Go 표준 라이브러리만 사용(`go.mod`에 `require` 없음).
  PDF 파서/라이터, 이미지 처리, 암호화, 폰트 서브셋 전부 직접 구현.
- **한글 출력.** 나눔고딕(OFL) TrueType 서브셋을 생성 PDF에 임베드
  (CIDFontType2 / Identity-H)해서 `텍스트 → PDF`와 문서 변환에서 한글이 제대로
  나와.
- **레거시 `.hwp` 지원.** 최소 CFB 리더 + HWP 레코드 디코더. 실제 한컴 파일
  6개(16 KB–2.1 MB)로 직접 검증.
- **UI 7개 언어**, 영어 기본: English · 한국어 · 日本語 · 中文(简体) · Español ·
  Français · Deutsch. 브랜드와 기술 용어(PDF, Word, `.docx` 등)는 원문 유지.
- **기본값이 프라이버시.** 옵션을 켜기 전엔 어떤 추적 스크립트도 안 불러
  (EthicalAds / Cloudflare Web Analytics는 설정 플래그로 꺼져 있음).

### 빌드

```sh
./build.sh
```

도구마다 `.wasm` 하나를 `web/<tool>/<tool>.wasm`으로 컴파일하고
`wasm_exec.js`를 `web/`에 복사해. Go 툴체인(1.26+)만 있으면 돼. `.wasm`은
git에 안 넣고 CI가 배포할 때마다 새로 빌드해.

### 로컬 실행

```sh
./build.sh
python3 -m http.server -d web 8000
```

http://localhost:8000 열기.

### 배포

`web/`는 완전 정적이야. **Cloudflare Pages**에 올라가 있고, `main`에 push할
때마다 CI가 자동 배포해(`.github/workflows/deploy.yml`): GitHub Actions가 Go를
세팅하고 `./build.sh`로 wasm을 빌드한 뒤 `wrangler pages deploy`로 올려.

CI 배포에는 저장소 시크릿 2개가 필요해 (Settings → Secrets and variables →
Actions):

- `CLOUDFLARE_API_TOKEN` — **Cloudflare Pages: Edit** 권한 토큰
  (Cloudflare 대시보드 → My Profile → API Tokens).
- `CLOUDFLARE_ACCOUNT_ID` — Cloudflare 계정 ID.

다른 정적 호스트도 되는데, `.wasm`을 `application/wasm`으로 주고 brotli/gzip만
켜면 돼.

### 테스트

```sh
go test ./pdf ./imgconv
```

PDF 로직은 전부 `pdf` 패키지에 있고, wasm·web 계층은 얇은 래퍼야.

### 한계

- 암호 걸린 PDF는 먼저 **암호 해제**를 거쳐야 해(다른 도구는 암호 입력을 거부).
  AES-128 / RC4-128(revision 3–4)만 지원 — AES-256은 미지원.
- 문서 → PDF(`.docx` / `.hwpx` / `.hwp`)는 **텍스트 재배치(best-effort)**야:
  문단 텍스트만, 레이아웃·표·이미지·서식은 유지 안 됨.
- `Word ↔ 한글`의 `.hwpx` 출력은 구조적으론 유효하지만 **실제 한컴 오피스에서
  미검증**(테스트할 `.hwpx` 임포트 필터가 없었음).
- 워터마크 텍스트는 Latin-1만; N-up은 페이지별 `/Rotate`를 무시해.
