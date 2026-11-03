import type {
  DocumentSnapshot,
  DocumentStatus,
  ExtractedDocument,
} from './api';

export function documentFormat(path = '') {
  const extension = path.split('.').at(-1)?.toLowerCase();
  return extension === 'pdf' || extension === 'docx' ? extension : null;
}

export function documentPending(status: DocumentStatus) {
  return status === 'queued' || status === 'extracting';
}

export function documentSnapshot(
  document: ExtractedDocument,
): DocumentSnapshot {
  return {
    id: document.id,
    format: document.format,
    status: document.status,
    sectionCount: document.sections.length,
    warnings: document.warnings,
    ...(document.error ? { error: document.error } : {}),
  };
}

export function documentStatusLabel(document: DocumentSnapshot) {
  if (document.status !== 'ready') return 'Original only';
  const unit = document.format === 'pdf' ? 'page' : 'paragraph';
  return `${document.sectionCount} ${unit}${document.sectionCount === 1 ? '' : 's'} · Saved text`;
}
