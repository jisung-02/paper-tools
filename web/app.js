"use strict";

/* app.js — shared boot / dropzone / run-button plumbing for every tool page.
   Loaded via a plain <script> tag after wasm_exec.js, so no imports/exports:
   everything below is a global function. Also owns the 7-language (English
   default, plus Korean/Japanese/Chinese/Spanish/French/German) i18n engine
   used by every page. Brand and format names (Paper Tools, PDF, PNG, JPG,
   JPEG, GIF, ZIP, Word, Hangul, .docx, .hwpx, .hwp, .txt, AES-128, A4, N-up)
   are never translated. */

/* ------------------------------------------------------------------ i18n --- */

const LANGS = [
  ["en", "English"],
  ["ko", "한국어"],
  ["ja", "日本語"],
  ["zh", "中文(简体)"],
  ["es", "Español"],
  ["fr", "Français"],
  ["de", "Deutsch"],
];
const LANG_CODES = LANGS.map((l) => l[0]);

// Set to your EthicalAds publisher id to enable ads. Empty string keeps the
// site fully local/private — initAds() below no-ops when this is empty.
const AD_PUBLISHER = "";

// Set to your Cloudflare Web Analytics token to enable cookieless traffic
// stats. Empty string keeps the site fully local/private — initAnalytics()
// below no-ops when this is empty.
const CF_ANALYTICS_TOKEN = ""; // set to your Cloudflare Web Analytics token to enable cookieless traffic stats

const I18N = {
  ja: {
    "180°": "180°",
    "2 per sheet (landscape A4)": "1枚に2ページ(横向きA4)",
    "27 file tools. No uploads, no installs — right here in this tab.": "27種類のファイルツール。アップロードもインストールも不要 — このタブの中だけで完結します。",
    "270° (90° counter-clockwise)": "270°(反時計回りに90°)",
    "4 per sheet (portrait A4)": "1枚に4ページ(縦向きA4)",
    "90° clockwise": "時計回りに90°",
    "A4 landscape (841.89×595.28pt)": "A4横向き(841.89×595.28pt)",
    "A4 portrait (595.28×841.89pt)": "A4縦向き(595.28×841.89pt)",
    "AES-128 · Latin letters/numbers only": "AES-128・英数字のみ",
    "Add Numbers": "番号を追加",
    "Add a blank page anywhere.": "好きな位置に白紙ページを追加します。",
    "Add an AES-128 password": "AES-128パスワードを設定",
    "Add numbers to the bottom center": "下部中央に番号を追加",
    "Add page numbers to the bottom center.": "下部中央にページ番号を追加します。",
    "After page (0 = at the very front)": "挿入位置(0=先頭)",
    "Angle": "角度",
    "Author": "作成者",
    "Back scan": "裏面スキャン",
    "Back scan is in reverse order": "裏面スキャンは逆順になっています",
    "Blank Page": "白紙ページ",
    "Bottom (mm)": "下(mm)",
    "Change the order of pages": "ページの順序を変更",
    "Change the order of pages.": "ページの順序を変更します。",
    "Combine": "結合",
    "Combine front and back scans": "表面と裏面のスキャンを結合",
    "Combine several PDFs into one": "複数のPDFを1つに結合",
    "Combine several PDFs into one, in order.": "複数のPDFを順番どおりに1つへ結合します。",
    "Compress": "圧縮",
    "Content": "コンテンツ",
    "Convert": "変換",
    "Convert .docx text into a PDF (text-focused). Layout, tables and images are not preserved.": ".docxの文章をPDFに変換します(テキスト中心)。レイアウト・表・画像は保持されません。",
    "Convert .docx text to PDF (text-focused)": ".docxの文章をPDFに変換(テキスト中心)",
    "Convert .docx to .hwpx (text-focused)": ".docxを.hwpxに変換(テキスト中心)",
    "Convert .docx to .hwpx. Only text and paragraphs are carried over.": ".docxを.hwpxに変換します。テキストと段落のみが引き継がれます。",
    "Convert .hwp text to PDF (text-focused)": ".hwpの文章をPDFに変換(テキスト中心)",
    "Convert .hwpx text into a PDF (text-focused). Layout, tables and images are not preserved.": ".hwpxの文章をPDFに変換します(テキスト中心)。レイアウト・表・画像は保持されません。",
    "Convert .hwpx text to PDF (text-focused)": ".hwpxの文章をPDFに変換(テキスト中心)",
    "Convert .hwpx to .docx (text-focused)": ".hwpxを.docxに変換(テキスト中心)",
    "Convert .hwpx to .docx. Only text and paragraphs are carried over.": ".hwpxを.docxに変換します。テキストと段落のみが引き継がれます。",
    "Convert between PNG, JPG and GIF": "PNG・JPG・GIFを相互変換",
    "Convert between PNG, JPG and GIF.": "PNG・JPG・GIFを相互変換します。",
    "Convert old .hwp text into a PDF (text-focused). Password-protected files aren't supported.": "古い形式の.hwp文章をPDFに変換します(テキスト中心)。パスワード保護されたファイルには対応していません。",
    "Create PDF": "PDFを作成",
    "Creator": "作成アプリ",
    "Crop": "切り抜き",
    "Delete pages you don't need.": "不要なページを削除します。",
    "Diagonal": "斜め",
    "Document": "ドキュメント",
    "Drag PDFs here, or click to choose": "PDFをここにドラッグ、またはクリックして選択",
    "Drag a .txt file here, or click to choose": ".txtファイルをここにドラッグ、またはクリックして選択",
    "Drag a PDF here, or click to choose": "PDFをここにドラッグ、またはクリックして選択",
    "Drag an image here, or click to choose": "画像をここにドラッグ、またはクリックして選択",
    "Drag images here, or click to choose": "画像をここにドラッグ、またはクリックして選択",
    "Drop a Hangul (.hwpx) file here, or click to choose": "Hangul(.hwpx)ファイルをここにドロップ、またはクリックして選択",
    "Drop a Word (.docx) file here, or click to choose": "Word(.docx)ファイルをここにドロップ、またはクリックして選択",
    "Drop a file here, or click to choose": "ファイルをここにドロップ、またはクリックして選択",
    "Drop an old Hangul (.hwp) file here, or click to choose": "古い形式のHangul(.hwp)ファイルをここにドロップ、またはクリックして選択",
    "Edit or remove title, author and more.": "タイトルや作成者などを編集・削除します。",
    "Edit or remove title, author, etc.": "タイトルや作成者などを編集・削除",
    "Encrypt the PDF with an AES-128 password.": "AES-128パスワードでPDFを暗号化します。",
    "Encrypted": "暗号化済み",
    "Encrypted — page info can't be read. Use the Unlock tool first.": "暗号化されています — ページ情報を読み取れません。先に[ロック解除]ツールを使ってください。",
    "Enter a page range.": "ページ範囲を入力してください。",
    "Enter a password.": "パスワードを入力してください。",
    "Enter some text.": "テキストを入力してください。",
    "Enter the new order.": "新しい順序を入力してください。",
    "Enter the pages to remove.": "削除するページを入力してください。",
    "Enter the watermark text.": "ウォーターマークのテキストを入力してください。",
    "Extract": "抽出",
    "Extract Images": "画像を抽出",
    "Extract Text": "テキストを抽出",
    "Extract selectable text to .txt": "選択可能なテキストを.txtに抽出",
    "Extract selectable text to a .txt file. (Scanned images and special fonts may not extract well)": "選択可能なテキストを.txtファイルに抽出します。(スキャン画像や特殊フォントはうまく抽出できない場合があります)",
    "Failed to load font: ": "フォントの読み込みに失敗しました: ",
    "File size": "ファイルサイズ",
    "Files are merged in the order you pick them.": "選択した順にファイルが結合されます。",
    "Fit every page to A4 (off = original image size)": "すべてのページをA4に合わせる(オフ=元の画像サイズ)",
    "Font size (pt)": "フォントサイズ(pt)",
    "Format": "形式",
    "Front scan": "表面スキャン",
    "Hangul → PDF": "Hangul → PDF",
    "Hangul → Word": "Hangul → Word",
    "Image Convert": "画像変換",
    "Image quality": "画像品質",
    "Images → PDF": "画像 → PDF",
    "Insert": "挿入",
    "Insert Blank Page": "白紙ページを挿入",
    "Insert a blank page anywhere": "好きな位置に白紙ページを挿入",
    "Interleave": "交互配置",
    "JPG quality": "JPG品質",
    "Keywords": "キーワード",
    "Latin letters, numbers and symbols only": "英数字・記号のみ",
    "Leave blank to keep existing value": "既存の値を保持する場合は空欄のまま",
    "Leave blank to match the user password": "ユーザーパスワードと同じにする場合は空欄のまま",
    "Left (mm)": "左(mm)",
    "Links and annotations will be removed": "リンクと注釈は削除されます",
    "List every page number once, in the new order": "すべてのページ番号を新しい順序で一度ずつ記入",
    "Make every page A4": "すべてのページをA4に統一",
    "Make every page the same size (A4).": "すべてのページを同じサイズ(A4)にします。",
    "Margin to trim from each side (mm)": "各辺から切り取る余白(mm)",
    "Max image width": "画像の最大幅",
    "Merge": "結合",
    "Merge PDFs": "PDFを結合",
    "Merge front and back scans into order.": "表面と裏面のスキャンを正しい順序で結合します。",
    "Metadata": "メタデータ",
    "N-up": "N-up",
    "New order (e.g. 3,1,2 — list every page number)": "新しい順序(例:3,1,2 — すべてのページ番号を記入)",
    "No": "いいえ",
    "No server · no uploads · everything runs in this tab": "サーバーなし・アップロードなし・すべてこのタブ内で処理",
    "Number of pages": "ページ数",
    "Old Hangul → PDF": "旧Hangul → PDF",
    "Opacity": "不透明度",
    "Organize": "整理",
    "Owner password": "所有者パスワード",
    "PDF Info": "PDF情報",
    "PDF producer": "PDF生成ツール",
    "PDF version": "PDFバージョン",
    "PDF → Text": "PDF → テキスト",
    "Page Numbers": "ページ番号",
    "Page count": "ページ数",
    "Page count, size, metadata": "ページ数・サイズ・メタデータ",
    "Pages (e.g. 1-3,7)": "ページ(例:1-3,7)",
    "Pages (leave blank for all)": "ページ(空欄で全ページ)",
    "Pages to remove": "削除するページ",
    "Paper Tools": "Paper Tools",
    "Password": "パスワード",
    "Paste text or drop a .txt file to make a PDF (Korean supported).": "テキストを貼り付けるか.txtファイルをドロップしてPDFを作成します(韓国語対応)。",
    "Print 2 or 4 pages per sheet": "1枚に2ページまたは4ページ印刷",
    "Print 2 or 4 pages per sheet.": "1枚に2ページまたは4ページ印刷します。",
    "Protect": "保護",
    "Pull embedded images out into a ZIP.": "埋め込まれた画像をZIPに取り出します。",
    "Pull images out into a ZIP": "画像をZIPに取り出す",
    "Pull out just the pages you need": "必要なページだけを取り出す",
    "Pull out just the pages you need.": "必要なページだけを取り出します。",
    "Remove": "削除",
    "Remove Pages": "ページを削除",
    "Remove a password you know": "知っているパスワードを削除",
    "Remove a password you know.": "知っているパスワードを削除します。",
    "Remove all metadata (ignores the fields above)": "すべてのメタデータを削除(上記の項目は無視されます)",
    "Reorder": "並べ替え",
    "Reorder Pages": "ページを並べ替え",
    "Resize": "サイズ変更",
    "Right (mm)": "右(mm)",
    "Rotate": "回転",
    "Rotate pages 90/180/270°": "ページを90/180/270°回転",
    "Save": "保存",
    "See page count, size and metadata.": "ページ数・サイズ・メタデータを確認します。",
    "Select a file.": "ファイルを選択してください。",
    "Select an image.": "画像を選択してください。",
    "Select at least 2 files.": "2つ以上のファイルを選択してください。",
    "Select at least one image.": "1つ以上の画像を選択してください。",
    "Select both the front and back files.": "表面と裏面の両方のファイルを選択してください。",
    "Set Password": "パスワードを設定",
    "Show Info": "情報を表示",
    "Shrink file size by recompressing images": "画像を再圧縮してファイルサイズを縮小",
    "Shrink file size by recompressing images.": "画像を再圧縮してファイルサイズを縮小します。",
    "Size": "サイズ",
    "Size (pt)": "サイズ(pt)",
    "Split & Extract": "分割・抽出",
    "Stamp": "スタンプ",
    "Stamp text across the page": "ページ全体にテキストをスタンプ",
    "Stamp text diagonally across the page.": "ページに斜めにテキストをスタンプします。",
    "Subject": "件名",
    "Take out the pages you don't need": "不要なページを取り除く",
    "Text": "テキスト",
    "Text content": "テキスト内容",
    "Text → PDF": "テキスト → PDF",
    "Title": "タイトル",
    "Top (mm)": "上(mm)",
    "Transform": "変形",
    "Trim the margins": "余白を切り取る",
    "Trim the margins.": "余白を切り取ります。",
    "Turn PNG/JPG images into PDF pages, one per page.": "PNG/JPG画像を1枚ずつPDFページに変換します。",
    "Turn PNG/JPG into PDF": "PNG/JPGをPDFに変換",
    "Turn pages 90/180/270°.": "ページを90/180/270°回転します。",
    "Turn text into PDF (Korean supported)": "テキストをPDFに変換(韓国語対応)",
    "Type or paste your text here…": "ここにテキストを入力または貼り付け…",
    "Unlock": "ロック解除",
    "User password": "ユーザーパスワード",
    "Watermark": "ウォーターマーク",
    "Word → Hangul": "Word → Hangul",
    "Word → PDF": "Word → PDF",
    "Yes": "はい",
    "Your files never leave the browser": "ファイルはブラウザの外に出ません",
    "e.g. 1-3,7": "例:1-3,7",
    "e.g. 1-3,7 · for an open-ended range from the end, leave the end blank like 5-": "例:1-3,7・末尾までの範囲は「5-」のように終端を空欄にします",
    "← All tools": "← すべてのツール",
    "Loading tool…": "ツールを読み込み中…",
    "Working…": "処理中…",
    "Failed to load tool: ": "ツールの読み込みに失敗しました: ",
    "FAQ": "よくある質問",
    "Is Paper Tools free?": "Paper Toolsは無料ですか?",
    "Yes. All 27 tools are free, with no account and no signup.": "はい。27種類のツールはすべて無料で、アカウント登録も不要です。",
    "Do my files get uploaded to a server?": "ファイルはサーバーにアップロードされますか?",
    "No. Every tool runs inside your browser via WebAssembly; your files never leave your device.": "いいえ。すべてのツールはWebAssemblyを使ってブラウザ内で動作するため、ファイルがお使いの端末から外に出ることはありません。",
    "Do I need to install anything?": "何かインストールする必要はありますか?",
    "No. It works in any modern browser, on desktop or mobile.": "いいえ。最新のブラウザであれば、パソコンでもスマートフォンでも動作します。",
    "What can it do?": "何ができますか?",
    "Merge, split, rotate, crop, compress, protect and unlock PDFs; convert images (PNG/JPG/GIF); convert Word/Hangul documents to PDF; extract text and images; and more.": "PDFの結合、分割、回転、切り抜き、圧縮、パスワード保護・解除、画像(PNG/JPG/GIF)の変換、Word/HangulファイルのPDF変換、テキストや画像の抽出など、さまざまな処理に対応しています。",
    "Is it open source?": "オープンソースですか?",
    "Yes. It's built with the Go standard library (no third-party dependencies) and compiled to WebAssembly.": "はい。Goの標準ライブラリのみで作られており(サードパーティ製の依存関係はありません)、WebAssemblyにコンパイルされています。",
    "Privacy": "プライバシー",
    "Privacy Policy": "プライバシーポリシー",
    "Your files are processed entirely in your browser. They are never uploaded to any server.": "ファイルはすべてブラウザ内で処理されます。サーバーにアップロードされることは一切ありません。",
    "We don't use accounts, logins, or our own tracking cookies.": "アカウントやログイン、独自のトラッキングクッキーは使用していません。",
    "The site is served as static files by Cloudflare Pages, which may log standard request metadata (like IP and user agent) for security and performance.": "このサイトはCloudflare Pagesによって静的ファイルとして配信されており、セキュリティとパフォーマンスのためにIPアドレスやユーザーエージェントなどの標準的なリクエスト情報が記録される場合があります。",
    "If ads are shown, they are provided by EthicalAds, a privacy-focused network that serves contextual ads without tracking cookies or collecting personal data.": "広告が表示される場合、それはプライバシーに配慮した広告ネットワークEthicalAdsによるもので、トラッキングクッキーの使用や個人データの収集を行わずに文脈に応じた広告を配信します。",
  },
  zh: {
    "180°": "180°",
    "2 per sheet (landscape A4)": "每页2张(横向A4)",
    "27 file tools. No uploads, no installs — right here in this tab.": "27 款文件工具。无需上传，无需安装——一切都在此标签页内完成。",
    "270° (90° counter-clockwise)": "270°(逆时针90°)",
    "4 per sheet (portrait A4)": "每页4张(纵向A4)",
    "90° clockwise": "顺时针90°",
    "A4 landscape (841.89×595.28pt)": "A4横向(841.89×595.28pt)",
    "A4 portrait (595.28×841.89pt)": "A4纵向(595.28×841.89pt)",
    "AES-128 · Latin letters/numbers only": "AES-128 · 仅限拉丁字母/数字",
    "Add Numbers": "添加页码",
    "Add a blank page anywhere.": "在任意位置插入空白页。",
    "Add an AES-128 password": "添加 AES-128 密码",
    "Add numbers to the bottom center": "在底部居中添加页码",
    "Add page numbers to the bottom center.": "在底部居中添加页码。",
    "After page (0 = at the very front)": "插入到第几页之后(0 = 最前面)",
    "Angle": "角度",
    "Author": "作者",
    "Back scan": "背面扫描",
    "Back scan is in reverse order": "背面扫描顺序相反",
    "Blank Page": "空白页",
    "Bottom (mm)": "下边距(mm)",
    "Change the order of pages": "更改页面顺序",
    "Change the order of pages.": "更改页面的顺序。",
    "Combine": "合并",
    "Combine front and back scans": "合并正反面扫描",
    "Combine several PDFs into one": "将多个PDF合并为一个",
    "Combine several PDFs into one, in order.": "按顺序将多个PDF合并为一个。",
    "Compress": "压缩",
    "Content": "内容",
    "Convert": "转换",
    "Convert .docx text into a PDF (text-focused). Layout, tables and images are not preserved.": "将 .docx 文字转换为 PDF(以文本为主)。不保留版式、表格和图片。",
    "Convert .docx text to PDF (text-focused)": "将 .docx 文字转换为 PDF(以文本为主)",
    "Convert .docx to .hwpx (text-focused)": "将 .docx 转换为 .hwpx(以文本为主)",
    "Convert .docx to .hwpx. Only text and paragraphs are carried over.": "将 .docx 转换为 .hwpx。仅保留文字和段落。",
    "Convert .hwp text to PDF (text-focused)": "将 .hwp 文字转换为 PDF(以文本为主)",
    "Convert .hwpx text into a PDF (text-focused). Layout, tables and images are not preserved.": "将 .hwpx 文字转换为 PDF(以文本为主)。不保留版式、表格和图片。",
    "Convert .hwpx text to PDF (text-focused)": "将 .hwpx 文字转换为 PDF(以文本为主)",
    "Convert .hwpx to .docx (text-focused)": "将 .hwpx 转换为 .docx(以文本为主)",
    "Convert .hwpx to .docx. Only text and paragraphs are carried over.": "将 .hwpx 转换为 .docx。仅保留文字和段落。",
    "Convert between PNG, JPG and GIF": "在 PNG、JPG 和 GIF 之间转换",
    "Convert between PNG, JPG and GIF.": "在 PNG、JPG 和 GIF 之间转换。",
    "Convert old .hwp text into a PDF (text-focused). Password-protected files aren't supported.": "将旧版 .hwp 文字转换为 PDF(以文本为主)。不支持受密码保护的文件。",
    "Create PDF": "创建 PDF",
    "Creator": "创建者",
    "Crop": "裁剪",
    "Delete pages you don't need.": "删除不需要的页面。",
    "Diagonal": "对角线",
    "Document": "文档",
    "Drag PDFs here, or click to choose": "将 PDF 拖到此处，或点击选择",
    "Drag a .txt file here, or click to choose": "将 .txt 文件拖到此处，或点击选择",
    "Drag a PDF here, or click to choose": "将 PDF 拖到此处，或点击选择",
    "Drag an image here, or click to choose": "将图片拖到此处，或点击选择",
    "Drag images here, or click to choose": "将图片拖到此处，或点击选择",
    "Drop a Hangul (.hwpx) file here, or click to choose": "将 Hangul(.hwpx)文件放到此处，或点击选择",
    "Drop a Word (.docx) file here, or click to choose": "将 Word(.docx)文件放到此处，或点击选择",
    "Drop a file here, or click to choose": "将文件放到此处，或点击选择",
    "Drop an old Hangul (.hwp) file here, or click to choose": "将旧版 Hangul(.hwp)文件放到此处，或点击选择",
    "Edit or remove title, author and more.": "编辑或删除标题、作者等信息。",
    "Edit or remove title, author, etc.": "编辑或删除标题、作者等",
    "Encrypt the PDF with an AES-128 password.": "使用 AES-128 密码加密 PDF。",
    "Encrypted": "已加密",
    "Encrypted — page info can't be read. Use the Unlock tool first.": "已加密——无法读取页面信息。请先使用[解锁]工具。",
    "Enter a page range.": "请输入页码范围。",
    "Enter a password.": "请输入密码。",
    "Enter some text.": "请输入文字。",
    "Enter the new order.": "请输入新的顺序。",
    "Enter the pages to remove.": "请输入要删除的页面。",
    "Enter the watermark text.": "请输入水印文字。",
    "Extract": "提取",
    "Extract Images": "提取图片",
    "Extract Text": "提取文字",
    "Extract selectable text to .txt": "将可选文字提取为 .txt",
    "Extract selectable text to a .txt file. (Scanned images and special fonts may not extract well)": "将可选文字提取到 .txt 文件。(扫描图像和特殊字体可能无法很好地提取)",
    "Failed to load font: ": "字体加载失败: ",
    "File size": "文件大小",
    "Files are merged in the order you pick them.": "文件将按您选择的顺序合并。",
    "Fit every page to A4 (off = original image size)": "将每页调整为 A4(关闭 = 原始图片尺寸)",
    "Font size (pt)": "字体大小(pt)",
    "Format": "格式",
    "Front scan": "正面扫描",
    "Hangul → PDF": "Hangul → PDF",
    "Hangul → Word": "Hangul → Word",
    "Image Convert": "图片转换",
    "Image quality": "图片质量",
    "Images → PDF": "图片 → PDF",
    "Insert": "插入",
    "Insert Blank Page": "插入空白页",
    "Insert a blank page anywhere": "在任意位置插入空白页",
    "Interleave": "交叉合并",
    "JPG quality": "JPG 质量",
    "Keywords": "关键词",
    "Latin letters, numbers and symbols only": "仅限拉丁字母、数字和符号",
    "Leave blank to keep existing value": "留空以保留现有值",
    "Leave blank to match the user password": "留空以与用户密码相同",
    "Left (mm)": "左边距(mm)",
    "Links and annotations will be removed": "链接和注释将被移除",
    "List every page number once, in the new order": "按新顺序列出每个页码一次",
    "Make every page A4": "将每页统一为 A4",
    "Make every page the same size (A4).": "将所有页面统一为相同大小(A4)。",
    "Margin to trim from each side (mm)": "每边裁去的边距(mm)",
    "Max image width": "最大图片宽度",
    "Merge": "合并",
    "Merge PDFs": "合并 PDF",
    "Merge front and back scans into order.": "按顺序合并正反面扫描。",
    "Metadata": "元数据",
    "N-up": "N-up",
    "New order (e.g. 3,1,2 — list every page number)": "新顺序(例如 3,1,2 — 列出每个页码)",
    "No": "否",
    "No server · no uploads · everything runs in this tab": "无服务器 · 无上传 · 一切都在此标签页内运行",
    "Number of pages": "页数",
    "Old Hangul → PDF": "旧版 Hangul → PDF",
    "Opacity": "不透明度",
    "Organize": "整理",
    "Owner password": "所有者密码",
    "PDF Info": "PDF 信息",
    "PDF producer": "PDF 生成程序",
    "PDF version": "PDF 版本",
    "PDF → Text": "PDF → 文本",
    "Page Numbers": "页码",
    "Page count": "页数",
    "Page count, size, metadata": "页数、大小、元数据",
    "Pages (e.g. 1-3,7)": "页面(例如 1-3,7)",
    "Pages (leave blank for all)": "页面(留空表示全部)",
    "Pages to remove": "要删除的页面",
    "Paper Tools": "Paper Tools",
    "Password": "密码",
    "Paste text or drop a .txt file to make a PDF (Korean supported).": "粘贴文字或拖放 .txt 文件以生成 PDF(支持韩文)。",
    "Print 2 or 4 pages per sheet": "每页打印2或4张内容",
    "Print 2 or 4 pages per sheet.": "每页打印2或4张内容。",
    "Protect": "保护",
    "Pull embedded images out into a ZIP.": "将内嵌图片提取到 ZIP 中。",
    "Pull images out into a ZIP": "将图片提取到 ZIP",
    "Pull out just the pages you need": "只提取您需要的页面",
    "Pull out just the pages you need.": "只提取您需要的页面。",
    "Remove": "移除",
    "Remove Pages": "删除页面",
    "Remove a password you know": "移除您知道的密码",
    "Remove a password you know.": "移除您知道的密码。",
    "Remove all metadata (ignores the fields above)": "删除所有元数据(忽略上方字段)",
    "Reorder": "重新排序",
    "Reorder Pages": "重新排序页面",
    "Resize": "调整大小",
    "Right (mm)": "右边距(mm)",
    "Rotate": "旋转",
    "Rotate pages 90/180/270°": "将页面旋转90/180/270°",
    "Save": "保存",
    "See page count, size and metadata.": "查看页数、大小和元数据。",
    "Select a file.": "请选择一个文件。",
    "Select an image.": "请选择一张图片。",
    "Select at least 2 files.": "请至少选择2个文件。",
    "Select at least one image.": "请至少选择一张图片。",
    "Select both the front and back files.": "请同时选择正面和背面文件。",
    "Set Password": "设置密码",
    "Show Info": "显示信息",
    "Shrink file size by recompressing images": "通过重新压缩图片缩小文件体积",
    "Shrink file size by recompressing images.": "通过重新压缩图片来缩小文件体积。",
    "Size": "大小",
    "Size (pt)": "大小(pt)",
    "Split & Extract": "拆分与提取",
    "Stamp": "盖章",
    "Stamp text across the page": "在页面上盖印文字",
    "Stamp text diagonally across the page.": "在页面上斜向盖印文字。",
    "Subject": "主题",
    "Take out the pages you don't need": "移除不需要的页面",
    "Text": "文本",
    "Text content": "文本内容",
    "Text → PDF": "文本 → PDF",
    "Title": "标题",
    "Top (mm)": "上边距(mm)",
    "Transform": "变换",
    "Trim the margins": "裁去边距",
    "Trim the margins.": "裁去边距。",
    "Turn PNG/JPG images into PDF pages, one per page.": "将 PNG/JPG 图片逐张转换为 PDF 页面。",
    "Turn PNG/JPG into PDF": "将 PNG/JPG 转换为 PDF",
    "Turn pages 90/180/270°.": "将页面旋转90/180/270°。",
    "Turn text into PDF (Korean supported)": "将文字转换为 PDF(支持韩文)",
    "Type or paste your text here…": "在此输入或粘贴文字…",
    "Unlock": "解锁",
    "User password": "用户密码",
    "Watermark": "水印",
    "Word → Hangul": "Word → Hangul",
    "Word → PDF": "Word → PDF",
    "Yes": "是",
    "Your files never leave the browser": "您的文件永远不会离开浏览器",
    "e.g. 1-3,7": "例如 1-3,7",
    "e.g. 1-3,7 · for an open-ended range from the end, leave the end blank like 5-": "例如 1-3,7 · 若要表示到末尾的开放范围，末尾留空，如 5-",
    "← All tools": "← 所有工具",
    "Loading tool…": "正在加载工具…",
    "Working…": "处理中…",
    "Failed to load tool: ": "工具加载失败: ",
    "FAQ": "常见问题",
    "Is Paper Tools free?": "Paper Tools 免费吗?",
    "Yes. All 27 tools are free, with no account and no signup.": "是的。全部 27 款工具均可免费使用，无需账号，也无需注册。",
    "Do my files get uploaded to a server?": "我的文件会上传到服务器吗?",
    "No. Every tool runs inside your browser via WebAssembly; your files never leave your device.": "不会。所有工具都通过 WebAssembly 在您的浏览器中运行，您的文件永远不会离开您的设备。",
    "Do I need to install anything?": "需要安装什么吗?",
    "No. It works in any modern browser, on desktop or mobile.": "不需要。它可以在任何现代浏览器中运行，无论是电脑还是手机。",
    "What can it do?": "它能做什么?",
    "Merge, split, rotate, crop, compress, protect and unlock PDFs; convert images (PNG/JPG/GIF); convert Word/Hangul documents to PDF; extract text and images; and more.": "合并、拆分、旋转、裁剪、压缩、加密和解锁 PDF；转换图片(PNG/JPG/GIF)；将 Word/Hangul 文档转换为 PDF；提取文字和图片等等。",
    "Is it open source?": "它是开源的吗?",
    "Yes. It's built with the Go standard library (no third-party dependencies) and compiled to WebAssembly.": "是的。它完全基于 Go 标准库构建(不依赖任何第三方库)，并编译为 WebAssembly。",
    "Privacy": "隐私",
    "Privacy Policy": "隐私政策",
    "Your files are processed entirely in your browser. They are never uploaded to any server.": "您的文件完全在浏览器中处理，绝不会上传到任何服务器。",
    "We don't use accounts, logins, or our own tracking cookies.": "我们不使用账号、登录，也不使用自己的跟踪 Cookie。",
    "The site is served as static files by Cloudflare Pages, which may log standard request metadata (like IP and user agent) for security and performance.": "本站由 Cloudflare Pages 以静态文件形式提供服务，出于安全和性能考虑，Cloudflare Pages 可能会记录标准的请求元数据(如 IP 地址和用户代理)。",
    "If ads are shown, they are provided by EthicalAds, a privacy-focused network that serves contextual ads without tracking cookies or collecting personal data.": "如果显示广告，广告由注重隐私的广告网络 EthicalAds 提供，该网络在不使用跟踪 Cookie、不收集个人数据的情况下展示与内容相关的广告。",
  },
  es: {
    "180°": "180°",
    "2 per sheet (landscape A4)": "2 por hoja (A4 horizontal)",
    "27 file tools. No uploads, no installs — right here in this tab.": "27 herramientas de archivos. Sin subidas, sin instalaciones: todo aquí mismo, en esta pestaña.",
    "270° (90° counter-clockwise)": "270° (90° en sentido antihorario)",
    "4 per sheet (portrait A4)": "4 por hoja (A4 vertical)",
    "90° clockwise": "90° en sentido horario",
    "A4 landscape (841.89×595.28pt)": "A4 horizontal (841.89×595.28pt)",
    "A4 portrait (595.28×841.89pt)": "A4 vertical (595.28×841.89pt)",
    "AES-128 · Latin letters/numbers only": "AES-128 · solo letras y números latinos",
    "Add Numbers": "Añadir números",
    "Add a blank page anywhere.": "Añade una página en blanco donde quieras.",
    "Add an AES-128 password": "Añadir una contraseña AES-128",
    "Add numbers to the bottom center": "Añadir números en la parte inferior central",
    "Add page numbers to the bottom center.": "Añade números de página en la parte inferior central.",
    "After page (0 = at the very front)": "Después de la página (0 = al principio)",
    "Angle": "Ángulo",
    "Author": "Autor",
    "Back scan": "Escaneo trasero",
    "Back scan is in reverse order": "El escaneo trasero está en orden inverso",
    "Blank Page": "Página en blanco",
    "Bottom (mm)": "Inferior (mm)",
    "Change the order of pages": "Cambiar el orden de las páginas",
    "Change the order of pages.": "Cambia el orden de las páginas.",
    "Combine": "Combinar",
    "Combine front and back scans": "Combinar escaneos frontal y trasero",
    "Combine several PDFs into one": "Combinar varios PDF en uno",
    "Combine several PDFs into one, in order.": "Combina varios PDF en uno solo, en orden.",
    "Compress": "Comprimir",
    "Content": "Contenido",
    "Convert": "Convertir",
    "Convert .docx text into a PDF (text-focused). Layout, tables and images are not preserved.": "Convierte el texto de un .docx en PDF (centrado en texto). No se conservan el diseño, las tablas ni las imágenes.",
    "Convert .docx text to PDF (text-focused)": "Convertir texto de .docx a PDF (centrado en texto)",
    "Convert .docx to .hwpx (text-focused)": "Convertir .docx a .hwpx (centrado en texto)",
    "Convert .docx to .hwpx. Only text and paragraphs are carried over.": "Convierte .docx a .hwpx. Solo se conservan el texto y los párrafos.",
    "Convert .hwp text to PDF (text-focused)": "Convertir texto de .hwp a PDF (centrado en texto)",
    "Convert .hwpx text into a PDF (text-focused). Layout, tables and images are not preserved.": "Convierte el texto de un .hwpx en PDF (centrado en texto). No se conservan el diseño, las tablas ni las imágenes.",
    "Convert .hwpx text to PDF (text-focused)": "Convertir texto de .hwpx a PDF (centrado en texto)",
    "Convert .hwpx to .docx (text-focused)": "Convertir .hwpx a .docx (centrado en texto)",
    "Convert .hwpx to .docx. Only text and paragraphs are carried over.": "Convierte .hwpx a .docx. Solo se conservan el texto y los párrafos.",
    "Convert between PNG, JPG and GIF": "Convertir entre PNG, JPG y GIF",
    "Convert between PNG, JPG and GIF.": "Convierte entre PNG, JPG y GIF.",
    "Convert old .hwp text into a PDF (text-focused). Password-protected files aren't supported.": "Convierte texto de un .hwp antiguo en PDF (centrado en texto). No se admiten archivos protegidos con contraseña.",
    "Create PDF": "Crear PDF",
    "Creator": "Creador",
    "Crop": "Recortar",
    "Delete pages you don't need.": "Elimina las páginas que no necesitas.",
    "Diagonal": "Diagonal",
    "Document": "Documento",
    "Drag PDFs here, or click to choose": "Arrastra los PDF aquí, o haz clic para elegir",
    "Drag a .txt file here, or click to choose": "Arrastra un archivo .txt aquí, o haz clic para elegir",
    "Drag a PDF here, or click to choose": "Arrastra un PDF aquí, o haz clic para elegir",
    "Drag an image here, or click to choose": "Arrastra una imagen aquí, o haz clic para elegir",
    "Drag images here, or click to choose": "Arrastra las imágenes aquí, o haz clic para elegir",
    "Drop a Hangul (.hwpx) file here, or click to choose": "Suelta un archivo Hangul (.hwpx) aquí, o haz clic para elegir",
    "Drop a Word (.docx) file here, or click to choose": "Suelta un archivo Word (.docx) aquí, o haz clic para elegir",
    "Drop a file here, or click to choose": "Suelta un archivo aquí, o haz clic para elegir",
    "Drop an old Hangul (.hwp) file here, or click to choose": "Suelta un archivo Hangul antiguo (.hwp) aquí, o haz clic para elegir",
    "Edit or remove title, author and more.": "Edita o elimina el título, el autor y más.",
    "Edit or remove title, author, etc.": "Editar o eliminar el título, el autor, etc.",
    "Encrypt the PDF with an AES-128 password.": "Cifra el PDF con una contraseña AES-128.",
    "Encrypted": "Cifrado",
    "Encrypted — page info can't be read. Use the Unlock tool first.": "Cifrado: no se puede leer la información de las páginas. Usa primero la herramienta Desbloquear.",
    "Enter a page range.": "Introduce un rango de páginas.",
    "Enter a password.": "Introduce una contraseña.",
    "Enter some text.": "Introduce algo de texto.",
    "Enter the new order.": "Introduce el nuevo orden.",
    "Enter the pages to remove.": "Introduce las páginas que quieres eliminar.",
    "Enter the watermark text.": "Introduce el texto de la marca de agua.",
    "Extract": "Extraer",
    "Extract Images": "Extraer imágenes",
    "Extract Text": "Extraer texto",
    "Extract selectable text to .txt": "Extraer texto seleccionable a .txt",
    "Extract selectable text to a .txt file. (Scanned images and special fonts may not extract well)": "Extrae el texto seleccionable a un archivo .txt. (Las imágenes escaneadas y las fuentes especiales pueden no extraerse bien)",
    "Failed to load font: ": "Error al cargar la fuente: ",
    "File size": "Tamaño del archivo",
    "Files are merged in the order you pick them.": "Los archivos se combinan en el orden en que los eliges.",
    "Fit every page to A4 (off = original image size)": "Ajustar cada página a A4 (desactivado = tamaño original de la imagen)",
    "Font size (pt)": "Tamaño de fuente (pt)",
    "Format": "Formato",
    "Front scan": "Escaneo frontal",
    "Hangul → PDF": "Hangul → PDF",
    "Hangul → Word": "Hangul → Word",
    "Image Convert": "Convertir imagen",
    "Image quality": "Calidad de imagen",
    "Images → PDF": "Imágenes → PDF",
    "Insert": "Insertar",
    "Insert Blank Page": "Insertar página en blanco",
    "Insert a blank page anywhere": "Insertar una página en blanco en cualquier lugar",
    "Interleave": "Intercalar",
    "JPG quality": "Calidad JPG",
    "Keywords": "Palabras clave",
    "Latin letters, numbers and symbols only": "Solo letras, números y símbolos latinos",
    "Leave blank to keep existing value": "Déjalo en blanco para conservar el valor actual",
    "Leave blank to match the user password": "Déjalo en blanco para que coincida con la contraseña de usuario",
    "Left (mm)": "Izquierda (mm)",
    "Links and annotations will be removed": "Se eliminarán los enlaces y las anotaciones",
    "List every page number once, in the new order": "Enumera cada número de página una vez, en el nuevo orden",
    "Make every page A4": "Convertir todas las páginas a A4",
    "Make every page the same size (A4).": "Hace que todas las páginas tengan el mismo tamaño (A4).",
    "Margin to trim from each side (mm)": "Margen que recortar de cada lado (mm)",
    "Max image width": "Ancho máximo de imagen",
    "Merge": "Combinar",
    "Merge PDFs": "Combinar PDF",
    "Merge front and back scans into order.": "Combina los escaneos frontal y trasero en el orden correcto.",
    "Metadata": "Metadatos",
    "N-up": "N-up",
    "New order (e.g. 3,1,2 — list every page number)": "Nuevo orden (p. ej., 3,1,2; indica cada número de página)",
    "No": "No",
    "No server · no uploads · everything runs in this tab": "Sin servidor · sin subidas · todo se ejecuta en esta pestaña",
    "Number of pages": "Número de páginas",
    "Old Hangul → PDF": "Hangul antiguo → PDF",
    "Opacity": "Opacidad",
    "Organize": "Organizar",
    "Owner password": "Contraseña de propietario",
    "PDF Info": "Información del PDF",
    "PDF producer": "Productor del PDF",
    "PDF version": "Versión del PDF",
    "PDF → Text": "PDF → Texto",
    "Page Numbers": "Números de página",
    "Page count": "Número de páginas",
    "Page count, size, metadata": "Número de páginas, tamaño, metadatos",
    "Pages (e.g. 1-3,7)": "Páginas (p. ej., 1-3,7)",
    "Pages (leave blank for all)": "Páginas (déjalo en blanco para todas)",
    "Pages to remove": "Páginas a eliminar",
    "Paper Tools": "Paper Tools",
    "Password": "Contraseña",
    "Paste text or drop a .txt file to make a PDF (Korean supported).": "Pega texto o suelta un archivo .txt para crear un PDF (compatible con coreano).",
    "Print 2 or 4 pages per sheet": "Imprimir 2 o 4 páginas por hoja",
    "Print 2 or 4 pages per sheet.": "Imprime 2 o 4 páginas por hoja.",
    "Protect": "Proteger",
    "Pull embedded images out into a ZIP.": "Extrae las imágenes incrustadas a un ZIP.",
    "Pull images out into a ZIP": "Extraer imágenes a un ZIP",
    "Pull out just the pages you need": "Extrae solo las páginas que necesitas",
    "Pull out just the pages you need.": "Extrae solo las páginas que necesitas.",
    "Remove": "Eliminar",
    "Remove Pages": "Eliminar páginas",
    "Remove a password you know": "Eliminar una contraseña que conoces",
    "Remove a password you know.": "Elimina una contraseña que conoces.",
    "Remove all metadata (ignores the fields above)": "Eliminar todos los metadatos (ignora los campos anteriores)",
    "Reorder": "Reordenar",
    "Reorder Pages": "Reordenar páginas",
    "Resize": "Cambiar tamaño",
    "Right (mm)": "Derecha (mm)",
    "Rotate": "Girar",
    "Rotate pages 90/180/270°": "Girar páginas 90/180/270°",
    "Save": "Guardar",
    "See page count, size and metadata.": "Consulta el número de páginas, el tamaño y los metadatos.",
    "Select a file.": "Selecciona un archivo.",
    "Select an image.": "Selecciona una imagen.",
    "Select at least 2 files.": "Selecciona al menos 2 archivos.",
    "Select at least one image.": "Selecciona al menos una imagen.",
    "Select both the front and back files.": "Selecciona los archivos frontal y trasero.",
    "Set Password": "Establecer contraseña",
    "Show Info": "Mostrar información",
    "Shrink file size by recompressing images": "Reducir el tamaño del archivo recomprimiendo imágenes",
    "Shrink file size by recompressing images.": "Reduce el tamaño del archivo recomprimiendo imágenes.",
    "Size": "Tamaño",
    "Size (pt)": "Tamaño (pt)",
    "Split & Extract": "Dividir y extraer",
    "Stamp": "Sello",
    "Stamp text across the page": "Estampar texto en toda la página",
    "Stamp text diagonally across the page.": "Estampa texto en diagonal sobre la página.",
    "Subject": "Asunto",
    "Take out the pages you don't need": "Quita las páginas que no necesitas",
    "Text": "Texto",
    "Text content": "Contenido del texto",
    "Text → PDF": "Texto → PDF",
    "Title": "Título",
    "Top (mm)": "Superior (mm)",
    "Transform": "Transformar",
    "Trim the margins": "Recortar los márgenes",
    "Trim the margins.": "Recorta los márgenes.",
    "Turn PNG/JPG images into PDF pages, one per page.": "Convierte imágenes PNG/JPG en páginas de PDF, una por página.",
    "Turn PNG/JPG into PDF": "Convertir PNG/JPG en PDF",
    "Turn pages 90/180/270°.": "Gira las páginas 90/180/270°.",
    "Turn text into PDF (Korean supported)": "Convertir texto en PDF (compatible con coreano)",
    "Type or paste your text here…": "Escribe o pega tu texto aquí…",
    "Unlock": "Desbloquear",
    "User password": "Contraseña de usuario",
    "Watermark": "Marca de agua",
    "Word → Hangul": "Word → Hangul",
    "Word → PDF": "Word → PDF",
    "Yes": "Sí",
    "Your files never leave the browser": "Tus archivos nunca salen del navegador",
    "e.g. 1-3,7": "p. ej., 1-3,7",
    "e.g. 1-3,7 · for an open-ended range from the end, leave the end blank like 5-": "p. ej., 1-3,7 · para un rango abierto hasta el final, deja el final en blanco como 5-",
    "← All tools": "← Todas las herramientas",
    "Loading tool…": "Cargando herramienta…",
    "Working…": "Procesando…",
    "Failed to load tool: ": "Error al cargar la herramienta: ",
    "FAQ": "Preguntas frecuentes",
    "Is Paper Tools free?": "¿Paper Tools es gratis?",
    "Yes. All 27 tools are free, with no account and no signup.": "Sí. Las 27 herramientas son gratuitas, sin necesidad de cuenta ni registro.",
    "Do my files get uploaded to a server?": "¿Mis archivos se suben a un servidor?",
    "No. Every tool runs inside your browser via WebAssembly; your files never leave your device.": "No. Cada herramienta se ejecuta dentro de tu navegador mediante WebAssembly; tus archivos nunca salen de tu dispositivo.",
    "Do I need to install anything?": "¿Necesito instalar algo?",
    "No. It works in any modern browser, on desktop or mobile.": "No. Funciona en cualquier navegador moderno, tanto en ordenador como en móvil.",
    "What can it do?": "¿Qué puede hacer?",
    "Merge, split, rotate, crop, compress, protect and unlock PDFs; convert images (PNG/JPG/GIF); convert Word/Hangul documents to PDF; extract text and images; and more.": "Combina, divide, gira, recorta, comprime, protege y desbloquea PDFs; convierte imágenes (PNG/JPG/GIF); convierte documentos Word/Hangul a PDF; extrae texto e imágenes; y mucho más.",
    "Is it open source?": "¿Es de código abierto?",
    "Yes. It's built with the Go standard library (no third-party dependencies) and compiled to WebAssembly.": "Sí. Está construido con la biblioteca estándar de Go (sin dependencias de terceros) y compilado a WebAssembly.",
    "Privacy": "Privacidad",
    "Privacy Policy": "Política de privacidad",
    "Your files are processed entirely in your browser. They are never uploaded to any server.": "Tus archivos se procesan por completo en tu navegador. Nunca se suben a ningún servidor.",
    "We don't use accounts, logins, or our own tracking cookies.": "No usamos cuentas, inicios de sesión ni cookies de seguimiento propias.",
    "The site is served as static files by Cloudflare Pages, which may log standard request metadata (like IP and user agent) for security and performance.": "El sitio se sirve como archivos estáticos a través de Cloudflare Pages, que puede registrar metadatos estándar de las solicitudes (como la IP y el user agent) por motivos de seguridad y rendimiento.",
    "If ads are shown, they are provided by EthicalAds, a privacy-focused network that serves contextual ads without tracking cookies or collecting personal data.": "Si se muestran anuncios, estos son proporcionados por EthicalAds, una red centrada en la privacidad que sirve anuncios contextuales sin cookies de seguimiento ni recopilación de datos personales.",
  },
  fr: {
    "180°": "180°",
    "2 per sheet (landscape A4)": "2 par feuille (A4 paysage)",
    "27 file tools. No uploads, no installs — right here in this tab.": "27 outils de fichiers. Aucun envoi, aucune installation — tout se passe directement dans cet onglet.",
    "270° (90° counter-clockwise)": "270° (90° dans le sens antihoraire)",
    "4 per sheet (portrait A4)": "4 par feuille (A4 portrait)",
    "90° clockwise": "90° dans le sens horaire",
    "A4 landscape (841.89×595.28pt)": "A4 paysage (841.89×595.28pt)",
    "A4 portrait (595.28×841.89pt)": "A4 portrait (595.28×841.89pt)",
    "AES-128 · Latin letters/numbers only": "AES-128 · lettres/chiffres latins uniquement",
    "Add Numbers": "Ajouter des numéros",
    "Add a blank page anywhere.": "Ajoutez une page vierge où vous voulez.",
    "Add an AES-128 password": "Ajouter un mot de passe AES-128",
    "Add numbers to the bottom center": "Ajouter des numéros en bas au centre",
    "Add page numbers to the bottom center.": "Ajoute des numéros de page en bas au centre.",
    "After page (0 = at the very front)": "Après la page (0 = tout au début)",
    "Angle": "Angle",
    "Author": "Auteur",
    "Back scan": "Numérisation du verso",
    "Back scan is in reverse order": "La numérisation du verso est dans l'ordre inverse",
    "Blank Page": "Page vierge",
    "Bottom (mm)": "Bas (mm)",
    "Change the order of pages": "Changer l'ordre des pages",
    "Change the order of pages.": "Change l'ordre des pages.",
    "Combine": "Combiner",
    "Combine front and back scans": "Combiner les numérisations recto et verso",
    "Combine several PDFs into one": "Combiner plusieurs PDF en un seul",
    "Combine several PDFs into one, in order.": "Combine plusieurs PDF en un seul, dans l'ordre.",
    "Compress": "Compresser",
    "Content": "Contenu",
    "Convert": "Convertir",
    "Convert .docx text into a PDF (text-focused). Layout, tables and images are not preserved.": "Convertit le texte d'un .docx en PDF (axé sur le texte). La mise en page, les tableaux et les images ne sont pas conservés.",
    "Convert .docx text to PDF (text-focused)": "Convertir le texte d'un .docx en PDF (axé sur le texte)",
    "Convert .docx to .hwpx (text-focused)": "Convertir .docx en .hwpx (axé sur le texte)",
    "Convert .docx to .hwpx. Only text and paragraphs are carried over.": "Convertit .docx en .hwpx. Seuls le texte et les paragraphes sont conservés.",
    "Convert .hwp text to PDF (text-focused)": "Convertir le texte d'un .hwp en PDF (axé sur le texte)",
    "Convert .hwpx text into a PDF (text-focused). Layout, tables and images are not preserved.": "Convertit le texte d'un .hwpx en PDF (axé sur le texte). La mise en page, les tableaux et les images ne sont pas conservés.",
    "Convert .hwpx text to PDF (text-focused)": "Convertir le texte d'un .hwpx en PDF (axé sur le texte)",
    "Convert .hwpx to .docx (text-focused)": "Convertir .hwpx en .docx (axé sur le texte)",
    "Convert .hwpx to .docx. Only text and paragraphs are carried over.": "Convertit .hwpx en .docx. Seuls le texte et les paragraphes sont conservés.",
    "Convert between PNG, JPG and GIF": "Convertir entre PNG, JPG et GIF",
    "Convert between PNG, JPG and GIF.": "Convertit entre PNG, JPG et GIF.",
    "Convert old .hwp text into a PDF (text-focused). Password-protected files aren't supported.": "Convertit le texte d'un ancien .hwp en PDF (axé sur le texte). Les fichiers protégés par mot de passe ne sont pas pris en charge.",
    "Create PDF": "Créer un PDF",
    "Creator": "Créateur",
    "Crop": "Rogner",
    "Delete pages you don't need.": "Supprime les pages dont vous n'avez pas besoin.",
    "Diagonal": "Diagonal",
    "Document": "Document",
    "Drag PDFs here, or click to choose": "Glissez des PDF ici, ou cliquez pour choisir",
    "Drag a .txt file here, or click to choose": "Glissez un fichier .txt ici, ou cliquez pour choisir",
    "Drag a PDF here, or click to choose": "Glissez un PDF ici, ou cliquez pour choisir",
    "Drag an image here, or click to choose": "Glissez une image ici, ou cliquez pour choisir",
    "Drag images here, or click to choose": "Glissez des images ici, ou cliquez pour choisir",
    "Drop a Hangul (.hwpx) file here, or click to choose": "Déposez un fichier Hangul (.hwpx) ici, ou cliquez pour choisir",
    "Drop a Word (.docx) file here, or click to choose": "Déposez un fichier Word (.docx) ici, ou cliquez pour choisir",
    "Drop a file here, or click to choose": "Déposez un fichier ici, ou cliquez pour choisir",
    "Drop an old Hangul (.hwp) file here, or click to choose": "Déposez un ancien fichier Hangul (.hwp) ici, ou cliquez pour choisir",
    "Edit or remove title, author and more.": "Modifiez ou supprimez le titre, l'auteur et plus encore.",
    "Edit or remove title, author, etc.": "Modifier ou supprimer le titre, l'auteur, etc.",
    "Encrypt the PDF with an AES-128 password.": "Chiffre le PDF avec un mot de passe AES-128.",
    "Encrypted": "Chiffré",
    "Encrypted — page info can't be read. Use the Unlock tool first.": "Chiffré — les informations de page ne peuvent pas être lues. Utilisez d'abord l'outil Déverrouiller.",
    "Enter a page range.": "Saisissez une plage de pages.",
    "Enter a password.": "Saisissez un mot de passe.",
    "Enter some text.": "Saisissez du texte.",
    "Enter the new order.": "Saisissez le nouvel ordre.",
    "Enter the pages to remove.": "Saisissez les pages à supprimer.",
    "Enter the watermark text.": "Saisissez le texte du filigrane.",
    "Extract": "Extraire",
    "Extract Images": "Extraire les images",
    "Extract Text": "Extraire le texte",
    "Extract selectable text to .txt": "Extraire le texte sélectionnable en .txt",
    "Extract selectable text to a .txt file. (Scanned images and special fonts may not extract well)": "Extrait le texte sélectionnable vers un fichier .txt. (Les images numérisées et les polices spéciales peuvent mal s'extraire)",
    "Failed to load font: ": "Échec du chargement de la police : ",
    "File size": "Taille du fichier",
    "Files are merged in the order you pick them.": "Les fichiers sont combinés dans l'ordre où vous les choisissez.",
    "Fit every page to A4 (off = original image size)": "Ajuster chaque page au format A4 (désactivé = taille d'origine de l'image)",
    "Font size (pt)": "Taille de police (pt)",
    "Format": "Format",
    "Front scan": "Numérisation du recto",
    "Hangul → PDF": "Hangul → PDF",
    "Hangul → Word": "Hangul → Word",
    "Image Convert": "Convertir l'image",
    "Image quality": "Qualité de l'image",
    "Images → PDF": "Images → PDF",
    "Insert": "Insérer",
    "Insert Blank Page": "Insérer une page vierge",
    "Insert a blank page anywhere": "Insérer une page vierge n'importe où",
    "Interleave": "Entrelacer",
    "JPG quality": "Qualité JPG",
    "Keywords": "Mots-clés",
    "Latin letters, numbers and symbols only": "Lettres, chiffres et symboles latins uniquement",
    "Leave blank to keep existing value": "Laissez vide pour conserver la valeur actuelle",
    "Leave blank to match the user password": "Laissez vide pour qu'il corresponde au mot de passe utilisateur",
    "Left (mm)": "Gauche (mm)",
    "Links and annotations will be removed": "Les liens et annotations seront supprimés",
    "List every page number once, in the new order": "Listez chaque numéro de page une fois, dans le nouvel ordre",
    "Make every page A4": "Mettre toutes les pages au format A4",
    "Make every page the same size (A4).": "Rend toutes les pages de la même taille (A4).",
    "Margin to trim from each side (mm)": "Marge à rogner de chaque côté (mm)",
    "Max image width": "Largeur maximale de l'image",
    "Merge": "Fusionner",
    "Merge PDFs": "Fusionner des PDF",
    "Merge front and back scans into order.": "Combine les numérisations recto et verso dans l'ordre.",
    "Metadata": "Métadonnées",
    "N-up": "N-up",
    "New order (e.g. 3,1,2 — list every page number)": "Nouvel ordre (ex. 3,1,2 — indiquez chaque numéro de page)",
    "No": "Non",
    "No server · no uploads · everything runs in this tab": "Aucun serveur · aucun envoi · tout s'exécute dans cet onglet",
    "Number of pages": "Nombre de pages",
    "Old Hangul → PDF": "Ancien Hangul → PDF",
    "Opacity": "Opacité",
    "Organize": "Organiser",
    "Owner password": "Mot de passe propriétaire",
    "PDF Info": "Infos PDF",
    "PDF producer": "Producteur du PDF",
    "PDF version": "Version du PDF",
    "PDF → Text": "PDF → Texte",
    "Page Numbers": "Numéros de page",
    "Page count": "Nombre de pages",
    "Page count, size, metadata": "Nombre de pages, taille, métadonnées",
    "Pages (e.g. 1-3,7)": "Pages (ex. 1-3,7)",
    "Pages (leave blank for all)": "Pages (laissez vide pour toutes)",
    "Pages to remove": "Pages à supprimer",
    "Paper Tools": "Paper Tools",
    "Password": "Mot de passe",
    "Paste text or drop a .txt file to make a PDF (Korean supported).": "Collez du texte ou déposez un fichier .txt pour créer un PDF (le coréen est pris en charge).",
    "Print 2 or 4 pages per sheet": "Imprimer 2 ou 4 pages par feuille",
    "Print 2 or 4 pages per sheet.": "Imprime 2 ou 4 pages par feuille.",
    "Protect": "Protéger",
    "Pull embedded images out into a ZIP.": "Extrait les images intégrées dans un ZIP.",
    "Pull images out into a ZIP": "Extraire les images dans un ZIP",
    "Pull out just the pages you need": "Extrayez uniquement les pages dont vous avez besoin",
    "Pull out just the pages you need.": "Extrait uniquement les pages dont vous avez besoin.",
    "Remove": "Supprimer",
    "Remove Pages": "Supprimer des pages",
    "Remove a password you know": "Supprimer un mot de passe que vous connaissez",
    "Remove a password you know.": "Supprime un mot de passe que vous connaissez.",
    "Remove all metadata (ignores the fields above)": "Supprimer toutes les métadonnées (ignore les champs ci-dessus)",
    "Reorder": "Réorganiser",
    "Reorder Pages": "Réorganiser les pages",
    "Resize": "Redimensionner",
    "Right (mm)": "Droite (mm)",
    "Rotate": "Pivoter",
    "Rotate pages 90/180/270°": "Pivoter les pages de 90/180/270°",
    "Save": "Enregistrer",
    "See page count, size and metadata.": "Affiche le nombre de pages, la taille et les métadonnées.",
    "Select a file.": "Sélectionnez un fichier.",
    "Select an image.": "Sélectionnez une image.",
    "Select at least 2 files.": "Sélectionnez au moins 2 fichiers.",
    "Select at least one image.": "Sélectionnez au moins une image.",
    "Select both the front and back files.": "Sélectionnez les fichiers recto et verso.",
    "Set Password": "Définir un mot de passe",
    "Show Info": "Afficher les infos",
    "Shrink file size by recompressing images": "Réduire la taille du fichier en recompressant les images",
    "Shrink file size by recompressing images.": "Réduit la taille du fichier en recompressant les images.",
    "Size": "Taille",
    "Size (pt)": "Taille (pt)",
    "Split & Extract": "Diviser et extraire",
    "Stamp": "Tampon",
    "Stamp text across the page": "Tamponner du texte sur toute la page",
    "Stamp text diagonally across the page.": "Tamponne du texte en diagonale sur la page.",
    "Subject": "Sujet",
    "Take out the pages you don't need": "Retirez les pages dont vous n'avez pas besoin",
    "Text": "Texte",
    "Text content": "Contenu du texte",
    "Text → PDF": "Texte → PDF",
    "Title": "Titre",
    "Top (mm)": "Haut (mm)",
    "Transform": "Transformer",
    "Trim the margins": "Rogner les marges",
    "Trim the margins.": "Rogne les marges.",
    "Turn PNG/JPG images into PDF pages, one per page.": "Transforme des images PNG/JPG en pages PDF, une par page.",
    "Turn PNG/JPG into PDF": "Transformer PNG/JPG en PDF",
    "Turn pages 90/180/270°.": "Fait pivoter les pages de 90/180/270°.",
    "Turn text into PDF (Korean supported)": "Transformer du texte en PDF (le coréen est pris en charge)",
    "Type or paste your text here…": "Saisissez ou collez votre texte ici…",
    "Unlock": "Déverrouiller",
    "User password": "Mot de passe utilisateur",
    "Watermark": "Filigrane",
    "Word → Hangul": "Word → Hangul",
    "Word → PDF": "Word → PDF",
    "Yes": "Oui",
    "Your files never leave the browser": "Vos fichiers ne quittent jamais le navigateur",
    "e.g. 1-3,7": "ex. 1-3,7",
    "e.g. 1-3,7 · for an open-ended range from the end, leave the end blank like 5-": "ex. 1-3,7 · pour une plage ouverte jusqu'à la fin, laissez la fin vide comme 5-",
    "← All tools": "← Tous les outils",
    "Loading tool…": "Chargement de l'outil…",
    "Working…": "Traitement en cours…",
    "Failed to load tool: ": "Échec du chargement de l'outil : ",
    "FAQ": "Foire aux questions",
    "Is Paper Tools free?": "Paper Tools est-il gratuit ?",
    "Yes. All 27 tools are free, with no account and no signup.": "Oui. Les 27 outils sont tous gratuits, sans compte ni inscription.",
    "Do my files get uploaded to a server?": "Mes fichiers sont-ils envoyés sur un serveur ?",
    "No. Every tool runs inside your browser via WebAssembly; your files never leave your device.": "Non. Chaque outil s'exécute directement dans votre navigateur grâce à WebAssembly ; vos fichiers ne quittent jamais votre appareil.",
    "Do I need to install anything?": "Dois-je installer quelque chose ?",
    "No. It works in any modern browser, on desktop or mobile.": "Non. Cela fonctionne dans n'importe quel navigateur moderne, sur ordinateur comme sur mobile.",
    "What can it do?": "Que peut-il faire ?",
    "Merge, split, rotate, crop, compress, protect and unlock PDFs; convert images (PNG/JPG/GIF); convert Word/Hangul documents to PDF; extract text and images; and more.": "Fusionner, diviser, faire pivoter, recadrer, compresser, protéger et déverrouiller des PDF ; convertir des images (PNG/JPG/GIF) ; convertir des documents Word/Hangul en PDF ; extraire du texte et des images ; et bien plus encore.",
    "Is it open source?": "Est-ce open source ?",
    "Yes. It's built with the Go standard library (no third-party dependencies) and compiled to WebAssembly.": "Oui. Il est développé avec la bibliothèque standard de Go (sans dépendance tierce) et compilé en WebAssembly.",
    "Privacy": "Confidentialité",
    "Privacy Policy": "Politique de confidentialité",
    "Your files are processed entirely in your browser. They are never uploaded to any server.": "Vos fichiers sont traités entièrement dans votre navigateur. Ils ne sont jamais envoyés vers un serveur.",
    "We don't use accounts, logins, or our own tracking cookies.": "Nous n'utilisons ni comptes, ni identifiants, ni cookies de suivi qui nous seraient propres.",
    "The site is served as static files by Cloudflare Pages, which may log standard request metadata (like IP and user agent) for security and performance.": "Le site est servi sous forme de fichiers statiques par Cloudflare Pages, qui peut enregistrer des métadonnées de requête standard (comme l'adresse IP et le user agent) à des fins de sécurité et de performance.",
    "If ads are shown, they are provided by EthicalAds, a privacy-focused network that serves contextual ads without tracking cookies or collecting personal data.": "Si des publicités sont affichées, elles proviennent d'EthicalAds, un réseau axé sur la confidentialité qui diffuse des publicités contextuelles sans cookies de suivi ni collecte de données personnelles.",
  },
  de: {
    "180°": "180°",
    "2 per sheet (landscape A4)": "2 pro Blatt (A4 Querformat)",
    "27 file tools. No uploads, no installs — right here in this tab.": "27 Datei-Tools. Kein Upload, keine Installation — alles direkt in diesem Tab.",
    "270° (90° counter-clockwise)": "270° (90° gegen den Uhrzeigersinn)",
    "4 per sheet (portrait A4)": "4 pro Blatt (A4 Hochformat)",
    "90° clockwise": "90° im Uhrzeigersinn",
    "A4 landscape (841.89×595.28pt)": "A4 Querformat (841.89×595.28pt)",
    "A4 portrait (595.28×841.89pt)": "A4 Hochformat (595.28×841.89pt)",
    "AES-128 · Latin letters/numbers only": "AES-128 · nur lateinische Buchstaben/Zahlen",
    "Add Numbers": "Nummern hinzufügen",
    "Add a blank page anywhere.": "Fügen Sie an beliebiger Stelle eine leere Seite ein.",
    "Add an AES-128 password": "AES-128-Passwort hinzufügen",
    "Add numbers to the bottom center": "Nummern unten mittig hinzufügen",
    "Add page numbers to the bottom center.": "Fügt Seitenzahlen unten mittig hinzu.",
    "After page (0 = at the very front)": "Nach Seite (0 = ganz am Anfang)",
    "Angle": "Winkel",
    "Author": "Autor",
    "Back scan": "Rückseiten-Scan",
    "Back scan is in reverse order": "Rückseiten-Scan liegt in umgekehrter Reihenfolge vor",
    "Blank Page": "Leere Seite",
    "Bottom (mm)": "Unten (mm)",
    "Change the order of pages": "Seitenreihenfolge ändern",
    "Change the order of pages.": "Ändert die Reihenfolge der Seiten.",
    "Combine": "Kombinieren",
    "Combine front and back scans": "Vorder- und Rückseiten-Scans kombinieren",
    "Combine several PDFs into one": "Mehrere PDFs zu einem zusammenführen",
    "Combine several PDFs into one, in order.": "Führt mehrere PDFs in der angegebenen Reihenfolge zu einem zusammen.",
    "Compress": "Komprimieren",
    "Content": "Inhalt",
    "Convert": "Konvertieren",
    "Convert .docx text into a PDF (text-focused). Layout, tables and images are not preserved.": "Wandelt den Text einer .docx-Datei in ein PDF um (textorientiert). Layout, Tabellen und Bilder bleiben dabei nicht erhalten.",
    "Convert .docx text to PDF (text-focused)": ".docx-Text in PDF umwandeln (textorientiert)",
    "Convert .docx to .hwpx (text-focused)": ".docx in .hwpx umwandeln (textorientiert)",
    "Convert .docx to .hwpx. Only text and paragraphs are carried over.": "Wandelt .docx in .hwpx um. Nur Text und Absätze werden übernommen.",
    "Convert .hwp text to PDF (text-focused)": ".hwp-Text in PDF umwandeln (textorientiert)",
    "Convert .hwpx text into a PDF (text-focused). Layout, tables and images are not preserved.": "Wandelt den Text einer .hwpx-Datei in ein PDF um (textorientiert). Layout, Tabellen und Bilder bleiben dabei nicht erhalten.",
    "Convert .hwpx text to PDF (text-focused)": ".hwpx-Text in PDF umwandeln (textorientiert)",
    "Convert .hwpx to .docx (text-focused)": ".hwpx in .docx umwandeln (textorientiert)",
    "Convert .hwpx to .docx. Only text and paragraphs are carried over.": "Wandelt .hwpx in .docx um. Nur Text und Absätze werden übernommen.",
    "Convert between PNG, JPG and GIF": "Zwischen PNG, JPG und GIF konvertieren",
    "Convert between PNG, JPG and GIF.": "Konvertiert zwischen PNG, JPG und GIF.",
    "Convert old .hwp text into a PDF (text-focused). Password-protected files aren't supported.": "Wandelt Text aus einer alten .hwp-Datei in ein PDF um (textorientiert). Passwortgeschützte Dateien werden nicht unterstützt.",
    "Create PDF": "PDF erstellen",
    "Creator": "Ersteller",
    "Crop": "Zuschneiden",
    "Delete pages you don't need.": "Löscht die Seiten, die Sie nicht benötigen.",
    "Diagonal": "Diagonal",
    "Document": "Dokument",
    "Drag PDFs here, or click to choose": "PDFs hierher ziehen oder klicken zum Auswählen",
    "Drag a .txt file here, or click to choose": ".txt-Datei hierher ziehen oder klicken zum Auswählen",
    "Drag a PDF here, or click to choose": "PDF hierher ziehen oder klicken zum Auswählen",
    "Drag an image here, or click to choose": "Bild hierher ziehen oder klicken zum Auswählen",
    "Drag images here, or click to choose": "Bilder hierher ziehen oder klicken zum Auswählen",
    "Drop a Hangul (.hwpx) file here, or click to choose": "Hangul-Datei (.hwpx) hier ablegen oder klicken zum Auswählen",
    "Drop a Word (.docx) file here, or click to choose": "Word-Datei (.docx) hier ablegen oder klicken zum Auswählen",
    "Drop a file here, or click to choose": "Datei hier ablegen oder klicken zum Auswählen",
    "Drop an old Hangul (.hwp) file here, or click to choose": "Alte Hangul-Datei (.hwp) hier ablegen oder klicken zum Auswählen",
    "Edit or remove title, author and more.": "Titel, Autor und mehr bearbeiten oder entfernen.",
    "Edit or remove title, author, etc.": "Titel, Autor usw. bearbeiten oder entfernen",
    "Encrypt the PDF with an AES-128 password.": "Verschlüsselt das PDF mit einem AES-128-Passwort.",
    "Encrypted": "Verschlüsselt",
    "Encrypted — page info can't be read. Use the Unlock tool first.": "Verschlüsselt — Seiteninformationen können nicht gelesen werden. Verwenden Sie zuerst das Tool „Entsperren\".",
    "Enter a page range.": "Bitte geben Sie einen Seitenbereich ein.",
    "Enter a password.": "Bitte geben Sie ein Passwort ein.",
    "Enter some text.": "Bitte geben Sie Text ein.",
    "Enter the new order.": "Bitte geben Sie die neue Reihenfolge ein.",
    "Enter the pages to remove.": "Bitte geben Sie die zu löschenden Seiten ein.",
    "Enter the watermark text.": "Bitte geben Sie den Wasserzeichentext ein.",
    "Extract": "Extrahieren",
    "Extract Images": "Bilder extrahieren",
    "Extract Text": "Text extrahieren",
    "Extract selectable text to .txt": "Auswählbaren Text als .txt extrahieren",
    "Extract selectable text to a .txt file. (Scanned images and special fonts may not extract well)": "Extrahiert auswählbaren Text in eine .txt-Datei. (Gescannte Bilder und spezielle Schriftarten lassen sich möglicherweise nicht gut extrahieren)",
    "Failed to load font: ": "Schriftart konnte nicht geladen werden: ",
    "File size": "Dateigröße",
    "Files are merged in the order you pick them.": "Die Dateien werden in der von Ihnen gewählten Reihenfolge zusammengeführt.",
    "Fit every page to A4 (off = original image size)": "Jede Seite an A4 anpassen (aus = ursprüngliche Bildgröße)",
    "Font size (pt)": "Schriftgröße (pt)",
    "Format": "Format",
    "Front scan": "Vorderseiten-Scan",
    "Hangul → PDF": "Hangul → PDF",
    "Hangul → Word": "Hangul → Word",
    "Image Convert": "Bild konvertieren",
    "Image quality": "Bildqualität",
    "Images → PDF": "Bilder → PDF",
    "Insert": "Einfügen",
    "Insert Blank Page": "Leere Seite einfügen",
    "Insert a blank page anywhere": "Leere Seite an beliebiger Stelle einfügen",
    "Interleave": "Verschränken",
    "JPG quality": "JPG-Qualität",
    "Keywords": "Schlüsselwörter",
    "Latin letters, numbers and symbols only": "Nur lateinische Buchstaben, Zahlen und Symbole",
    "Leave blank to keep existing value": "Leer lassen, um den vorhandenen Wert beizubehalten",
    "Leave blank to match the user password": "Leer lassen, um es mit dem Benutzerpasswort abzugleichen",
    "Left (mm)": "Links (mm)",
    "Links and annotations will be removed": "Links und Anmerkungen werden entfernt",
    "List every page number once, in the new order": "Jede Seitenzahl einmal in der neuen Reihenfolge auflisten",
    "Make every page A4": "Alle Seiten in A4 umwandeln",
    "Make every page the same size (A4).": "Bringt alle Seiten auf dieselbe Größe (A4).",
    "Margin to trim from each side (mm)": "Von jeder Seite zu entfernender Rand (mm)",
    "Max image width": "Maximale Bildbreite",
    "Merge": "Zusammenführen",
    "Merge PDFs": "PDFs zusammenführen",
    "Merge front and back scans into order.": "Führt Vorder- und Rückseiten-Scans in der richtigen Reihenfolge zusammen.",
    "Metadata": "Metadaten",
    "N-up": "N-up",
    "New order (e.g. 3,1,2 — list every page number)": "Neue Reihenfolge (z. B. 3,1,2 — jede Seitenzahl angeben)",
    "No": "Nein",
    "No server · no uploads · everything runs in this tab": "Kein Server · kein Upload · alles läuft in diesem Tab",
    "Number of pages": "Anzahl der Seiten",
    "Old Hangul → PDF": "Altes Hangul → PDF",
    "Opacity": "Deckkraft",
    "Organize": "Organisieren",
    "Owner password": "Eigentümerkennwort",
    "PDF Info": "PDF-Info",
    "PDF producer": "PDF-Produzent",
    "PDF version": "PDF-Version",
    "PDF → Text": "PDF → Text",
    "Page Numbers": "Seitenzahlen",
    "Page count": "Seitenanzahl",
    "Page count, size, metadata": "Seitenanzahl, Größe, Metadaten",
    "Pages (e.g. 1-3,7)": "Seiten (z. B. 1-3,7)",
    "Pages (leave blank for all)": "Seiten (leer lassen für alle)",
    "Pages to remove": "Zu entfernende Seiten",
    "Paper Tools": "Paper Tools",
    "Password": "Passwort",
    "Paste text or drop a .txt file to make a PDF (Korean supported).": "Text einfügen oder eine .txt-Datei ablegen, um ein PDF zu erstellen (Koreanisch wird unterstützt).",
    "Print 2 or 4 pages per sheet": "2 oder 4 Seiten pro Blatt drucken",
    "Print 2 or 4 pages per sheet.": "Druckt 2 oder 4 Seiten pro Blatt.",
    "Protect": "Schützen",
    "Pull embedded images out into a ZIP.": "Extrahiert eingebettete Bilder in ein ZIP.",
    "Pull images out into a ZIP": "Bilder in ein ZIP extrahieren",
    "Pull out just the pages you need": "Nur die benötigten Seiten herausziehen",
    "Pull out just the pages you need.": "Zieht nur die benötigten Seiten heraus.",
    "Remove": "Entfernen",
    "Remove Pages": "Seiten entfernen",
    "Remove a password you know": "Ein bekanntes Passwort entfernen",
    "Remove a password you know.": "Entfernt ein bekanntes Passwort.",
    "Remove all metadata (ignores the fields above)": "Alle Metadaten entfernen (ignoriert die obigen Felder)",
    "Reorder": "Neu anordnen",
    "Reorder Pages": "Seiten neu anordnen",
    "Resize": "Größe ändern",
    "Right (mm)": "Rechts (mm)",
    "Rotate": "Drehen",
    "Rotate pages 90/180/270°": "Seiten um 90/180/270° drehen",
    "Save": "Speichern",
    "See page count, size and metadata.": "Zeigt Seitenanzahl, Größe und Metadaten an.",
    "Select a file.": "Bitte wählen Sie eine Datei aus.",
    "Select an image.": "Bitte wählen Sie ein Bild aus.",
    "Select at least 2 files.": "Bitte wählen Sie mindestens 2 Dateien aus.",
    "Select at least one image.": "Bitte wählen Sie mindestens ein Bild aus.",
    "Select both the front and back files.": "Bitte wählen Sie sowohl die Vorder- als auch die Rückseiten-Datei aus.",
    "Set Password": "Passwort festlegen",
    "Show Info": "Info anzeigen",
    "Shrink file size by recompressing images": "Dateigröße durch erneutes Komprimieren der Bilder verkleinern",
    "Shrink file size by recompressing images.": "Verkleinert die Dateigröße durch erneutes Komprimieren der Bilder.",
    "Size": "Größe",
    "Size (pt)": "Größe (pt)",
    "Split & Extract": "Teilen & Extrahieren",
    "Stamp": "Stempel",
    "Stamp text across the page": "Text über die gesamte Seite stempeln",
    "Stamp text diagonally across the page.": "Stempelt Text diagonal über die Seite.",
    "Subject": "Betreff",
    "Take out the pages you don't need": "Nicht benötigte Seiten entfernen",
    "Text": "Text",
    "Text content": "Textinhalt",
    "Text → PDF": "Text → PDF",
    "Title": "Titel",
    "Top (mm)": "Oben (mm)",
    "Transform": "Umwandeln",
    "Trim the margins": "Ränder zuschneiden",
    "Trim the margins.": "Schneidet die Ränder zu.",
    "Turn PNG/JPG images into PDF pages, one per page.": "Wandelt PNG/JPG-Bilder in PDF-Seiten um, eines pro Seite.",
    "Turn PNG/JPG into PDF": "PNG/JPG in PDF umwandeln",
    "Turn pages 90/180/270°.": "Dreht Seiten um 90/180/270°.",
    "Turn text into PDF (Korean supported)": "Text in PDF umwandeln (Koreanisch wird unterstützt)",
    "Type or paste your text here…": "Text hier eingeben oder einfügen …",
    "Unlock": "Entsperren",
    "User password": "Benutzerkennwort",
    "Watermark": "Wasserzeichen",
    "Word → Hangul": "Word → Hangul",
    "Word → PDF": "Word → PDF",
    "Yes": "Ja",
    "Your files never leave the browser": "Ihre Dateien verlassen niemals den Browser",
    "e.g. 1-3,7": "z. B. 1-3,7",
    "e.g. 1-3,7 · for an open-ended range from the end, leave the end blank like 5-": "z. B. 1-3,7 · für einen offenen Bereich bis zum Ende das Ende leer lassen, z. B. 5-",
    "← All tools": "← Alle Tools",
    "Loading tool…": "Tool wird geladen …",
    "Working…": "Wird verarbeitet …",
    "Failed to load tool: ": "Tool konnte nicht geladen werden: ",
    "FAQ": "Häufig gestellte Fragen",
    "Is Paper Tools free?": "Ist Paper Tools kostenlos?",
    "Yes. All 27 tools are free, with no account and no signup.": "Ja. Alle 27 Tools sind kostenlos, ohne Konto und ohne Anmeldung.",
    "Do my files get uploaded to a server?": "Werden meine Dateien auf einen Server hochgeladen?",
    "No. Every tool runs inside your browser via WebAssembly; your files never leave your device.": "Nein. Jedes Tool läuft über WebAssembly direkt in Ihrem Browser; Ihre Dateien verlassen niemals Ihr Gerät.",
    "Do I need to install anything?": "Muss ich etwas installieren?",
    "No. It works in any modern browser, on desktop or mobile.": "Nein. Es funktioniert in jedem modernen Browser, am Desktop wie auf dem Smartphone.",
    "What can it do?": "Was kann es?",
    "Merge, split, rotate, crop, compress, protect and unlock PDFs; convert images (PNG/JPG/GIF); convert Word/Hangul documents to PDF; extract text and images; and more.": "PDFs zusammenführen, aufteilen, drehen, zuschneiden, komprimieren, schützen und entsperren; Bilder (PNG/JPG/GIF) umwandeln; Word/Hangul-Dokumente in PDF umwandeln; Text und Bilder extrahieren und vieles mehr.",
    "Is it open source?": "Ist es Open Source?",
    "Yes. It's built with the Go standard library (no third-party dependencies) and compiled to WebAssembly.": "Ja. Es basiert ausschließlich auf der Go-Standardbibliothek (keine Abhängigkeiten von Drittanbietern) und wird zu WebAssembly kompiliert.",
    "Privacy": "Datenschutz",
    "Privacy Policy": "Datenschutzerklärung",
    "Your files are processed entirely in your browser. They are never uploaded to any server.": "Ihre Dateien werden vollständig in Ihrem Browser verarbeitet. Sie werden niemals auf einen Server hochgeladen.",
    "We don't use accounts, logins, or our own tracking cookies.": "Wir verwenden keine Konten, keine Logins und keine eigenen Tracking-Cookies.",
    "The site is served as static files by Cloudflare Pages, which may log standard request metadata (like IP and user agent) for security and performance.": "Die Website wird als statische Dateien über Cloudflare Pages bereitgestellt, das aus Sicherheits- und Leistungsgründen Standard-Anfragedaten (wie IP-Adresse und User-Agent) protokollieren kann.",
    "If ads are shown, they are provided by EthicalAds, a privacy-focused network that serves contextual ads without tracking cookies or collecting personal data.": "Falls Werbung angezeigt wird, stammt sie von EthicalAds, einem auf Datenschutz ausgerichteten Netzwerk, das kontextbezogene Anzeigen ohne Tracking-Cookies oder das Sammeln personenbezogener Daten ausliefert.",
  },
};

function sanitizeLang(lang) {
  return LANG_CODES.indexOf(lang) !== -1 ? lang : "en";
}

const FIXED = window.__FIXED_LANG || "";
let LANG = FIXED || sanitizeLang(localStorage.getItem("lang"));

function t(en, ko) {
  if (LANG === "en") return en;
  if (LANG === "ko") return ko != null ? ko : en;
  return (I18N[LANG] && I18N[LANG][en]) || en;
}

function applyLang() {
  document.documentElement.lang = LANG;

  document.querySelectorAll("[data-i18n]").forEach((el) => {
    if (el.classList.contains("wordmark")) return; // brand stays literal
    const en = el.dataset.en != null ? el.dataset.en : el.textContent;
    let v;
    if (LANG === "en") v = en;
    else if (LANG === "ko") v = el.dataset.ko != null ? el.dataset.ko : en;
    else v = (I18N[LANG] && I18N[LANG][en]) || en;
    el.textContent = v;
  });

  document.querySelectorAll("[data-en-placeholder]").forEach((el) => {
    const en = el.dataset.enPlaceholder;
    el.placeholder = LANG === "en" ? en : LANG === "ko" ? el.dataset.koPlaceholder || en : (I18N[LANG] && I18N[LANG][en]) || en;
  });

  document.querySelectorAll("[data-en-aria]").forEach((el) => {
    const en = el.dataset.enAria;
    el.setAttribute("aria-label", LANG === "en" ? en : LANG === "ko" ? el.dataset.koAria || en : (I18N[LANG] && I18N[LANG][en]) || en);
  });

  const sel = document.querySelector(".langsel");
  if (sel) sel.value = LANG;
}

function setLang(lang) {
  const sanitized = sanitizeLang(lang);
  localStorage.setItem("lang", sanitized);

  if (FIXED) {
    if (sanitized === FIXED) {
      LANG = sanitized;
      applyLang();
      return;
    }
    const rest = location.pathname.slice(("/" + FIXED).length) || "/";
    location.href = sanitized === "en" ? rest : "/" + sanitized + rest;
    return;
  }

  if (sanitized !== "en") {
    location.href = "/" + sanitized + location.pathname;
    return;
  }

  LANG = sanitized;
  applyLang();
}

window.t = t;
window.setLang = setLang;

// Replace the plain EN/KO toggle markup with a <select> covering all 7
// languages. Falls back to a harmless click-listener for any legacy
// [data-lang] element that might still be around.
function initLangSelector() {
  const nav = document.querySelector("nav.langtoggle");
  if (nav) {
    const select = document.createElement("select");
    select.className = "langsel";
    select.setAttribute("aria-label", "Language");
    LANGS.forEach(([code, label]) => {
      const opt = document.createElement("option");
      opt.value = code;
      opt.textContent = label;
      if (code === LANG) opt.selected = true;
      select.appendChild(opt);
    });
    select.addEventListener("change", () => setLang(select.value));
    nav.innerHTML = "";
    nav.appendChild(select);
  }

  document.addEventListener("click", (e) => {
    const b = e.target.closest("[data-lang]");
    if (b) {
      e.preventDefault();
      setLang(b.dataset.lang);
    }
  });
}

/* --------------------------------------------------------------- head --- */

// initFavicon() injects the shared favicon link into every page's <head>,
// so individual HTML files don't each need the same <link> tag.
function initFavicon() {
  if (document.querySelector('link[rel="icon"]')) return;
  const link = document.createElement("link");
  link.rel = "icon";
  link.href = "/favicon.svg";
  document.head.appendChild(link);
}

/* ---------------------------------------------------------------- ads --- */

// initAds() is a no-op while AD_PUBLISHER is empty: no external script
// loads and no DOM changes happen, keeping the site fully local/private by
// default. Set AD_PUBLISHER (above) to an EthicalAds publisher id to opt
// in — this injects the EthicalAds script once, plus a single "stickybox"
// ad unit (an unobtrusive floating format that needs no layout slot).
// EthicalAds is contextual and cookieless.
let adsInited = false;
function initAds() {
  if (!AD_PUBLISHER || adsInited) return;
  adsInited = true;

  const script = document.createElement("script");
  script.src = "https://media.ethicalads.io/media/client/ethicalads.min.js";
  script.async = true;
  document.head.appendChild(script);

  const ad = document.createElement("div");
  ad.setAttribute("data-ea-publisher", AD_PUBLISHER);
  ad.setAttribute("data-ea-type", "image");
  ad.setAttribute("data-ea-style", "stickybox");
  document.body.appendChild(ad);
}

/* ----------------------------------------------------------- analytics --- */

// initAnalytics() is a no-op while CF_ANALYTICS_TOKEN is empty: no external
// script loads and no DOM changes happen, keeping the site fully local/
// private by default. Set CF_ANALYTICS_TOKEN (above) to a Cloudflare Web
// Analytics token to opt in — this injects the Cloudflare beacon script
// once. Cloudflare Web Analytics is cookieless and collects no personal
// data; opt-in via CF_ANALYTICS_TOKEN, so no consent banner needed.
let analyticsInited = false;
function initAnalytics() {
  if (!CF_ANALYTICS_TOKEN || analyticsInited) return;
  analyticsInited = true;

  const script = document.createElement("script");
  script.defer = true;
  script.src = "https://static.cloudflareinsights.com/beacon.min.js";
  script.setAttribute("data-cf-beacon", JSON.stringify({ token: CF_ANALYTICS_TOKEN }));
  document.head.appendChild(script);
}

initLangSelector();
applyLang();
initFavicon();
initAds();
initAnalytics();

/* ---------------------------------------------------------- boot / wasm --- */

// boot(wasmFile) instantiates the page's wasm binary, flips #status from
// "Loading tool…" to hidden once ready, and enables every [data-needs-wasm]
// control. Returns a promise that resolves once the module is running.
function boot(wasmFile) {
  const statusEl = document.getElementById("status");
  const setStatus = (msg) => {
    if (statusEl) statusEl.textContent = msg;
  };
  setStatus(t("Loading tool…", "도구 준비 중…"));

  const go = new Go();
  const ready = (async () => {
    let result;
    try {
      result = await WebAssembly.instantiateStreaming(fetch(wasmFile), go.importObject);
    } catch (e) {
      // Some static hosts serve .wasm with the wrong MIME type, which
      // breaks instantiateStreaming. Fall back to fetch + arrayBuffer.
      const resp = await fetch(wasmFile);
      const buf = await resp.arrayBuffer();
      result = await WebAssembly.instantiate(buf, go.importObject);
    }
    go.run(result.instance);
    setStatus("");
    if (statusEl) statusEl.hidden = true;
    document.querySelectorAll("[data-needs-wasm]").forEach((el) => {
      el.disabled = false;
    });
  })();

  ready.catch((err) => {
    setStatus(t("Failed to load tool: ", "도구를 불러오지 못했습니다: ") + err);
  });

  return ready;
}

/* -------------------------------------------------------------- dropzone --- */

// dropzone(id, {multiple}) wires up a .drop container: click and drag/drop
// both feed the hidden file input inside it, and the chosen files render as
// a .filelist. Returns { get files() }.
function dropzone(id, opts) {
  opts = opts || {};
  const el = document.getElementById(id);
  const input = el.querySelector("input[type=file]");
  const listEl = el.querySelector(".filelist");
  let files = [];

  function render() {
    if (!listEl) return;
    listEl.innerHTML = "";
    for (const f of files) {
      const li = document.createElement("li");
      const kb = Math.max(1, Math.round(f.size / 1024));
      li.textContent = f.name + " (" + kb + " KB)";
      listEl.appendChild(li);
    }
  }

  function setFiles(list) {
    const arr = Array.from(list);
    files = opts.multiple ? arr : arr.slice(0, 1);
    render();
  }

  el.setAttribute("role", "button");
  if (!el.hasAttribute("tabindex")) el.setAttribute("tabindex", "0");

  el.addEventListener("click", () => input.click());
  el.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      input.click();
    }
  });
  el.addEventListener("dragover", (e) => {
    e.preventDefault();
    el.classList.add("over");
  });
  el.addEventListener("dragleave", () => el.classList.remove("over"));
  el.addEventListener("drop", (e) => {
    e.preventDefault();
    el.classList.remove("over");
    if (e.dataTransfer && e.dataTransfer.files) setFiles(e.dataTransfer.files);
  });
  // The input sits inside the clickable zone; stop its own click from
  // bubbling back into el's click handler and reopening the picker.
  input.addEventListener("click", (e) => e.stopPropagation());
  input.addEventListener("change", () => setFiles(input.files));

  return {
    get files() {
      return files;
    },
  };
}

/* ---------------------------------------------------------------- bytes --- */

async function fileBytes(f) {
  const buf = await f.arrayBuffer();
  return new Uint8Array(buf);
}

async function allFiles(files) {
  const out = [];
  for (const f of files) out.push(await fileBytes(f));
  return out;
}

/* --------------------------------------------------------------- errors --- */

// Go-error substrings mapped to friendlier messages in every supported
// language. Anything unrecognized is shown as-is.
const ERROR_MAP = [
  { needle: "encrypted files are not supported", en: "This PDF is password-protected. Use the Unlock tool first.", ko: "암호가 걸린 PDF입니다. 먼저 [암호 해제] 도구를 사용하세요.", ja: "このPDFはパスワードで保護されています。先に[ロック解除]ツールを使ってください。", zh: "此 PDF 受密码保护。请先使用[解锁]工具。", es: "Este PDF está protegido con contraseña. Usa primero la herramienta Desbloquear.", fr: "Ce PDF est protégé par mot de passe. Utilisez d'abord l'outil Déverrouiller.", de: "Diese PDF-Datei ist passwortgeschützt. Verwenden Sie zuerst das Tool „Entsperren\"." },
  { needle: "wrong password", en: "Incorrect password.", ko: "암호가 올바르지 않습니다.", ja: "パスワードが正しくありません。", zh: "密码不正确。", es: "Contraseña incorrecta.", fr: "Mot de passe incorrect.", de: "Falsches Passwort." },
  { needle: "only Latin-1 text is supported", en: "Watermark supports Latin letters, numbers and symbols only.", ko: "워터마크는 영문·숫자·기호만 지원합니다.", ja: "ウォーターマークは英数字・記号のみ対応しています。", zh: "水印仅支持拉丁字母、数字和符号。", es: "La marca de agua solo admite letras, números y símbolos latinos.", fr: "Le filigrane ne prend en charge que les lettres, chiffres et symboles latins.", de: "Wasserzeichen unterstützen nur lateinische Buchstaben, Zahlen und Symbole." },
  { needle: "AES-256", en: "AES-256 encrypted files are not supported yet.", ko: "AES-256으로 암호화된 파일은 아직 지원하지 않습니다.", ja: "AES-256で暗号化されたファイルはまだ対応していません。", zh: "尚不支持 AES-256 加密的文件。", es: "Los archivos cifrados con AES-256 aún no son compatibles.", fr: "Les fichiers chiffrés avec AES-256 ne sont pas encore pris en charge.", de: "AES-256-verschlüsselte Dateien werden noch nicht unterstützt." },
  { needle: "not a PDF", en: "This doesn't look like a PDF file.", ko: "PDF 파일이 아닌 것 같습니다.", ja: "PDFファイルではないようです。", zh: "这看起来不是 PDF 文件。", es: "Esto no parece un archivo PDF.", fr: "Cela ne ressemble pas à un fichier PDF.", de: "Das sieht nicht nach einer PDF-Datei aus." },
  { needle: "unsupported format", en: "Only PNG or JPG is supported.", ko: "PNG 또는 JPG만 지원합니다.", ja: "PNGまたはJPGのみ対応しています。", zh: "仅支持 PNG 或 JPG。", es: "Solo se admiten PNG o JPG.", fr: "Seuls PNG ou JPG sont pris en charge.", de: "Nur PNG oder JPG werden unterstützt." },
  { needle: "CMYK", en: "CMYK JPEG is not supported.", ko: "CMYK JPEG는 지원하지 않습니다.", ja: "CMYKのJPEGには対応していません。", zh: "不支持 CMYK JPEG。", es: "No se admite JPEG en CMYK.", fr: "Le JPEG CMYK n'est pas pris en charge.", de: "CMYK-JPEG wird nicht unterstützt." },
  { needle: "유효한 docx", en: "This isn't a valid .docx file.", ko: "유효한 docx 파일이 아닙니다.", ja: "有効な.docxファイルではありません。", zh: "这不是有效的 .docx 文件。", es: "Este no es un archivo .docx válido.", fr: "Ce n'est pas un fichier .docx valide.", de: "Dies ist keine gültige .docx-Datei." },
  { needle: "유효한 hwpx", en: "This isn't a valid .hwpx file.", ko: "유효한 hwpx 파일이 아닙니다.", ja: "有効な.hwpxファイルではありません。", zh: "这不是有效的 .hwpx 文件。", es: "Este no es un archivo .hwpx válido.", fr: "Ce n'est pas un fichier .hwpx valide.", de: "Dies ist keine gültige .hwpx-Datei." },
  { needle: "유효한 hwp", en: "This isn't a valid .hwp file.", ko: "유효한 hwp 파일이 아닙니다.", ja: "有効な.hwpファイルではありません。", zh: "这不是有效的 .hwp 文件。", es: "Este no es un archivo .hwp válido.", fr: "Ce n'est pas un fichier .hwp valide.", de: "Dies ist keine gültige .hwp-Datei." },
  { needle: "암호가 걸린 한글", en: "This Hangul document is password-protected.", ko: "암호가 걸린 한글 문서입니다.", ja: "このHangul文書はパスワードで保護されています。", zh: "此 Hangul 文档受密码保护。", es: "Este documento Hangul está protegido con contraseña.", fr: "Ce document Hangul est protégé par mot de passe.", de: "Dieses Hangul-Dokument ist passwortgeschützt." },
  { needle: "처리 중 오류", en: "Something went wrong while processing the file.", ko: "처리 중 오류가 발생했습니다.", ja: "ファイルの処理中に問題が発生しました。", zh: "处理文件时出现问题。", es: "Algo salió mal al procesar el archivo.", fr: "Un problème est survenu lors du traitement du fichier.", de: "Beim Verarbeiten der Datei ist ein Fehler aufgetreten." },
  { needle: "no extractable images", en: "No extractable images were found.", ko: "추출할 수 있는 이미지가 없습니다.", ja: "抽出できる画像が見つかりませんでした。", zh: "未找到可提取的图片。", es: "No se encontraron imágenes extraíbles.", fr: "Aucune image extractible n'a été trouvée.", de: "Es wurden keine extrahierbaren Bilder gefunden." },
];

function mapError(msg) {
  for (const e of ERROR_MAP) {
    if (msg.indexOf(e.needle) !== -1) return e[LANG] || e.en;
  }
  return msg;
}

function showErr(el, msg) {
  if (el) el.textContent = mapError(String(msg));
}

/* ----------------------------------------------------------------- run --- */

// run(btn, fn) disables btn for the duration of the async fn, showing
// "Working…" in the active language, yields one tick so the disabled state
// paints before any heavy synchronous wasm call, and routes thrown errors
// to #err.
async function run(btn, fn) {
  const original = btn.textContent;
  const errEl = document.getElementById("err");
  if (errEl) errEl.textContent = "";
  btn.disabled = true;
  btn.textContent = t("Working…", "처리 중…");
  await new Promise((resolve) => setTimeout(resolve, 0));
  try {
    await fn();
  } catch (e) {
    showErr(errEl, e && e.message ? e.message : String(e));
  } finally {
    btn.disabled = false;
    btn.textContent = original;
  }
}

/* -------------------------------------------------------------- results --- */

// finish(r, filename, errEl, mime) handles the {data|json|error} shape every
// pdfRun call returns: downloads on data, returns the parsed object on
// json, shows the (translated) message on error. mime defaults to "application/pdf".
function finish(r, filename, errEl, mime) {
  if (r.error) {
    showErr(errEl, r.error);
    return null;
  }
  if (r.data) {
    download(r.data, filename, mime);
    return null;
  }
  if (r.json) {
    return JSON.parse(r.json);
  }
  return null;
}

function download(u8, name, mime) {
  mime = mime || "application/pdf";
  const blob = new Blob([u8], { type: mime });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
