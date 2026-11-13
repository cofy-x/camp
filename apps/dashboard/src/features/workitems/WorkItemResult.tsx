import { Button } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { Report } from '../workspace/Report';
import type { WorkItem } from './api';

export function WorkItemResult({ item }: { item: WorkItem }) {
  const team = useTeam();
  const result = item.result;
  if (!result) return null;
  const onlyFile = result.files.length === 1 ? result.files[0] : undefined;
  const directFile =
    onlyFile &&
    onlyFile.status !== 'D' &&
    onlyFile.status !== 'deleted' &&
    !result.filesLimited;

  function open(path?: string) {
    if (!result) return;
    void team.navigate({
      app: 'library',
      resourceId: result.resourceId,
      commit: result.commit,
      path,
      contextRoomId: item.roomId,
    });
  }

  return (
    <section className="work-item-result" aria-label="Work result">
      {result.commit && (
        <>
          <div className="work-item-file-heading">
            <span>{result.published ? 'Published to Room' : 'Team draft'}</span>
            {!directFile && <Button onClick={() => open()}>Open files</Button>}
          </div>
          <ul className="work-item-files">
            {result.files.map((file) => (
              <li key={file.path}>
                {file.status === 'D' || file.status === 'deleted' ? (
                  <span>{file.path}</span>
                ) : (
                  <Button onClick={() => open(file.path)}>{file.path}</Button>
                )}
                <span>
                  {file.status === 'D' || file.status === 'deleted'
                    ? 'Deleted'
                    : file.status === 'A' || file.status === 'added'
                      ? 'Added'
                      : 'Changed'}
                </span>
              </li>
            ))}
          </ul>
          {result.filesLimited && (
            <p>More files are available in Open files.</p>
          )}
        </>
      )}
      {result.summary && (
        <Report
          key={`${result.taskId}:${result.commit}`}
          content={result.summary}
          stateKey={`work-report:${item.id}:${result.taskId}:${result.commit}`}
        />
      )}
    </section>
  );
}
