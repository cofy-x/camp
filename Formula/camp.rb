class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.14"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.14/camp-server-darwin-arm64.tar.gz"
      sha256 "c6cfad741e70a651879ce183bd699ec591ecda31d119b3fbdcec1921291c32da"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.14/camp-server-darwin-amd64.tar.gz"
      sha256 "38c9cf2e6ac658ed6a237e2f758cdc1e248c84067b0ff84d5e782f7aed13b868"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.14/camp-server-linux-amd64.tar.gz"
      sha256 "87535aa89357b0b3d43bd802138f9a0822e39fe427bd9f747b2ddb1d8866486e"
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
