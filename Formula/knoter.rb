class Knoter < Formula
  desc "Upload HTML reports to Microsoft OneNote"
  homepage "https://github.com/hirenj/go-knoter"
  license "MIT"
  version "0.1.0"

  on_macos do
    on_arm do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-darwin-arm64.tar.gz"
      sha256 "0400dd0004759043bb24e0a4b7ddf9b02c1c78f8471eae3b6b2ff23bc03d020b"
    end
    on_intel do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-darwin-amd64.tar.gz"
      sha256 "0400dd0004759043bb24e0a4b7ddf9b02c1c78f8471eae3b6b2ff23bc03d020b"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-linux-arm64.tar.gz"
      sha256 "0400dd0004759043bb24e0a4b7ddf9b02c1c78f8471eae3b6b2ff23bc03d020b"
    end
    on_intel do
      url "https://github.com/hirenj/go-knoter/releases/download/v#{version}/knoter-linux-amd64.tar.gz"
      sha256 "0400dd0004759043bb24e0a4b7ddf9b02c1c78f8471eae3b6b2ff23bc03d020b"
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
