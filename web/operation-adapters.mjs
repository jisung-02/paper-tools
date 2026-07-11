export const workflowOperations = Object.freeze([
  "merge", "interleave", "remove", "reorder", "rotate", "flatten",
  "compress", "metadata", "watermark", "pagenum", "protect", "unlock",
]);

export const batchOperations = Object.freeze([
  "merge", "interleave", "remove", "rotate", "flatten", "compress",
  "metadata", "watermark", "pagenum", "protect", "unlock", "img2pdf",
]);

function one(inputs, id) {
  if (!Array.isArray(inputs) || inputs.length !== 1) throw new Error(`${id} requires one input`);
  return inputs[0];
}

function number(value, fallback, min, max, name) {
  const result = value == null ? fallback : Number(value);
  if (!Number.isFinite(result) || result < min || result > max) throw new Error(`invalid ${name}`);
  return result;
}

function text(value, fallback = "") {
  return value == null ? fallback : String(value);
}

export function operationArgs(id, inputs, params = {}) {
  switch (id) {
  case "merge":
    if (!Array.isArray(inputs) || inputs.length < 2) throw new Error("merge requires at least two inputs");
    return [inputs];
  case "interleave":
    if (!Array.isArray(inputs) || inputs.length !== 2) throw new Error("interleave requires two inputs");
    return [inputs[0], inputs[1], Boolean(params.reverseB)];
  case "img2pdf":
    if (!Array.isArray(inputs) || !inputs.length) throw new Error("img2pdf requires at least one input");
    return [inputs, params];
  case "remove": {
    const pages = text(params.pages);
    if (!pages.trim()) throw new Error("remove pages are required");
    return [one(inputs, id), pages];
  }
  case "reorder": {
    const order = text(params.order);
    if (!order.trim()) throw new Error("page order is required");
    return [one(inputs, id), order];
  }
  case "rotate": {
    const degrees = Number(params.degrees ?? 90);
    if (![90, 180, 270].includes(degrees)) throw new Error("invalid rotation degrees");
    return [one(inputs, id), text(params.pages, "all"), degrees];
  }
  case "flatten":
    return [one(inputs, id)];
  case "compress":
    return [
      one(inputs, id),
      Math.round(number(params.quality, 80, 1, 100, "quality")),
      Math.round(number(params.maxWidth, 1600, 0, 10000, "max width")),
      Boolean(params.grayscale),
    ];
  case "metadata":
    return [one(inputs, id), text(params.title), text(params.author), text(params.subject), text(params.keywords), Boolean(params.strip)];
  case "watermark": {
    const value = text(params.text);
    if (!value) throw new Error("watermark text is required");
    return [one(inputs, id), value, number(params.fontSize, 48, 4, 200, "font size"), number(params.opacity, 0.2, 0, 1, "opacity"), params.diagonal !== false];
  }
  case "pagenum":
    return [one(inputs, id), text(params.format, "{n}"), number(params.fontSize, 11, 4, 72, "font size")];
  case "protect": {
    const userPassword = text(params.userPassword);
    if (!userPassword) throw new Error("password is required");
    const cipher = text(params.cipher, "aes256");
    if (cipher !== "aes128" && cipher !== "aes256") throw new Error("invalid cipher");
    return [one(inputs, id), userPassword, text(params.ownerPassword), cipher];
  }
  case "unlock": {
    const password = text(params.password);
    if (!password) throw new Error("password is required");
    return [one(inputs, id), password];
  }
  default:
    throw new Error(`operation is not supported by workflow: ${id}`);
  }
}
