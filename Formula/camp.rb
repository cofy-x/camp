class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.17"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.17/camp-server-darwin-arm64.tar.gz"
      sha256 "e89da95da903ee07337a6c7cbbea5c18c94a202a132dc54c0a2273d35fb5757a"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.17/camp-server-darwin-amd64.tar.gz"
      sha256 "8b8ca55b1d078315c55e2c4ef97f91c34be18eb598b73c108d7037d301f62d32"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.17/camp-server-linux-amd64.tar.gz"
      sha256 "dc9440ce69e65328edeb98e89c993c9294dade9135e4d4df8271cdff43bbe309"
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
