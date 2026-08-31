class Cupthread < Formula
  desc "CupThread CLI — manage your CupThread projects from the command line"
  homepage "https://cupthread.com"
  url "https://github.com/CupThread/CupThreadAgenticCoding.git", branch: "main"
  version "0.2.0"
  license "MIT"
  head "https://github.com/CupThread/CupThreadAgenticCoding.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"cupthread"), "./cmd/cupthread"
  end

  test do
    assert_match "cupthread", shell_output("#{bin}/cupthread --version")
  end
end
