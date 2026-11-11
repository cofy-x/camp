class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.13"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.13/camp-server-darwin-arm64.tar.gz"
      sha256 "6d71537aa6ecf9815e485c10aebea416dc2da99a4f28d6ab76bd3a8a6f0eafe1"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.13/camp-server-darwin-amd64.tar.gz"
      sha256 "36d3d5578e2931d8d6da1518ed518d54d1c9f4e2c4ccfb4102478507c81ae324"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.13/camp-server-linux-amd64.tar.gz"
      sha256 "9b859c4d1188f005411b54946a42e55d05ef8989f3ddf093e603d8d01ac2b6af"
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
