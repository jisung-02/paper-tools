const MAX_WORDS = 500_000;
const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f-\u009f]/;

function positiveDimension(value, name) {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    throw new TypeError(`${name} must be a positive finite number`);
  }
  return value;
}

function finiteNumber(value, name) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new TypeError(`${name} must be a finite number`);
  }
  return value;
}

export function normalizeTextBoxes(boxes, imageWidth, imageHeight) {
  const width = positiveDimension(imageWidth, "image width");
  const height = positiveDimension(imageHeight, "image height");
  if (!Array.isArray(boxes)) throw new TypeError("OCR word boxes must be an array");
  if (boxes.length > MAX_WORDS) throw new RangeError(`OCR word boxes exceed ${MAX_WORDS}`);

  return boxes.map((item, index) => {
    if (!item || typeof item !== "object" || !item.rect || typeof item.rect !== "object") {
      throw new TypeError(`OCR word ${index + 1} is missing its bounds`);
    }
    if (typeof item.text !== "string") {
      throw new TypeError(`OCR word ${index + 1} text must be a string`);
    }
    const text = item.text.trim();
    if (!text || CONTROL_CHARACTERS.test(text)) {
      throw new TypeError(`OCR word ${index + 1} has invalid text`);
    }
    const confidence = finiteNumber(item.confidence, `OCR word ${index + 1} confidence`);
    if (confidence < 0 || confidence > 1) {
      throw new RangeError(`OCR word ${index + 1} confidence must be between 0 and 1`);
    }

    const left = finiteNumber(item.rect.left, `OCR word ${index + 1} bounds`);
    const top = finiteNumber(item.rect.top, `OCR word ${index + 1} bounds`);
    const right = finiteNumber(item.rect.right, `OCR word ${index + 1} bounds`);
    const bottom = finiteNumber(item.rect.bottom, `OCR word ${index + 1} bounds`);
    if (left < 0 || top < 0 || right > width || bottom > height || left >= right || top >= bottom) {
      throw new RangeError(`OCR word ${index + 1} has invalid bounds`);
    }

    return {
      text,
      confidence,
      left: left / width,
      top: top / height,
      right: right / width,
      bottom: bottom / height,
    };
  });
}
