import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Runners } from './Runners';

export const runnersApp: AppDefinition = {
  id: 'runners',
  title: 'Runners',
  icon: 'computer',
  component: Runners,
  windowKey: () => 'main',
  defaultSize: { width: 820, height: 650 },
  minSize: { width: 360, height: 320 },
};
