import { Buffer } from 'node:buffer';
import { writeFileSync } from 'node:fs';
import { crc32, deflateRawSync } from 'node:zlib';
import { fileURLToPath, URL } from 'node:url';
import process from 'node:process';

// Synthetic examples, generated without document applications or model calls.
export function pdf(pages) {
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ];
  const children = [];
  for (const lines of pages) {
    const pageId = objects.length + 1;
    children.push(`${pageId} 0 R`);
    objects.push(
      `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents ${pageId + 1} 0 R >>`,
    );
    const escape = (value) => value.replace(/[\\()]/g, '\\$&');
    const stream = [
      'BT',
      '/F1 12 Tf',
      '50 740 Td',
      '20 TL',
      ...lines.flatMap((line, index) => [
        index ? 'T*' : '',
        `(${escape(line)}) Tj`,
      ]),
      'ET',
    ].join('\n');
    objects.push(
      `<< /Length ${Buffer.byteLength(stream)} >>\nstream\n${stream}\nendstream`,
    );
  }
  objects[1] = `<< /Type /Pages /Kids [${children.join(' ')}] /Count ${pages.length} >>`;
  let document = '%PDF-1.7\n';
  const offsets = [0];
  for (let index = 0; index < objects.length; index++) {
    offsets.push(Buffer.byteLength(document));
    document += `${index + 1} 0 obj\n${objects[index]}\nendobj\n`;
  }
  const xref = Buffer.byteLength(document);
  document += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  document += offsets
    .slice(1)
    .map((offset) => `${String(offset).padStart(10, '0')} 00000 n \n`)
    .join('');
  document += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(document);
}

export function zip(entries) {
  const locals = [];
  const central = [];
  let offset = 0;
  for (const [name, value] of entries) {
    const filename = Buffer.from(name);
    const data = Buffer.from(value);
    const compressed = deflateRawSync(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(8, 8);
    local.writeUInt16LE(33, 12);
    local.writeUInt32LE(crc32(data), 14);
    local.writeUInt32LE(compressed.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(filename.length, 26);
    const directory = Buffer.alloc(46);
    directory.writeUInt32LE(0x02014b50);
    directory.writeUInt16LE(20, 4);
    local.copy(directory, 6, 4, 28);
    directory.writeUInt32LE(offset, 42);
    locals.push(local, filename, compressed);
    central.push(directory, filename);
    offset += local.length + filename.length + compressed.length;
  }
  const directory = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

export function docx(body, extra = []) {
  return zip([
    [
      '[Content_Types].xml',
      '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
    ],
    [
      '_rels/.rels',
      '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
    ],
    [
      'word/document.xml',
      `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>${body}<w:sectPr/></w:body></w:document>`,
    ],
    ...extra,
  ]);
}

export const customerBrief = [
  [
    'Customer support pilot - brief',
    'Sponsor: Morgan, Support Lead.',
    'Team: three support specialists; around 40 tickets each week.',
    'Duration: two weeks. Capacity: 6 staff-hours per week.',
    'Budget: no new software purchases.',
    'Target: 25% fewer repeat setup questions.',
    'Keep customer satisfaction at its current level.',
    'Start with a shared setup guide before considering a chatbot.',
  ],
  [
    'Measurement and approval - risks',
    'No customer-satisfaction baseline has been recorded yet.',
    'Count repeat setup tickets before and during the pilot.',
    'Ask the same short satisfaction question each week.',
    'Morgan approves guide changes before specialists publish them.',
    'Report uncertainty: a two-week sample cannot prove long-term impact.',
  ],
];

export const interviewBody = [
  '<w:p><w:r><w:t>Support interviews - pilot evidence</w:t></w:r></w:p>',
  '<w:p><w:r><w:t>Five staff and customer interviews informed this synthetic example.</w:t></w:r></w:p>',
  '<w:p><w:r><w:t>Specialist: Setup questions repeat, and answers live in private notes.</w:t></w:r></w:p>',
  '<w:p><w:r><w:t>Customer: A short checklist is easier to follow than a chatbot conversation.</w:t></w:r></w:p>',
  '<w:p><w:r><w:t>Specialist: An incorrect automated answer is worse than waiting for a person.</w:t></w:r></w:p>',
  '<w:p><w:r><w:t>Team request: Keep feedback visible and review repeat questions each week.</w:t></w:r></w:p>',
  '<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Approval owner</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Morgan approves every published guide update.</w:t></w:r></w:p></w:tc></w:tr></w:tbl>',
  '<w:p><w:r><w:t>Do not claim an existing satisfaction baseline: the team has not measured one.</w:t></w:r></w:p>',
].join('');

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  writeFileSync(
    new URL('customer-brief.pdf', import.meta.url),
    pdf(customerBrief),
  );
  writeFileSync(
    new URL('interview-notes.docx', import.meta.url),
    docx(interviewBody),
  );
  writeFileSync(
    new URL(
      '../../apps/server/internal/documenttext/testdata/no-text.pdf',
      import.meta.url,
    ),
    pdf([[]]),
  );
}
