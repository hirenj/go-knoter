class Knoter < Formula
  desc "Upload HTML reports to Microsoft OneNote"
  homepage "https://github.com/hirenj/go-knoter"
  license "MIT"

  head "https://github.com/hirenj/go-knoter.git", branch: "main"

  stable do
    url "https://github.com/hirenj/go-knoter/archive/refs/tags/v0.1.0.tar.gz"
    sha256 "PLACEHOLDER"
  end

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w", output: bin/"knoter"), "./cmd/knoter"
    system "go", "build", *std_go_args(ldflags: "-s -w", output: bin/"knoter-auth"), "./cmd/knoter-auth"
  end

  test do
    assert_match "upload HTML", shell_output("#{bin}/knoter help")
    system "#{bin}/knoter-auth", "--help"
  end
end
