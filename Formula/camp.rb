class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.7"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.7/camp-server-darwin-arm64.tar.gz"
      sha256 "1bd1cd457440807748393251ebe00c7e3807dbd30a613d674a4b6338a233984c"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.7/camp-server-darwin-amd64.tar.gz"
      sha256 "6de37b5d2247cebc17345fbedc674598af60b850f864c70a2395de5979a42d3a"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.7/camp-server-linux-amd64.tar.gz"
      sha256 "e50b52191b7bb92be15889c9981ca1c30973b6e6a07eb30c9225d483032010b0"
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
