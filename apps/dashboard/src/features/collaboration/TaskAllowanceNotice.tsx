import type { Task } from './api';

type Allowance = Pick<
  Task,
  'status' | 'activeRunId' | 'modelRequestsUsed' | 'maxModelRequests'
>;

export function TaskAllowanceNotice({
  task,
  canContinue,
}: {
  task: Allowance;
  canContinue: boolean;
}) {
  // This describes the current recovery prerequisite, not the cause of a
  // historic execution error. The last allowed request can still finish work.
  if (
    !['needs_attention', 'waiting_for_person'].includes(task.status) ||
    task.activeRunId ||
    task.maxModelRequests <= 0 ||
    task.modelRequestsUsed < task.maxModelRequests
  )
    return null;

  return (
    <p className="collaboration-budget" role="status">
      Model request allowance exhausted ({task.modelRequestsUsed}/
      {task.maxModelRequests}).{' '}
      {canContinue
        ? 'Add model requests in Continue to resume.'
        : 'The owner or an administrator can add model requests.'}
    </p>
  );
}
