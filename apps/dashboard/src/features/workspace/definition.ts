import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Workspace } from './Workspace';

export const workspaceApp: AppDefinition = {
  id: 'workspace',
  title: 'Workspace',
  icon: 'conversation',
  component: Workspace,
  windowKey: () => 'main',
  defaultSize: { width: 1120, height: 680 },
  minSize: { width: 360, height: 320 },
};
