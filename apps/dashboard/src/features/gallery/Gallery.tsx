import { useState } from 'react';
import {
  Button,
  Checkbox,
  Field,
  PixelIcon,
  Palette,
  Progress,
  Radio,
  Select,
  StatusBar,
  Tabs,
} from '@cofy-x/camp-platinum';
import type { AppWindowProps } from '@cofy-x/camp-platinum';

export function Gallery({ desktop }: AppWindowProps) {
  const [tab, setTab] = useState('controls');
  const [checked, setChecked] = useState(true);
  const [choice, setChoice] = useState('platinum');
  const [status, setStatus] = useState('Ready');
  const [paletteOpen, setPaletteOpen] = useState(true);
  const [text, setText] = useState('Hello, Platinum!');
  return (
    <div className="gallery-app">
      <div className="app-pad">
        <div className="panel-heading">
          <PixelIcon name="gallery" />
          <div>
            <strong>Platinum Controls</strong>
            <p>Every control is ready for your next application.</p>
          </div>
        </div>
        <Tabs
          label="Component categories"
          value={tab}
          onChange={setTab}
          tabs={[
            { id: 'controls', label: 'Controls' },
            { id: 'dialogs', label: 'Dialogs & Menus' },
            { id: 'icons', label: 'Icons' },
          ]}
        >
          {tab === 'controls' && (
            <div className="gallery-controls">
              <Field
                label="Text field"
                value={text}
                onChange={(event) => setText(event.target.value)}
              />
              <Field label="Disabled field" value="Not available" disabled />
              <Select
                label="Pop-up menu"
                value={choice}
                onChange={(event) => setChoice(event.target.value)}
              >
                <option value="platinum">Platinum</option>
                <option value="classic">Classic</option>
                <option value="custom">Custom</option>
                <option value="unavailable" disabled>
                  Unavailable
                </option>
              </Select>
              <Checkbox
                label="Enable this option"
                checked={checked}
                onChange={(event) => setChecked(event.target.checked)}
              />
              <Checkbox label="Unavailable option" disabled />
              <fieldset>
                <legend>Radio buttons</legend>
                <Radio
                  name="gallery-radio"
                  label="First option"
                  defaultChecked
                />
                <Radio name="gallery-radio" label="Second option" />
                <Radio name="gallery-radio" label="Disabled" disabled />
              </fieldset>
              <div className="app-actions">
                <Button onClick={() => setStatus('Button pressed.')}>
                  Button
                </Button>
                <Button
                  primary
                  onClick={() => setStatus('Default action completed.')}
                >
                  Default
                </Button>
                <Button disabled>Disabled</Button>
              </div>
              <Progress label="Progress" value={checked ? 65 : 30} max={100} />
            </div>
          )}
          {tab === 'dialogs' && (
            <div className="gallery-controls">
              {paletteOpen ? (
                <Palette
                  title="Utility Palette"
                  onClose={() => setPaletteOpen(false)}
                >
                  <p>
                    A compact nonmodal panel with the same title stripes and
                    close box as desktop windows.
                  </p>
                </Palette>
              ) : (
                <Button onClick={() => setPaletteOpen(true)}>
                  Show Palette
                </Button>
              )}
              <p>
                Dialogs keep keyboard focus inside, support Return and Escape,
                and return focus to their origin.
              </p>
              <Button
                onClick={() =>
                  void desktop
                    .dialog({
                      title: 'Clear the Example',
                      message: 'Clear the text field in Platinum Controls?',
                      buttons: [
                        {
                          label: 'Cancel',
                          value: 'cancel',
                          cancel: true,
                          default: true,
                        },
                        { label: 'Clear', value: 'clear' },
                      ],
                    })
                    .then((answer) => {
                      if (answer?.button === 'clear') {
                        setText('');
                        setStatus('The example text field was cleared.');
                      } else setStatus('The example text was kept.');
                    })
                }
              >
                Show Confirmation…
              </Button>
              <Button
                onClick={() =>
                  void desktop
                    .dialog({
                      title: 'A Platinum Dialog',
                      message: 'Your changes have been saved.',
                      buttons: [{ label: 'OK', value: 'ok', default: true }],
                    })
                    .then(() => setStatus('Dialog closed.'))
                }
              >
                Show Alert…
              </Button>
              <Button
                onClick={() =>
                  void desktop
                    .dialog({
                      title: 'Name Your Document',
                      input: { label: 'Name', value: 'Untitled.txt' },
                      buttons: [
                        { label: 'Cancel', value: 'cancel', cancel: true },
                        { label: 'Save', value: 'save', default: true },
                      ],
                    })
                    .then((answer) =>
                      setStatus(
                        answer?.button === 'save'
                          ? `Selected: ${answer.value}`
                          : 'Cancelled',
                      ),
                    )
                }
              >
                Show Input Dialog…
              </Button>
              <Button
                onClick={(event) => {
                  const rect = event.currentTarget.getBoundingClientRect();
                  desktop.contextMenu(
                    [
                      {
                        label: 'First command',
                        action: () => setStatus('First command selected.'),
                      },
                      {
                        label: 'Selected option',
                        checked: true,
                        action: () => setStatus('Selected option.'),
                      },
                      { separator: true },
                      {
                        label: 'Submenu',
                        children: [
                          {
                            label: 'Nested command',
                            action: () => setStatus('Nested command selected.'),
                          },
                        ],
                      },
                      { label: 'Unavailable', disabled: true },
                    ],
                    { x: rect.left, y: rect.bottom },
                  );
                }}
              >
                Show Menu
              </Button>
            </div>
          )}
          {tab === 'icons' && (
            <div className="icon-gallery">
              {(
                [
                  'computer',
                  'disk',
                  'folder',
                  'document',
                  'checklist',
                  'conversation',
                  'notes',
                  'trash',
                  'appearance',
                  'gallery',
                  'image',
                  'network',
                  'info',
                  'save',
                ] as const
              ).map((name) => (
                <div key={name}>
                  <PixelIcon name={name} />
                  <span>{name}</span>
                </div>
              ))}
            </div>
          )}
        </Tabs>
      </div>
      <StatusBar>{status}</StatusBar>
    </div>
  );
}
