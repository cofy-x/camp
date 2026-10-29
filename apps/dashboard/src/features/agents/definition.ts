import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Agents } from './Agents';

export const agentsApp: AppDefinition = {
  id: 'agents',
  title: 'Agents',
  icon: 'agent',
  component: Agents,
  windowKey: () => 'main',
  defaultSize: { width: 850, height: 620 },
  minSize: { width: 360, height: 320 },
};
