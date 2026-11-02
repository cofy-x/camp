class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.10"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.10/camp-server-darwin-arm64.tar.gz"
      sha256 "8ae5d0e3c9fbcfe96dd977e3551cd8943262a02a7e681437e987eb9f99a7223c"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.10/camp-server-darwin-amd64.tar.gz"
      sha256 "18720f99bf013d981ca8518b9e833d4d853804912b707d17d86448fc7f63fdd1"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.10/camp-server-linux-amd64.tar.gz"
      sha256 "dcdd477d3186698f0afae0ffcde0719df14fbde54dfc54e7fa8332fb46f4092e"
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
