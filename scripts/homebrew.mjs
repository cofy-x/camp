import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';
import { validVersion } from './release.mjs';

export function homebrewFormula(descriptor) {
  if (!validVersion(descriptor.version))
    throw new Error('A Homebrew formula requires a fixed release.');
  const artifact = (platform) => {
    const value = descriptor.servers?.[platform];
    if (
      value?.filename !== `camp-server-${platform}.tar.gz` ||
      !/^[a-f0-9]{64}$/.test(value.sha256)
    )
      throw new Error(
        'A Homebrew formula requires verified platform artifacts.',
      );
    return `url "https://github.com/cofy-x/camp/releases/download/${descriptor.version}/${value.filename}"\n      sha256 "${value.sha256}"`;
  };
  return `class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "${descriptor.version.slice(1)}"
  license "MIT"

  on_macos do
    on_arm do
      ${artifact('darwin-arm64')}
    end
    on_intel do
      ${artifact('darwin-amd64')}
    end
  end

  on_linux do
    on_intel do
      ${artifact('linux-amd64')}
    end
  end

  depends_on "git"

  def install
    bin.install "camp"
    doc.install "INSTALL.md"
    pkgshare.install "licenses", "runner-release.json"
  end

  def caveats
    <<~EOS
      Run camp to start a local Server, then open http://127.0.0.1:3001.
      No service starts automatically. Native Agent work requires a supported Linux Runner.
      Team data lives separately from this installation in the OS user configuration directory.
    EOS
  end

  test do
    assert_match "camp v#{version}", shell_output("#{bin}/camp version")
  end
end
`;
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
  const directory = join(root, 'dist/release');
  const descriptor = JSON.parse(
    await readFile(join(directory, 'server-release.json'), 'utf8'),
  );
  await writeFile(join(directory, 'camp.rb'), homebrewFormula(descriptor));
}
