import { useState } from 'react';
import { Button, Checkbox, PixelIcon } from '@cofy-x/camp-platinum';
import type { AppWindowProps } from '@cofy-x/camp-platinum';
import { runCommand } from '../../shared/desktop/dialogs';
import { useAppearanceData } from './PreferencesContext';

export function Appearance({ desktop }: AppWindowProps) {
  const data = useAppearanceData();
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState('');

  function update(patch: Partial<typeof data.preferences>) {
    setSaving(true);
    setStatus('Saving…');
    void runCommand(desktop, async () => {
      try {
        await data.savePreferences(patch);
        setStatus('Saved.');
      } catch (error) {
        setStatus('Settings were not saved.');
        throw error;
      } finally {
        setSaving(false);
      }
    });
  }

  return (
    <div className="settings-app camp-appearance">
      <div className="app-pad">
        <div className="panel-heading">
          <PixelIcon name="appearance" />
          <div>
            <h2>Appearance</h2>
          </div>
        </div>
        <fieldset disabled={saving}>
          <legend>Desktop Pattern</legend>
          <div className="wallpaper-options">
            {(['lavender', 'grid', 'solid'] as const).map((wallpaper) => (
              <button
                key={wallpaper}
                aria-pressed={data.preferences.wallpaper === wallpaper}
                className={`wallpaper-swatch ${wallpaper} ${data.preferences.wallpaper === wallpaper ? 'chosen' : ''}`}
                onClick={() => update({ wallpaper })}
              >
                <span className="swatch-preview" />
                <span>
                  {wallpaper === 'lavender'
                    ? 'Lavender'
                    : wallpaper === 'grid'
                      ? 'Graph Paper'
                      : 'Solid Lilac'}
                </span>
              </button>
            ))}
          </div>
        </fieldset>
        <fieldset disabled={saving}>
          <legend>Desktop Options</legend>
          <Checkbox
            label="Snap icons to the grid"
            checked={data.preferences.iconSnap}
            onChange={(event) => update({ iconSnap: event.target.checked })}
          />
          <Checkbox
            label="Expand the Control Strip"
            checked={data.preferences.controlStrip}
            onChange={(event) => update({ controlStrip: event.target.checked })}
          />
          <div className="app-actions">
            <Button onClick={() => desktop.arrangeIcons()}>
              Clean Up Desktop
            </Button>
          </div>
        </fieldset>
      </div>
      <p className="camp-appearance-status" role="status">
        {status}
      </p>
    </div>
  );
}
