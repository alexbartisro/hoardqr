// Copies OCR (Tesseract.js) runtime assets out of node_modules into static/tesseract/
// so the built app never fetches them from cdn.jsdelivr.net at runtime — only
// `npm ci`/`npm install` (which already hits the network) needs internet access.
// Runs as an npm "postinstall" hook. Output is gitignored: these are build
// artifacts derived from installed packages, not source to commit.
//
// Core variant: the plain, non-SIMD, LSTM-only build. Not the fastest option
// tesseract.js offers, but deterministic — it skips the runtime SIMD/relaxed-SIMD
// feature-detection branch entirely, so exactly one core file ships instead of
// several, and behavior doesn't vary by device.

import { copyFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const webRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const outDir = path.join(webRoot, 'static', 'tesseract');
mkdirSync(outDir, { recursive: true });

const copies = [
	['node_modules/tesseract.js/dist/worker.min.js', 'worker.min.js'],
	['node_modules/tesseract.js-core/tesseract-core-lstm.wasm.js', 'tesseract-core-lstm.wasm.js'],
	['node_modules/tesseract.js-core/tesseract-core-lstm.wasm', 'tesseract-core-lstm.wasm'],
	['node_modules/@tesseract.js-data/eng/4.0.0_best_int/eng.traineddata.gz', 'eng.traineddata.gz'],
	['node_modules/@tesseract.js-data/ron/4.0.0_best_int/ron.traineddata.gz', 'ron.traineddata.gz']
];

for (const [from, to] of copies) {
	copyFileSync(path.join(webRoot, from), path.join(outDir, to));
}

console.log(`Vendored ${copies.length} Tesseract.js assets into static/tesseract/`);
