class Knoter < Formula
  desc "Upload HTML reports to Microsoft OneNote"
  homepage "https://github.com/hirenj/go-knoter"
  license "MIT"
  version "0.1.0"

  on_macos do
    on_arm do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-darwin-arm64.tar.gz"
      sha256 "PLACEHOLDER_darwin_arm64"
    end
    on_intel do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-darwin-amd64.tar.gz"
      sha256 "PLACEHOLDER_darwin_amd64"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-linux-arm64.tar.gz"
      sha256 "PLACEHOLDER_linux_arm64"
    end
    on_intel do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-linux-amd64.tar.gz"
      sha256 "PLACEHOLDER_linux_amd64"
    end
  end

  def install
    bin.install "knoter"
    bin.install "knoter-auth"
  end

  test do
    assert_match "upload HTML", shell_output("#{bin}/knoter help")
    assert_match "knoter-auth", shell_output("#{bin}/knoter-auth --help 2>&1; true")
  end
end
