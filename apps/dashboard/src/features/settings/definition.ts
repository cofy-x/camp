import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Settings } from './Settings';

export const settingsApp: AppDefinition = {
  id: 'settings',
  title: 'Settings',
  icon: 'settings',
  component: Settings,
  windowKey: () => 'main',
  defaultSize: { width: 850, height: 650 },
  minSize: { width: 360, height: 340 },
};
