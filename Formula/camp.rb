class Camp < Formula
  desc "A shared browser workspace for people and agents"
  homepage "https://github.com/cofy-x/camp"
  version "0.1.0-alpha.12"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.12/camp-server-darwin-arm64.tar.gz"
      sha256 "ca45a5d750a1505995fe0e5202bf5d64873bb901c56bff6b1e437411707fa8c4"
    end
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.12/camp-server-darwin-amd64.tar.gz"
      sha256 "d73c4f36b9b48d6e73d68699e64b02391c27fbae30027ca997f11208beabe2be"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.12/camp-server-linux-amd64.tar.gz"
      sha256 "4964889765d03303ada43649ee394ac075120157088d61f6a59ea054fec19867"
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
