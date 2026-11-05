import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Work } from './Work';

export const workApp: AppDefinition = {
  id: 'work',
  title: 'Work',
  icon: 'checklist',
  component: Work,
  windowKey: () => 'main',
  defaultSize: { width: 940, height: 650 },
  minSize: { width: 360, height: 320 },
};
