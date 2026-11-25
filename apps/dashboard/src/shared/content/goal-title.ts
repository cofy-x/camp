const titleLength = 96;
const sentences =
  typeof Intl.Segmenter === 'function'
    ? new Intl.Segmenter(undefined, { granularity: 'sentence' })
    : null;

export function goalTitle(goal: string): string {
  const firstLine = goal.trim().split(/\r?\n/, 1)[0];
  const line = firstLine.replace(/^#{1,6}\s+/, '').trim();
  const firstSentence = sentences
    ?.segment(line)
    [Symbol.iterator]()
    .next().value;
  const title = firstSentence?.segment.trim() || line || firstLine;
  const characters = Array.from(title);
  if (characters.length <= titleLength) return title;

  const preview = characters.slice(0, titleLength - 1).join('');
  if (/\s/u.test(characters[titleLength - 1])) return `${preview.trimEnd()}…`;
  const lastSpace = preview.lastIndexOf(' ');
  return `${lastSpace > preview.length * 0.7 ? preview.slice(0, lastSpace) : preview}…`;
}
