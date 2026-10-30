class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.3"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.3/camp-server-darwin-arm64.tar.gz"
      sha256 "880586d2ba5aa758409d5dab3bbd0cd227c251bc53040bfa334bf88683cda953"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.3/camp-server-darwin-amd64.tar.gz"
      sha256 "fc8eb73786263dbe7bd858b2ad98bebea0593327a14d8f65b447ab2498c9bc61"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.3/camp-server-linux-amd64.tar.gz"
      sha256 "6f46d09bddaa5edaa7426efeac73583aad7f0881f8a7ceae4780cfd7d89c644d"
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
