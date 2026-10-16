// Считает sha256 встроенного скрипта темы из dist/index.html и подставляет его в CSP nginx.conf:
// так скрипты разрешены только свои, без 'unsafe-inline'.
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";

const html = readFileSync("dist/index.html", "utf8");
const inline = html.match(/<script>([\s\S]*?)<\/script>/);
if (!inline) throw new Error("inline theme script not found in dist/index.html");
const hash = createHash("sha256").update(inline[1]).digest("base64");
const conf = readFileSync("nginx.conf", "utf8").replace("THEME_SCRIPT_HASH", hash);
writeFileSync("nginx.conf", conf);
console.log("CSP script hash:", hash);
