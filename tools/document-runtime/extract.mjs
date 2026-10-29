import process from 'node:process';
import { Buffer } from 'node:buffer';
import console from 'node:console';
import { fileURLToPath, URL } from 'node:url';

const maxInput = 8 * 1024 * 1024;
const maxText = 1024 * 1024;
const maxPages = 200;

// Library diagnostics must never share the single-response protocol on stdout.
console.log = (...args) => console.error(...args);

class PreparationFailure extends Error {
  constructor(code, message, retryable = false) {
    super(message);
    this.code = code;
    this.retryable = retryable;
  }
}

async function inputBytes() {
  const chunks = [];
  let length = 0;
  for await (const chunk of process.stdin) {
    length += chunk.length;
    if (length > maxInput)
      throw new PreparationFailure(
        'input_limit',
        'The document exceeds the 8 MiB preparation limit.',
      );
    chunks.push(chunk);
  }
  const data = Buffer.concat(chunks, length);
  if (data.subarray(0, 5).toString('ascii') !== '%PDF-')
    throw new PreparationFailure(
      'malformed',
      'This file does not contain a readable PDF header.',
    );
  return new Uint8Array(data);
}

async function extract() {
  const data = await inputBytes();
  let pdfjs;
  try {
    pdfjs = await import('pdfjs-dist/legacy/build/pdf.mjs');
    if (pdfjs.version !== '6.4.299')
      throw new Error('Unexpected PDF.js version');
  } catch {
    throw new PreparationFailure(
      'unavailable',
      'The pinned PDF text runtime is unavailable. Run make install-document-runtime.',
      true,
    );
  }
  const asset = (name) =>
    fileURLToPath(
      new URL(`./node_modules/pdfjs-dist/${name}/`, import.meta.url),
    );
  const task = pdfjs.getDocument({
    data,
    cMapUrl: asset('cmaps'),
    cMapPacked: true,
    standardFontDataUrl: asset('standard_fonts'),
    useWorkerFetch: false,
    useSystemFonts: false,
    useWasm: false,
    disableFontFace: true,
    enableXfa: false,
    stopAtErrors: true,
    verbosity: 0,
  });
  try {
    const document = await task.promise;
    if (document.numPages > maxPages)
      throw new PreparationFailure(
        'limit_exceeded',
        'The PDF exceeds the 200-page preparation limit. Split it into smaller documents.',
      );
    const sections = [];
    const emptyPages = [];
    let total = 0;
    let itemsSeen = 0;
    for (let pageNumber = 1; pageNumber <= document.numPages; pageNumber++) {
      const page = await document.getPage(pageNumber);
      const chunks = [];
      let previous = null;
      const reader = page.streamTextContent().getReader();
      try {
        for (;;) {
          const chunk = await reader.read();
          if (chunk.done) break;
          for (const item of chunk.value.items) {
            if (!('str' in item)) continue;
            itemsSeen++;
            if (itemsSeen > 500000)
              throw new PreparationFailure(
                'limit_exceeded',
                'The PDF exceeds its text-complexity limit.',
              );
            let separator = '';
            if (
              previous &&
              item.str &&
              !/^\s/.test(item.str) &&
              !/\s$/.test(previous.str)
            ) {
              const height = Math.max(
                Math.abs(item.transform[3]),
                Math.abs(previous.transform[3]),
                1,
              );
              const lineChanged =
                Math.abs(item.transform[5] - previous.transform[5]) >
                height * 0.5;
              const gap =
                item.transform[4] - (previous.transform[4] + previous.width);
              if (lineChanged) separator = '\n';
              else if (gap > Math.max(1, height * 0.1)) separator = ' ';
            }
            const text = separator + item.str + (item.hasEOL ? '\n' : '');
            total += Buffer.byteLength(text, 'utf8');
            if (total > maxText)
              throw new PreparationFailure(
                'limit_exceeded',
                'The document exceeds the 1 MiB extracted-text limit.',
              );
            chunks.push(text);
            previous = item.hasEOL ? null : item;
          }
        }
      } finally {
        // The document task owns cancellation. An async iterator's implicit
        // cancel plus task.destroy races PDF.js stream closure on limit errors.
        reader.releaseLock();
      }
      const text = chunks.join('').trim();
      if (!text) emptyPages.push(pageNumber);
      sections.push({
        id: `p${pageNumber}`,
        label: `Page ${pageNumber}`,
        text,
      });
      page.cleanup();
    }
    if (emptyPages.length === sections.length)
      throw new PreparationFailure(
        'no_text',
        'No extractable text was found. This PDF may contain scanned pages or images; OCR is not available.',
      );
    const warnings = [
      'Page numbers identify physical PDF pages. Extracted reading order and spacing may differ from the original; images and diagrams are not read, and no OCR is performed.',
    ];
    if (emptyPages.length)
      warnings.push(
        `No extractable text on physical pages: ${emptyPages.join(', ')}. These pages may contain images or scans.`,
      );
    return { format: 'pdf', sections, warnings };
  } finally {
    await task.destroy();
  }
}

try {
  process.stdout.write(JSON.stringify({ result: await extract() }) + '\n');
} catch (error) {
  let failure;
  if (error instanceof PreparationFailure)
    failure = {
      code: error.code,
      message: error.message,
      retryable: error.retryable,
    };
  else if (error?.name === 'PasswordException')
    failure = {
      code: 'encrypted',
      message:
        'This PDF requires a password. Upload an unlocked copy to prepare its text.',
      retryable: false,
    };
  else
    failure = {
      code: 'malformed',
      message:
        'The PDF could not be read completely. It may be damaged or use unsupported features.',
      retryable: false,
    };
  process.stdout.write(JSON.stringify({ error: failure }) + '\n');
}
