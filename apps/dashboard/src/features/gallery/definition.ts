import type { AppDefinition } from '@cofy-x/camp-platinum';
import { Gallery } from './Gallery';

export const galleryApp: AppDefinition = {
  id: 'gallery',
  title: 'Platinum Controls',
  icon: 'gallery',
  component: Gallery,
  defaultSize: { width: 440, height: 460 },
  minSize: { width: 310, height: 310 },
};
