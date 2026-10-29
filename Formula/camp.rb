class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.2"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.2/camp-server-darwin-arm64.tar.gz"
      sha256 "6662c8866a182abb42f14c9de0c4db9dac8969725419984a77118d606109c825"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.2/camp-server-darwin-amd64.tar.gz"
      sha256 "41ac987e5dce4e2ebcc840acca7aa7b154a001b1cf45a67a85a29cc63d9dce5b"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.2/camp-server-linux-amd64.tar.gz"
      sha256 "a7079515a597e772eee928c5263bcaa37d34a8d86e92e61466a927b8a3600425"
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
