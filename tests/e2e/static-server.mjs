import { createReadStream, existsSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";

const root = join(process.cwd(), "web");
const types = {
  ".css": "text/css; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".svg": "image/svg+xml",
  ".wasm": "application/wasm",
};

createServer((req, res) => {
  const pathname = new URL(req.url, "http://localhost").pathname;
  const relative = normalize(decodeURIComponent(pathname)).replace(/^[/\\]+/, "");
  let file = join(root, relative);
  if (!file.startsWith(root)) return res.writeHead(403).end();
  if (existsSync(file) && statSync(file).isDirectory()) file = join(file, "index.html");
  if (!existsSync(file) || !statSync(file).isFile()) return res.writeHead(404).end();
  res.writeHead(200, { "content-type": types[extname(file)] || "application/octet-stream" });
  createReadStream(file).pipe(res);
}).listen(8765, "127.0.0.1");
