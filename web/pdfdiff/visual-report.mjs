const encoder = new TextEncoder();

function pageSummary(value) {
  const changedPixels = Number(value?.changedPixels);
  const totalPixels = Number(value?.totalPixels);
  if (!Number.isSafeInteger(value?.index) || value.index < 0 ||
      !Number.isSafeInteger(changedPixels) || changedPixels < 0 ||
      !Number.isSafeInteger(totalPixels) || totalPixels < changedPixels) {
    throw new TypeError("invalid visual diff summary");
  }
  return {
    index: value.index,
    leftPage: value.pair?.a == null ? null : value.pair.a + 1,
    rightPage: value.pair?.b == null ? null : value.pair.b + 1,
    changedPixels,
    totalPixels,
    ratio: totalPixels ? changedPixels / totalPixels : 0,
    bounds: value.bounds ? { ...value.bounds } : null,
    pageSizeChanged: Boolean(value.pageSizeChanged),
  };
}

export function buildVisualReport({ fallback = false, summaries, threshold, antialiasTolerance }) {
  if (!Array.isArray(summaries)) throw new TypeError("visual summaries must be an array");
  const pages = summaries.map(pageSummary);
  return {
    schema: "paper-tools-visual-diff-v1",
    alignmentFallback: Boolean(fallback),
    threshold: Number(threshold),
    antialiasTolerance: Number(antialiasTolerance),
    comparedPages: pages.length,
    changedPages: pages.filter((page) => page.changedPixels || page.pageSizeChanged).length,
    pages,
  };
}

export async function* visualReportEntries(options) {
  const report = buildVisualReport(options);
  if (typeof options.heatmap !== "function") throw new TypeError("heatmap renderer is required");
  for (const page of report.pages) {
    if (!page.changedPixels && !page.pageSizeChanged) continue;
    const data = await options.heatmap(page.index);
    if (data != null) {
      yield { name: `heatmap-page-${String(page.index + 1).padStart(4, "0")}.png`, data };
    }
  }
  yield {
    name: "report.json",
    data: encoder.encode(`${JSON.stringify(report, null, 2)}\n`),
  };
}
