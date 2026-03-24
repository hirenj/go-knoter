class Knoter < Formula
  desc "Upload HTML reports to Microsoft OneNote"
  homepage "https://github.com/hirenj/go-knoter"
  license "MIT"

  head "https://github.com/hirenj/go-knoter.git", branch: "main"

  stable do
    url "https://github.com/hirenj/go-knoter/archive/refs/tags/v0.0.4.tar.gz"
    sha256 "82707cbf15627d006cba84327e6a571b7840e5b5d99b4d8be381545c577e13cc"
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
