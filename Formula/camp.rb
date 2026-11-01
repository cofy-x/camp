class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.8"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.8/camp-server-darwin-arm64.tar.gz"
      sha256 "7fd30b561315eb853034da9635a159a524637624ad2d2098be340fb5dd7b9386"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.8/camp-server-darwin-amd64.tar.gz"
      sha256 "6b0cb680f3ed5efc1eb8c87606c02cdf4830cc7820bde6911840097c2af45ec5"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.8/camp-server-linux-amd64.tar.gz"
      sha256 "ab27c711539829b7e21ba7d3e0836c6968e13930beda22b41a2061b2e52f2028"
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
      No service starts automatically. Connect a Runner with camp runner setup.
      Native execution supports Apple Silicon macOS 15+ and Ubuntu 24.04 amd64.
      Team data lives separately from this installation in the OS user configuration directory.
    EOS
  end

  test do
    assert_match "camp v#{version}", shell_output("#{bin}/camp version")
  end
end
