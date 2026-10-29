import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Appearance } from './Appearance';

export const appearanceApp: AppDefinition = {
  id: 'appearance',
  title: 'Appearance',
  icon: 'appearance',
  component: Appearance,
  defaultSize: { width: 440, height: 360 },
  minSize: { width: 320, height: 320 },
};
