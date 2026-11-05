class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.11"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.11/camp-server-darwin-arm64.tar.gz"
      sha256 "7de77488654957eecd78720a0729f192cab988f1654ca0c850b93f621b6b97e8"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.11/camp-server-darwin-amd64.tar.gz"
      sha256 "1ed6865fb3621e9eabe339051731799654019963abe164461dbc92f23218de7f"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.11/camp-server-linux-amd64.tar.gz"
      sha256 "7e3b2a0ac5f29a59e797881abfc69a2a92592ab4162ad20603d700c4d8540f84"
    end
  end

  depends_on "git"

  def install
    if OS.mac?
      libexec.install "camp"
      bin.install_symlink libexec/"camp"
    else
      bin.install "camp"
    end
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
