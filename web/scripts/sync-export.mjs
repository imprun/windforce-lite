import { cp, mkdir, rm } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(webDir, "..", "..");
const outDir = path.join(root, "web", "out");
const embedDir = path.join(root, "internal", "webui", "assets");

await rm(embedDir, { recursive: true, force: true });
await mkdir(embedDir, { recursive: true });
await cp(outDir, embedDir, { recursive: true });
