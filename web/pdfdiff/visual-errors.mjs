export const VISUAL_ERROR_MESSAGES = Object.freeze({
  "input-limit": Object.freeze(["The selected PDFs exceed the visual comparison input limit.", "선택한 PDF가 시각 비교 입력 한도를 초과했습니다."]),
  "page-limit": Object.freeze(["The selected PDFs exceed the visual comparison page limit.", "선택한 PDF가 시각 비교 페이지 한도를 초과했습니다."]),
  "live-pixel-limit": Object.freeze(["The visual comparison exceeds the live pixel limit.", "시각 비교가 동시 픽셀 한도를 초과했습니다."]),
  "export-limit": Object.freeze(["The visual comparison export exceeds the output limit.", "시각 비교 내보내기가 출력 한도를 초과했습니다."]),
  "worker-failed": Object.freeze(["The visual comparison Worker failed.", "시각 비교 Worker를 실행하지 못했습니다."]),
  "canvas-unavailable": Object.freeze(["Canvas rendering is unavailable.", "Canvas 렌더링을 사용할 수 없습니다."]),
  "invalid-input": Object.freeze(["The visual comparison input is invalid.", "시각 비교 입력이 올바르지 않습니다."]),
  "invalid-options": Object.freeze(["The visual comparison settings are invalid.", "시각 비교 설정이 올바르지 않습니다."]),
  "export-failed": Object.freeze(["The visual comparison export failed.", "시각 비교 내보내기에 실패했습니다."]),
  "missing-input": Object.freeze(["Select both PDF A and PDF B.", "PDF A와 PDF B를 모두 선택하세요."]),
  unexpected: Object.freeze(["Visual comparison failed.", "시각 비교에 실패했습니다."]),
});

export function visualError(code, detail, ErrorType = Error) {
  const error = new ErrorType(String(detail || code || "visual comparison failed"));
  error.code = VISUAL_ERROR_MESSAGES[code] ? code : "unexpected";
  return error;
}

export function visualErrorMessage(error, translate = (english) => english) {
  const message = VISUAL_ERROR_MESSAGES[error?.code] || VISUAL_ERROR_MESSAGES.unexpected;
  return translate(message[0], message[1]);
}
