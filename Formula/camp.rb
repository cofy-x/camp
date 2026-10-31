class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.4"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.4/camp-server-darwin-arm64.tar.gz"
      sha256 "24dcd21ed3f8174a159a61983c0a7dfb949b5fae0a0656318660218de2bbb650"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.4/camp-server-darwin-amd64.tar.gz"
      sha256 "91be2d30d92eed0ff403333eab8172e8b620947e198f55d6d5b6a15c953ff4de"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.4/camp-server-linux-amd64.tar.gz"
      sha256 "b5d300846605e8b9045264f68a9dbea9027dcc03c2e3edfa9615a45abfe1fb22"
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
