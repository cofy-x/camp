import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import {
  dirname,
  extname,
  isAbsolute,
  relative,
  resolve,
  sep,
} from 'node:path';
import { fileURLToPath } from 'node:url';
import GithubSlugger from 'github-slugger';
import remarkParse from 'remark-parse';
import { unified } from 'unified';

const parser = unified().use(remarkParse);

function* walk(node) {
  yield node;
  for (const child of node.children ?? []) yield* walk(child);
}

function headingText(node) {
  if (node.type === 'html') return '';
  return (
    node.value ?? node.alt ?? node.children?.map(headingText).join('') ?? ''
  );
}

function inside(root, target) {
  const path = relative(root, target);
  return !isAbsolute(path) && path !== '..' && !path.startsWith(`..${sep}`);
}

export function checkDocs(directory) {
  const root = realpathSync(directory);
  const files = new Set(
    execFileSync(
      'git',
      [
        '-C',
        root,
        'ls-files',
        '-z',
        '--cached',
        '--others',
        '--exclude-standard',
      ],
      { encoding: 'utf8' },
    )
      .split('\0')
      .filter((file) => file && existsSync(resolve(root, file))),
  );
  const markdown = [...files].filter((file) => /\.md$/i.test(file)).sort();
  const errors = [];
  const documents = new Map();
  let links = 0;
  const report = (file, line, message) =>
    errors.push(`${file}:${line}: ${message}`);
  const read = (file) => {
    const path = realpathSync(resolve(root, file));
    if (!inside(root, path))
      throw new Error(`${file} points outside this checkout`);
    return readFileSync(path, 'utf8');
  };
  const json = (file) => JSON.parse(read(file));
  const document = (file) => {
    if (documents.has(file)) return documents.get(file);
    const source = read(file);
    const nodes = [...walk(parser.parse(source))];
    const slugger = new GithubSlugger();
    const headings = new Set(
      nodes
        .filter((node) => node.type === 'heading')
        .map((node) => slugger.slug(headingText(node))),
    );
    const result = { source, nodes, headings };
    documents.set(file, result);
    return result;
  };

  for (const file of markdown) {
    for (const node of document(file).nodes) {
      if (!['link', 'image', 'definition'].includes(node.type)) continue;
      const url = node.url;
      if (/^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(url)) continue;
      links++;
      const line = node.position.start.line;
      try {
        const hash = url.indexOf('#');
        const path = decodeURIComponent(
          (hash < 0 ? url : url.slice(0, hash)).split('?')[0],
        );
        const anchor = hash < 0 ? '' : decodeURIComponent(url.slice(hash + 1));
        const target = path
          ? path.startsWith('/')
            ? resolve(root, `.${path}`)
            : resolve(root, dirname(file), path)
          : resolve(root, file);
        if (!inside(root, target)) {
          report(file, line, `Local link leaves this checkout: ${url}`);
          continue;
        }
        const name = relative(root, target).split(sep).join('/');
        const directoryTarget = [...files].some((entry) =>
          entry.startsWith(`${name}/`),
        );
        if (
          !existsSync(target) ||
          (!files.has(name) && name && !directoryTarget)
        ) {
          report(file, line, `Missing or ignored local target: ${url}`);
        } else if (!inside(root, realpathSync(target))) {
          report(file, line, `Local link points outside this checkout: ${url}`);
        } else if (anchor && extname(target).toLowerCase() === '.md') {
          if (!document(name).headings.has(anchor)) {
            report(file, line, `Missing heading anchor: ${url}`);
          }
        }
      } catch (error) {
        report(file, line, `Cannot check ${url}: ${error.message}`);
      }
    }
  }

  const manifest = json('package.json');
  const pnpm = manifest.packageManager?.match(
    /^pnpm@(\d+\.\d+\.\d+(?:-[\w.-]+)?)(?:\+.*)?$/,
  )?.[1];
  const go = read('go.mod').match(/^go (\d+\.\d+\.\d+)\r?$/m)?.[1];
  const pins = {
    go: { version: go, owner: 'go.mod' },
    node: { version: read('.node-version').trim(), owner: '.node-version' },
    pnpm: { version: pnpm, owner: 'package.json#packageManager' },
    dsh: {
      version: json('tools/dsh-runtime/package.json').dependencies[
        '@deepseek-ai/dsh'
      ],
      owner: 'tools/dsh-runtime/package.json',
    },
    'pdf.js': {
      version: json('tools/document-runtime/package.json').dependencies[
        'pdfjs-dist'
      ],
      owner: 'tools/document-runtime/package.json',
    },
  };
  for (const [label, pin] of Object.entries(pins)) {
    if (!pin.version || !/^\d+\.\d+\.\d+(?:-[\w.-]+)?$/.test(pin.version)) {
      throw new Error(`Expected an exact ${label} version in ${pin.owner}`);
    }
  }
  const compare = (file, line, label, actual, pin) => {
    if (actual !== pin.version) {
      report(
        file,
        line,
        `${label} ${actual} differs from ${pin.owner} (${pin.version})`,
      );
    }
  };
  for (const file of markdown) {
    if (file.startsWith('docs/decisions/')) continue;
    const { source } = document(file);
    for (const match of source.matchAll(
      /\b(Go|Node|pnpm|DSH|PDF\.js)(?:[*`]*\s+[*`]*v?|@)(\d+\.\d+\.\d+(?:-[\w-]+(?:\.[\w-]+)*)?)/gi,
    )) {
      compare(
        file,
        source.slice(0, match.index).split('\n').length,
        match[1],
        match[2],
        pins[match[1].toLowerCase()],
      );
    }
  }
  for (const file of files) {
    if (file !== 'package.json' && !file.endsWith('/package.json')) continue;
    const pkg = json(file);
    for (const label of ['node', 'pnpm']) {
      if (pkg.engines?.[label] !== undefined) {
        compare(file, 1, `engines.${label}`, pkg.engines[label], pins[label]);
      }
    }
    if (pkg.packageManager !== undefined) {
      compare(file, 1, 'packageManager', pkg.packageManager.split('+')[0], {
        ...pins.pnpm,
        version: `pnpm@${pnpm}`,
      });
    }
  }
  const layerfs = json('tools/layerfs-runtime/pin.json');
  for (const label of ['node', 'pnpm']) {
    compare(
      'tools/layerfs-runtime/pin.json',
      1,
      label,
      layerfs[label],
      pins[label],
    );
  }
  compare(
    'tools/project-runtime/package.json',
    1,
    'dependencies.pnpm',
    json('tools/project-runtime/package.json').dependencies.pnpm,
    pins.pnpm,
  );
  return { errors, documents: markdown.length, links };
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const result = checkDocs(
      resolve(process.argv[2] ?? resolve(import.meta.dirname, '..')),
    );
    if (result.errors.length) {
      console.error(result.errors.join('\n'));
      process.exitCode = 1;
    } else {
      console.log(
        `Checked ${result.documents} Markdown files, ${result.links} local links and pinned versions.`,
      );
    }
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
