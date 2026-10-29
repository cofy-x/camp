import type { RunEvent } from './api';

export interface ActivitySummary {
  seq: number;
  label: string;
  detail: string;
}

export function summarizeActivity(event: RunEvent): ActivitySummary | null {
  let label = '';
  let detail = '';
  if (event.type === 'model') label = 'Thinking';
  else if (event.type === 'tool_call') {
    try {
      const call = JSON.parse(event.text) as {
        name?: unknown;
        arguments?: unknown;
      };
      const name =
        typeof call.name === 'string'
          ? call.name.replace(/\s+/g, ' ').slice(0, 80)
          : '';
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
      const args =
        typeof call.arguments === 'string'
          ? JSON.parse(call.arguments)
          : call.arguments;
      if (args && typeof args === 'object') {
        const value = args.command ?? args.path ?? args.file_path;
        if (typeof value === 'string')
          detail = value.replace(/\s+/g, ' ').slice(0, 160);
      }
    } catch {
      label ||= 'Using a tool';
    }
  } else if (event.type === 'completion') label = 'Finishing the response';
  else return null;
  return { seq: event.seq, label, detail };
}
