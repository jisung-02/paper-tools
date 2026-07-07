#!/usr/bin/env node

import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.join(__dirname, "..");
const webDir = path.join(repoRoot, "web");
const toolsDir = __dirname;

const BASE = "https://papertools.dev";
const LANGS_TO_GEN = ["ko", "ja", "zh", "es", "fr", "de"];
const TOOL_SLUGS = [
  "merge", "interleave", "split", "remove", "reorder", "blank", "rotate",
  "crop", "resize", "nup", "img2pdf", "watermark", "pagenum", "compress",
  "flatten", "metadata", "info", "protect", "unlock", "imgconv", "pdftext", "pdfimages",
  "pdf2img", "txt2pdf", "docx2pdf", "hwpx2pdf", "hwp2pdf", "docx2hwpx", "hwpx2docx",
  "md2pdf", "stamp", "imgresize", "xlsx2csv", "pdfdiff"
];

let warningCount = 0;

// Helper: HTML entity decode
function htmlDecode(str) {
  if (!str) return "";
  const map = {
    "&amp;": "&",
    "&lt;": "<",
    "&gt;": ">",
    "&quot;": '"',
    "&#39;": "'",
    "&apos;": "'"
  };
  // Match both named and numeric entities
  return str.replace(/&(?:#39|apos|amp|lt|gt|quot);/g, (entity) => map[entity] || entity);
}

// Helper: HTML entity encode for safe HTML output
function htmlEncode(str) {
  if (!str) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// Helper: Encode text for a TEXT NODE only (no quote/apostrophe escaping)
function encodeTextNode(str) {
  if (!str) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// Helper: Get page key for meta-i18n.json lookup ("", "merge", "privacy", etc.)
function getPageKey(pagePath) {
  if (pagePath === "" || pagePath === "/") return "";
  if (pagePath === "privacy/") return "privacy";
  // Extract tool slug from "merge/" -> "merge"
  return pagePath.replace(/\/$/, "");
}

// Helper: Inject hreflang block before </head>
function injectHreflang(html, pagePath) {
  const hreflangBlock = `<!-- hreflang -->
<link rel="alternate" hreflang="en" href="${BASE}/${pagePath}">
<link rel="alternate" hreflang="ko" href="${BASE}/ko/${pagePath}">
<link rel="alternate" hreflang="ja" href="${BASE}/ja/${pagePath}">
<link rel="alternate" hreflang="zh" href="${BASE}/zh/${pagePath}">
<link rel="alternate" hreflang="es" href="${BASE}/es/${pagePath}">
<link rel="alternate" hreflang="fr" href="${BASE}/fr/${pagePath}">
<link rel="alternate" hreflang="de" href="${BASE}/de/${pagePath}">
<link rel="alternate" hreflang="x-default" href="${BASE}/${pagePath}">`;

  // Check if hreflang block already exists
  const hreflangRegex = /<!-- hreflang -->[\s\S]*?<link rel="alternate" hreflang="x-default"[^>]*>/;
  if (hreflangRegex.test(html)) {
    // Replace existing block
    return html.replace(hreflangRegex, hreflangBlock);
  } else {
    // Insert before </head>
    return html.replace(/(<\/head>)/i, hreflangBlock + "\n$1");
  }
}

// Helper: Extract tool name to ko map from landing page
function buildToolNameKoMap(landingHtml) {
  const map = {};
  // Find all data-i18n elements with data-en and data-ko
  const dataI18nRegex = /<[^>]+data-i18n[^>]*data-en="([^"]*)"[^>]*data-ko="([^"]*)"[^>]*>[^<]*<\/[^>]+>/g;
  let match;
  while ((match = dataI18nRegex.exec(landingHtml)) !== null) {
    const enText = htmlDecode(match[1]);
    const koText = htmlDecode(match[2]);
    map[enText] = koText;
  }
  return map;
}

// Helper: Extract the I18N dict for one language from web/i18n/<lang>.js
function extractLangDict(fileContent, lang) {
  // Find "window.I18N.<lang> = { ... };" with balanced braces
  const re = new RegExp(
    `window\\.I18N\\.${lang}\\s*=\\s*({[^}]*(?:{[^}]*}[^}]*)*});`
  );
  const match = fileContent.match(re);
  if (!match) {
    console.error(`ERROR: Could not find window.I18N.${lang} object in web/i18n/${lang}.js`);
    return {};
  }
  try {
    const dictStr = match[1];
    // Evaluate the object literal safely
    const fn = new Function("return (" + dictStr + ")");
    return fn();
  } catch (e) {
    console.error(`ERROR: Failed to parse I18N.${lang} object:`, e.message);
    return {};
  }
}

// Helper: Assemble the full {ja: {...}, zh: {...}, ...} shape from the
// per-language dict files under web/i18n/
function extractI18N(i18nDir) {
  const dictLangs = ["ja", "zh", "es", "fr", "de"];
  const i18n = {};
  for (const lang of dictLangs) {
    const filePath = path.join(i18nDir, `${lang}.js`);
    if (!fs.existsSync(filePath)) {
      console.error(`ERROR: web/i18n/${lang}.js not found`);
      i18n[lang] = {};
      continue;
    }
    const fileContent = fs.readFileSync(filePath, "utf-8");
    i18n[lang] = extractLangDict(fileContent, lang);
  }
  return i18n;
}

// Helper: Translate text using I18N or enToKo map
function translateText(text, lang, i18n, enToKo, sourceFile) {
  if (!text) return text;
  if (lang === "ko") {
    return enToKo[text] || text;
  }
  // For ja, zh, es, fr, de
  const translated = i18n[lang]?.[text];
  if (!translated) {
    warningCount++;
    process.stderr.write(`WARN missing I18N[${lang}]["${text}"] in ${sourceFile}\n`);
    return text;
  }
  return translated;
}

// Helper: Bake data-i18n elements for a given language
function bakeI18nElements(html, lang, i18n, enToKo, sourceFile) {
  // Match whole leaf elements carrying data-i18n; parse attributes from the
  // open tag separately (optional groups inside [^>]* runs never capture).
  const leafRe = /(<([a-zA-Z][a-zA-Z0-9]*)\b[^>]*\bdata-i18n\b[^>]*>)([^<]*)(<\/\2>)/g;
  html = html.replace(leafRe, (m, openTag, tagName, text, closeTag) => {
    const enM = openTag.match(/\bdata-en="([^"]*)"/);
    const koM = openTag.match(/\bdata-ko="([^"]*)"/);
    let resolved = null;
    if (lang === "ko") {
      if (koM) resolved = htmlDecode(koM[1]);
    } else if (enM) {
      resolved = translateText(htmlDecode(enM[1]), lang, i18n, enToKo, sourceFile);
    }
    if (resolved === null) return m; // leave element completely untouched
    return openTag + encodeTextNode(resolved) + closeTag;
  });

  // Handle placeholder attributes on input/textarea (tag-level parsing;
  // does not assume attribute ordering).
  const phRe = /<(?:input|textarea)\b[^>]*\bdata-en-placeholder="[^"]*"[^>]*>/g;
  html = html.replace(phRe, (tag) => {
    const enM = tag.match(/\bdata-en-placeholder="([^"]*)"/);
    const koM = tag.match(/\bdata-ko-placeholder="([^"]*)"/);
    let resolved = null;
    if (lang === "ko") {
      if (koM) resolved = htmlDecode(koM[1]);
    } else if (enM) {
      resolved = translateText(htmlDecode(enM[1]), lang, i18n, enToKo, sourceFile);
    }
    if (resolved === null) return tag;
    const enc = htmlEncode(resolved); // attribute value: full encode is correct here
    // Negative lookbehind so we don't match inside data-en-placeholder / data-ko-placeholder
    if (/(?<!-)placeholder="[^"]*"/.test(tag)) {
      return tag.replace(/(?<!-)placeholder="[^"]*"/, `placeholder="${enc}"`);
    }
    return tag.replace(/\bdata-en-placeholder=/, `placeholder="${enc}" data-en-placeholder=`);
  });

  return html;
}

// Helper: Translate meta tags in HEAD
function translateHeadMeta(html, pageKey, lang, metaI18n) {
  if (!metaI18n[pageKey]) return html;

  const pageData = metaI18n[pageKey][lang];
  if (!pageData) return html;

  const { title, description } = pageData;

  // Translate <title> (use JSON title as-is; it's a text node)
  if (title) {
    html = html.replace(/<title>[^<]*<\/title>/, `<title>${encodeTextNode(title)}</title>`);
  }

  // Translate <meta name="description">
  if (description) {
    html = html.replace(
      /(<meta name="description" content=")([^"]*)/,
      `$1${htmlEncode(description)}`
    );
  }

  // Translate og:title
  if (title) {
    html = html.replace(
      /(<meta property="og:title" content=")([^"]*)/,
      `$1${htmlEncode(title)}`
    );
  }

  // Translate og:description
  if (description) {
    html = html.replace(
      /(<meta property="og:description" content=")([^"]*)/,
      `$1${htmlEncode(description)}`
    );
  }

  // Translate twitter:title
  if (title) {
    html = html.replace(
      /(<meta name="twitter:title" content=")([^"]*)/,
      `$1${htmlEncode(title)}`
    );
  }

  // Translate twitter:description
  if (description) {
    html = html.replace(
      /(<meta name="twitter:description" content=")([^"]*)/,
      `$1${htmlEncode(description)}`
    );
  }

  return html;
}

// Helper: Rewrite canonical and og:url
function rewriteCanonicalURLs(html, pagePath, lang) {
  const langPrefix = lang === "en" ? "" : `${lang}/`;
  const canonicalUrl = `${BASE}/${langPrefix}${pagePath}`;

  // Rewrite canonical link
  html = html.replace(
    /(<link rel="canonical" href=")([^"]*)/,
    `$1${canonicalUrl}`
  );

  // Rewrite og:url (may not exist on privacy page)
  html = html.replace(
    /(<meta property="og:url" content=")([^"]*)/,
    `$1${canonicalUrl}`
  );

  return html;
}

// Helper: Mutate JSON-LD blocks
function transformJsonLd(html, pagePath, lang, pageKey, metaI18n, i18n, enToKo, sourceFile) {
  const pageData = metaI18n[pageKey]?.[lang];
  if (!pageData) return html;

  const langPrefix = lang === "en" ? "" : `${lang}/`;
  const canonicalUrl = `${BASE}/${langPrefix}${pagePath}`;

  return html.replace(
    /<script type="application\/ld\+json">([\s\S]*?)<\/script>/g,
    (match, jsonText) => {
      try {
        const obj = JSON.parse(jsonText);

        // Top-level description
        if (obj.description) {
          obj.description = pageData.description;
        }

        // WebApplication: translate name
        if (obj["@type"] === "WebApplication" && pageData.title) {
          obj.name = pageData.title;
        }

        // Top-level url
        if (obj.url) {
          obj.url = canonicalUrl;
        }

        // isPartOf.url
        if (obj.isPartOf?.url) {
          obj.isPartOf.url = `${BASE}/${langPrefix}`;
        }

        // Landing page: ItemList and FAQPage
        if (obj["@type"] === "ItemList" && obj.itemListElement) {
          obj.itemListElement = obj.itemListElement.map((item) => {
            // Extract tool slug from URL
            const urlMatch = item.url?.match(/\/([^/]+)\/?$/);
            const toolSlug = urlMatch ? urlMatch[1] : null;

            if (toolSlug) {
              item.url = `${BASE}/${langPrefix}${toolSlug}/`;

              // Translate name
              if (item.name) {
                item.name = translateText(item.name, lang, i18n, enToKo, sourceFile);
              }
            }

            return item;
          });
        }

        // HowTo (tool pages): translate the heading name and each step's name.
        if (obj["@type"] === "HowTo") {
          if (obj.name) {
            obj.name = translateText(obj.name, lang, i18n, enToKo, sourceFile);
          }
          if (Array.isArray(obj.step)) {
            obj.step = obj.step.map((step) => {
              if (step.name) {
                step.name = translateText(step.name, lang, i18n, enToKo, sourceFile);
              }
              return step;
            });
          }
        }

        // FAQPage (landing page and tool pages): translate each Q/A pair.
        if (obj["@type"] === "FAQPage" && obj.mainEntity) {
          obj.mainEntity = obj.mainEntity.map((qa) => {
            if (qa.name) {
              qa.name = translateText(qa.name, lang, i18n, enToKo, sourceFile);
            }
            if (qa.acceptedAnswer?.text) {
              qa.acceptedAnswer.text = translateText(
                qa.acceptedAnswer.text,
                lang,
                i18n,
                enToKo,
                sourceFile
              );
            }
            return qa;
          });
        }

        return `<script type="application/ld+json">${JSON.stringify(obj)}</script>`;
      } catch (e) {
        console.error("ERROR: Failed to parse JSON-LD:", e.message);
        return match;
      }
    }
  );
}

// Helper: Rewrite asset paths
function rewriteAssetPaths(html, toolSlug) {
  // ../style.css -> /style.css
  html = html.replace(/\.\.\/style\.css/g, "/style.css");

  // ./style.css -> /style.css (less common but be safe)
  html = html.replace(/\.\/style\.css/g, "/style.css");

  // ../app.js -> /app.js
  html = html.replace(/\.\.\/app\.js/g, "/app.js");

  // ./app.js -> /app.js
  html = html.replace(/\.\/app\.js/g, "/app.js");

  // ../wasm_exec.js -> /wasm_exec.js
  html = html.replace(/\.\.\/wasm_exec\.js/g, "/wasm_exec.js");

  // ./wasm_exec.js -> /wasm_exec.js
  html = html.replace(/\.\/wasm_exec\.js/g, "/wasm_exec.js");

  // ../NanumGothic-Regular.ttf -> /NanumGothic-Regular.ttf (global)
  html = html.replace(/\.\.\/NanumGothic-Regular\.ttf/g, "/NanumGothic-Regular.ttf");

  // ./NanumGothic-Regular.ttf -> /NanumGothic-Regular.ttf
  html = html.replace(/\.\/NanumGothic-Regular\.ttf/g, "/NanumGothic-Regular.ttf");

  // ./<tool>.wasm -> /<tool>/<tool>.wasm (tool pages only)
  if (toolSlug) {
    const wasmPattern = new RegExp(`\\./${toolSlug}\\.wasm`, "g");
    html = html.replace(wasmPattern, `/${toolSlug}/${toolSlug}.wasm`);
  }

  return html;
}

// Helper: Rewrite internal links to stay in-language
function rewriteInternalLinks(html, pagePath, lang) {
  const langPrefix = lang === "en" ? "" : `/${lang}`;

  // Landing page: tool card links "./merge/" -> "/L/merge/"
  // Tool pages and privacy: back-link and wordmark "../" -> "/L/"
  // Landing page wordmark "./" -> "/L/"

  if (pagePath === "" || pagePath === "/") {
    // Landing page
    // Tool links: href="./merge/" (etc.) -> href="/L/merge/"
    TOOL_SLUGS.forEach((slug) => {
      html = html.replace(
        new RegExp(`href="\\.\/${slug}\/`, "g"),
        `href="${langPrefix}/${slug}/`
      );
    });

    // Privacy link: href="./privacy/" -> href="/L/privacy/"
    html = html.replace(/href="\.\/privacy\//g, `href="${langPrefix}/privacy/`);

    // Wordmark: href="./" -> href="/L/"
    html = html.replace(/href="\.\/"/g, `href="${langPrefix}/"`);
  } else {
    // Tool pages and privacy
    // Back-link and wordmark: href="../" -> href="/L/"
    html = html.replace(/href="\.\.\/"/g, `href="${langPrefix}/"`);
  }

  return html;
}

// Helper: Insert fixed lang marker before app.js script
function insertFixedLangMarker(html, lang) {
  const marker = `<script>window.__FIXED_LANG="${lang}";</script>`;
  return html.replace(
    /(<script src="\/app\.js"><\/script>)/,
    marker + "\n$1"
  );
}

// Languages that ship their own translation dict under web/i18n/. Korean is
// excluded: its strings live inline as data-ko attributes, so ko pages need
// no dict at all.
const DICT_LANGS = ["ja", "zh", "es", "fr", "de"];

// Helper: Insert the language's dict <script> tag before the app.js script
function insertDictScript(html, lang) {
  const tag = `<script src="/i18n/${lang}.js"></script>`;
  return html.replace(
    /(<script src="\/app\.js"><\/script>)/,
    tag + "\n$1"
  );
}

// Main transformation pipeline for a single page per language
function transformPageForLanguage(sourceHtml, sourceFile, pagePath, lang, i18n, metaI18n, enToKo) {
  let html = sourceHtml;

  const pageKey = getPageKey(pagePath);

  // Extract tool slug for asset rewriting
  const toolSlug = pagePath && pagePath !== "/" ? pagePath.replace(/\/$/, "") : null;

  // EN->KO lookup used for JSON-LD (which has no sibling data-ko attribute to
  // read directly). The landing page alone doesn't cover strings that only
  // appear on a given tool page (e.g. its HowTo/FAQ copy), so merge in a map
  // built from this page's own data-i18n elements.
  const pageEnToKo = { ...enToKo, ...buildToolNameKoMap(sourceHtml) };

  // Step 4a: Bake data-i18n elements
  html = bakeI18nElements(html, lang, i18n, enToKo, sourceFile);

  // Step 4b: Change lang attribute
  html = html.replace(/<html[^>]*lang="[^"]*"/, (match) => {
    return match.replace(/lang="[^"]*"/, `lang="${lang}"`);
  });

  // Step 4c: Translate HEAD metadata
  html = translateHeadMeta(html, pageKey, lang, metaI18n);

  // Step 4d: Rewrite canonical and og:url
  html = rewriteCanonicalURLs(html, pagePath, lang);

  // Step 4e: Transform JSON-LD
  html = transformJsonLd(html, pagePath, lang, pageKey, metaI18n, i18n, pageEnToKo, sourceFile);

  // Step 4f: Rewrite asset paths
  html = rewriteAssetPaths(html, toolSlug);

  // Step 4g: Rewrite internal links
  html = rewriteInternalLinks(html, pagePath, lang);

  // Step 4h: Inject hreflang (using source pagePath, not L-prefixed)
  html = injectHreflang(html, pagePath);

  // Step 4i: Insert fixed lang marker
  if (lang !== "en") {
    html = insertFixedLangMarker(html, lang);
  }

  // Step 4j: Insert the language's dict script (ja/zh/es/fr/de only; ko
  // needs no dict since its strings are inline data-ko attributes)
  if (DICT_LANGS.includes(lang)) {
    html = insertDictScript(html, lang);
  }

  return html;
}

// Main function
async function main() {
  try {
    // STEP 0: Clean up generated language directories
    for (const lang of LANGS_TO_GEN) {
      const langDir = path.join(webDir, lang);
      if (fs.existsSync(langDir)) {
        fs.rmSync(langDir, { recursive: true, force: true });
      }
    }

    // STEP 1: Extract I18N from web/i18n/<lang>.js dict files
    const i18nDir = path.join(webDir, "i18n");
    const i18n = extractI18N(i18nDir);

    // STEP 2: Load meta-i18n.json
    let metaI18n = {};
    const metaPath = path.join(toolsDir, "meta-i18n.json");
    if (fs.existsSync(metaPath)) {
      const metaContent = fs.readFileSync(metaPath, "utf-8");
      metaI18n = JSON.parse(metaContent);
    } else {
      console.warn("WARNING: meta-i18n.json not found; page metadata will be untranslated");
    }

    // Build enToKo map from landing page
    const landingPath = path.join(webDir, "index.html");
    let enToKo = {};
    if (fs.existsSync(landingPath)) {
      const landingHtml = fs.readFileSync(landingPath, "utf-8");
      enToKo = buildToolNameKoMap(landingHtml);
    }

    // STEP 3: List source pages
    const sourcePages = [
      { file: "index.html", pagePath: "" }
    ];

    // Add tool pages
    for (const slug of TOOL_SLUGS) {
      sourcePages.push({
        file: path.join(slug, "index.html"),
        pagePath: `${slug}/`
      });
    }

    // Add privacy page
    sourcePages.push({
      file: path.join("privacy", "index.html"),
      pagePath: "privacy/"
    });

    // STEP 4 + 5: Transform and write pages for each language
    for (const lang of LANGS_TO_GEN) {
      for (const { file, pagePath } of sourcePages) {
        const sourceFile = path.join(webDir, file);

        if (!fs.existsSync(sourceFile)) {
          console.warn(`WARNING: source file not found: ${sourceFile}`);
          continue;
        }

        const sourceHtml = fs.readFileSync(sourceFile, "utf-8");

        // Transform for this language
        const transformedHtml = transformPageForLanguage(
          sourceHtml,
          sourceFile,
          pagePath,
          lang,
          i18n,
          metaI18n,
          enToKo
        );

        // Write to web/<lang>/<pagePath>index.html
        const outputDir = path.join(webDir, lang, pagePath);
        fs.mkdirSync(outputDir, { recursive: true });
        const outputFile = path.join(outputDir, "index.html");
        fs.writeFileSync(outputFile, transformedHtml, "utf-8");
      }
    }

    // STEP 6: Inject hreflang into English source pages
    for (const { file, pagePath } of sourcePages) {
      const sourceFile = path.join(webDir, file);

      if (!fs.existsSync(sourceFile)) continue;

      let sourceHtml = fs.readFileSync(sourceFile, "utf-8");

      // Apply hreflang injection only
      sourceHtml = injectHreflang(sourceHtml, pagePath);

      fs.writeFileSync(sourceFile, sourceHtml, "utf-8");
    }

    // STEP 7: Generate sitemap.xml
    const sitemapEntries = [];

    // English pages (root)
    for (const { pagePath } of sourcePages) {
      sitemapEntries.push(generateSitemapEntry(`${BASE}/${pagePath}`, pagePath));
    }

    // Language-prefixed pages
    for (const lang of LANGS_TO_GEN) {
      for (const { pagePath } of sourcePages) {
        const url = `${BASE}/${lang}/${pagePath}`;
        sitemapEntries.push(generateSitemapEntry(url, pagePath));
      }
    }

    const sitemap = generateSitemap(sitemapEntries);
    const sitemapPath = path.join(webDir, "sitemap.xml");
    fs.writeFileSync(sitemapPath, sitemap, "utf-8");

    // STEP 8: Print summary
    console.log(`${warningCount} missing I18N keys encountered`);
    console.log(`Generated ${LANGS_TO_GEN.length * sourcePages.length} pages across ${LANGS_TO_GEN.length} languages.`);
  } catch (error) {
    console.error("FATAL ERROR:", error.message);
    process.exit(1);
  }
}

// Helper: Generate a single sitemap entry
function generateSitemapEntry(url, pagePath) {
  let priority;
  if (pagePath === "") priority = "1.0"; // landing
  else if (pagePath === "privacy/") priority = "0.5"; // privacy
  else priority = "0.8"; // tool pages

  const xhtmlLinks = [];
  const pagePathForLinks = pagePath === "" ? "" : pagePath.replace(/\/$/, "");

  // All 8 hreflang links
  xhtmlLinks.push(`    <xhtml:link rel="alternate" hreflang="en" href="${BASE}/${pagePathForLinks}${pagePathForLinks ? "/" : ""}"/>`);
  for (const lang of LANGS_TO_GEN) {
    xhtmlLinks.push(`    <xhtml:link rel="alternate" hreflang="${lang}" href="${BASE}/${lang}/${pagePathForLinks}${pagePathForLinks ? "/" : ""}"/>`);
  }
  xhtmlLinks.push(`    <xhtml:link rel="alternate" hreflang="x-default" href="${BASE}/${pagePathForLinks}${pagePathForLinks ? "/" : ""}"/>`);

  return {
    loc: url,
    priority,
    xhtmlLinks
  };
}

// Helper: Generate complete sitemap XML
function generateSitemap(entries) {
  const lines = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">'
  ];

  for (const entry of entries) {
    lines.push("  <url>");
    lines.push(`    <loc>${entry.loc}</loc>`);
    lines.push(`    <priority>${entry.priority}</priority>`);
    for (const link of entry.xhtmlLinks) {
      lines.push(link);
    }
    lines.push("  </url>");
  }

  lines.push("</urlset>");
  return lines.join("\n");
}

main().catch((error) => {
  console.error("Unhandled error:", error);
  process.exit(1);
});
