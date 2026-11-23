import { CampMark } from '../../shared/desktop/CampMark';

export function About() {
  return (
    <div className="about-app app-pad">
      <CampMark size={64} />
      <h1>Camp</h1>
      <p>A shared desktop for people and agents.</p>
      <hr />
      <p className="fine-print">
        Your team runs this Camp instance. Ask your administrator about access,
        model configuration, and backups.
      </p>
    </div>
  );
}
