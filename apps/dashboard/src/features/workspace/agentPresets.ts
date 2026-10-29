import type { Agent } from './api';

export interface AgentPreset {
  id: 'writer' | 'reviewer' | 'builder' | 'custom';
  label: string;
  description: string;
  name: string;
  icon: string;
  instructions: string;
  access: Agent['access'];
}

export const agentPresets: AgentPreset[] = [
  {
    id: 'writer',
    label: 'Writer',
    description: 'Draft and improve documents',
    name: 'Writer',
    icon: 'notes',
    instructions:
      'Write and improve clear, useful documents for the intended audience. Ask about missing requirements and preserve existing work.',
    access: 'write',
  },
  {
    id: 'reviewer',
    label: 'Reviewer',
    description: 'Read work and offer feedback',
    name: 'Reviewer',
    icon: 'checklist',
    instructions:
      'Review the referenced work for clarity, correctness, and gaps. Give specific, actionable feedback and distinguish required changes from suggestions.',
    access: 'read',
  },
  {
    id: 'builder',
    label: 'Builder',
    description: 'Change files and run checks',
    name: 'Builder',
    icon: 'terminal',
    instructions:
      'Implement focused changes, preserve existing behavior, and run relevant checks when available. Explain what changed and any remaining limitations.',
    access: 'write',
  },
  {
    id: 'custom',
    label: 'Custom',
    description: 'Define your own role',
    name: '',
    icon: 'agent',
    instructions: '',
    access: 'write',
  },
];

export function suggestAgentHandle(name: string, usedHandles: string[]) {
  if (!name.trim()) return '';
  let base = name
    .normalize('NFKD')
    .replace(/\p{Mark}/gu, '')
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^[-_]+|[-_]+$/g, '');
  if (!base) base = 'agent';
  if (!/^[a-z]/.test(base)) base = `agent-${base}`;
  base = base.slice(0, 48).replace(/[-_]+$/g, '');
  const used = new Set(usedHandles.map((handle) => handle.toLowerCase()));
  if (!used.has(base)) return base;
  let suffix = 2;
  while (true) {
    const ending = `-${suffix}`;
    const candidate = `${base.slice(0, 48 - ending.length)}${ending}`;
    if (!used.has(candidate)) return candidate;
    suffix += 1;
  }
}
