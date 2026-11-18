import { useRead } from '../../shared/reads/useRead';

type Check = { issue?: { code: string; message: string } };

export function useWorkExecution(
  roomId: string,
  participants: string[],
  completionMode: string | undefined,
  enabled: boolean,
) {
  const query = new URLSearchParams({
    completion: completionMode === 'automatic' ? 'automatic' : 'human',
  });
  for (const id of [...participants].sort()) query.append('agentId', id);
  const path =
    enabled && roomId && participants.length > 0 && participants.length <= 8
      ? `/rooms/${encodeURIComponent(roomId)}/work-execution?${query}`
      : null;
  const read = useRead<Check>(path);
  return {
    ready: Boolean(
      read.data && !read.data.issue && !read.error && !read.refreshing,
    ),
    checking: Boolean(path && !read.data && !read.error),
    issue: read.data?.issue,
    error: read.error,
    retry: read.refresh,
  };
}
