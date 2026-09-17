# typed: strict
# frozen_string_literal: true

# Homebrew formula.
#
# Builds from the repository rather than from a release tarball, because
# there is no tagged release: the CLI and the JSON still change, and a
# version number would imply otherwise.
#
# Install it from a clone:
#
#   brew install --HEAD --build-from-source packaging/homebrew/tokenamun.rb
#
# Or, once this file is in a tap (Formula/tokenamun.rb in ctford/homebrew-tap):
#
#   brew install --HEAD ctford/tap/tokenamun
class Tokenamun < Formula
  desc "Cache-aware token profiler for Claude Code and Entire"
  homepage "https://github.com/ctford/tokenamun"
  license "MIT"
  head "https://github.com/ctford/tokenamun.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/tokenamun"
  end

  test do
    assert_match "tokenamun", shell_output("#{bin}/tokenamun version")
    # doctor exits non-zero where there is nothing to read, which is the
    # normal state of a sandbox, so only that it runs is asserted.
    system bin/"tokenamun", "--help"
  end
end
