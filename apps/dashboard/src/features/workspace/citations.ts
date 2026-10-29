import type { Reference } from './api';

export interface SourceCitation {
  reference: Reference;
  section: string;
  label: string;
}

// Source links only address sections in the exact evidence supplied to this run.
// The model's link label is not proof that a document or section exists.
export function resolveCitation(
  value: string,
  references: Reference[],
): SourceCitation | undefined {
  const match = /^camp-source:([1-8]):p([1-9][0-9]{0,3})$/.exec(value);
  if (!match) return;
  const reference = references[Number(match[1]) - 1];
  const document = reference?.document;
  const position = Number(match[2]);
  if (
    reference?.kind !== 'file' ||
    !reference.path ||
    !document?.id ||
    document.status !== 'ready' ||
    !['pdf', 'docx'].includes(document.format) ||
    !Number.isInteger(document.sectionCount) ||
    position > document.sectionCount
  )
    return;
  const location = document.format === 'pdf' ? 'Page' : 'Paragraph';
  return {
    reference,
    section: `p${position}`,
    label: `${reference.path.split('/').at(-1)} · ${location} ${position}`,
  };
}
