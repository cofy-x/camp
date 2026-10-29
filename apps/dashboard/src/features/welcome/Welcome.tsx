import { PixelIcon } from '@cofy-x/camp-platinum';

export function About() {
  return (
    <div className="about-app app-pad">
      <PixelIcon name="conversation" size={64} />
      <h1>Camp</h1>
      <p>A shared desktop for people and agents.</p>
      <hr />
      <p>
        Discuss work in Rooms, keep sources and useful documents in Library, and
        bring the right agents into each conversation.
      </p>
      <p className="fine-print">
        Your team runs this Camp instance. Ask your administrator about access,
        model configuration, and backups.
      </p>
    </div>
  );
}
