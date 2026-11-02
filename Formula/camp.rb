class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.9"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.9/camp-server-darwin-arm64.tar.gz"
      sha256 "abe0e5d8a731d08d5edde618980d414e4affc7f99fdda4ab98d35cbe218bdfc1"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.9/camp-server-darwin-amd64.tar.gz"
      sha256 "f627e0645099955946eb93a31d25cfe6ec1d9123ba100f8219cacdef966db91d"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.9/camp-server-linux-amd64.tar.gz"
      sha256 "3ca38c3e4c9c77f2dc716e3c43dfe326e8f27cf535ecfd161b448ee39f85f5b7"
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
