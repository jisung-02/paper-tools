import { diffPixels } from "./pixel-diff.mjs";

self.onmessage = (event) => {
  const message = event.data || {};
  try {
    const diff = diffPixels(
      message.a,
      message.widthA,
      message.heightA,
      message.b,
      message.widthB,
      message.heightB,
      message.options,
    );
    const heatmap = diff.heatmap;
    self.postMessage({
      id: message.id,
      ok: true,
      result: { ...diff, heatmap: heatmap.buffer },
    }, [heatmap.buffer]);
  } catch (error) {
    self.postMessage({
      id: message.id,
      ok: false,
      code: error?.code,
      error: error instanceof Error ? error.message : String(error),
    });
  }
};
