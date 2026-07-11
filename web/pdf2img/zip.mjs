// web/pdf2img/zip.mjs — thin re-export wrapper. The zipStore(files)
// implementation lives in ../zip.js (a dependency-free STORE-mode ZIP
// writer written without import/export so classic tool pages — Compress,
// Image Convert — can also load it via a plain <script> tag). Importing it
// here for its side effect (assigning zipStore onto the global object)
// keeps a single implementation shared by this module (used by
// pdf2img.mjs) and by zip.test.mjs.
import "../zip.js";

const globalTarget = typeof window !== "undefined" ? window : globalThis;

export const zipStore = globalTarget.zipStore;
export const zipStoreStream = globalTarget.zipStoreStream;
