import { existsSync } from "node:fs";

const localChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

export function chromiumLaunchOptions() {
  return existsSync(localChrome) ? { executablePath: localChrome } : {};
}
