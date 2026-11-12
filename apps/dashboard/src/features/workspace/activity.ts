import { statusLabel, type RunActivityItem } from './api';

export interface ActivitySummary {
  seq: number;
  label: string;
  detail: string;
}

export function summarizeActivity(event: RunActivityItem): ActivitySummary {
  let label: string;
  let detail = '';
  if (event.type === 'model') label = 'Thinking';
  else if (event.type === 'tool_call') {
    const name = event.name;
    const tools: Record<string, string> = {
      bash: 'Running a command',
      read: 'Reading a file',
      read_file: 'Reading a file',
      write: 'Writing a file',
      write_file: 'Writing a file',
      edit: 'Editing a file',
      list_files: 'Listing files',
      search: 'Searching files',
    };
    label = Object.hasOwn(tools, name)
      ? tools[name]
      : name
        ? `Using ${name}`
        : 'Using a tool';
    detail = event.detail;
  } else if (event.type === 'completion') label = 'Finishing the response';
  else if (event.type === 'error') {
    label = 'Execution error';
    detail = event.detail;
  } else {
    label = Object.hasOwn(statusLabel, event.name)
      ? statusLabel[event.name as keyof typeof statusLabel]
      : 'Status updated';
  }
  return { seq: event.seq, label, detail };
}
