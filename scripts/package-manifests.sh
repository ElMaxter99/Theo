#!/usr/bin/env bash
# Writes a Scoop manifest and a Homebrew formula for a release from its
# checksums.txt: scripts/package-manifests.sh <tag> <checksums.txt> <out-dir>
# The release workflow runs it and attaches the two files to the release.
set -euo pipefail

tag=${1:?usage: package-manifests.sh <tag> <checksums.txt> <out-dir>}
sums=${2:?checksums.txt}
out=${3:?output directory}
version=${tag#v}
repo=${REPO:-ElMaxter99/Theo}
base="https://github.com/$repo/releases/download/$tag"

sum() { # sum <archive name> -> sha256
  local s
  s=$(awk -v f="$1" '$2 == f { print $1 }' "$sums")
  [ -n "$s" ] || { echo "no checksum for $1 in $sums" >&2; exit 1; }
  echo "$s"
}

mkdir -p "$out"

win_amd=$(sum "theo_${tag}_windows_amd64.zip")
win_arm=$(sum "theo_${tag}_windows_arm64.zip")
cat > "$out/theo.json" <<E2
{
  "version": "$version",
  "description": "Manage dev projects and isolated Docker Compose environments per Git worktree",
  "homepage": "https://github.com/$repo",
  "license": "MIT",
  "architecture": {
    "64bit": {
      "url": "$base/theo_${tag}_windows_amd64.zip",
      "hash": "$win_amd",
      "extract_dir": "theo_${tag}_windows_amd64"
    },
    "arm64": {
      "url": "$base/theo_${tag}_windows_arm64.zip",
      "hash": "$win_arm",
      "extract_dir": "theo_${tag}_windows_arm64"
    }
  },
  "bin": "theo.exe",
  "checkver": "github",
  "autoupdate": {
    "architecture": {
      "64bit": {
        "url": "https://github.com/$repo/releases/download/v\${version}/theo_v\${version}_windows_amd64.zip",
        "extract_dir": "theo_v\${version}_windows_amd64"
      },
      "arm64": {
        "url": "https://github.com/$repo/releases/download/v\${version}/theo_v\${version}_windows_arm64.zip",
        "extract_dir": "theo_v\${version}_windows_arm64"
      }
    }
  }
}
E2

lin_amd=$(sum "theo_${tag}_linux_amd64.tar.gz")
lin_arm=$(sum "theo_${tag}_linux_arm64.tar.gz")
mac_amd=$(sum "theo_${tag}_darwin_amd64.tar.gz")
mac_arm=$(sum "theo_${tag}_darwin_arm64.tar.gz")
cat > "$out/theo.rb" <<E2
class Theo < Formula
  desc "Manage dev projects and isolated Docker Compose environments per Git worktree"
  homepage "https://github.com/$repo"
  version "$version"
  license "MIT"

  on_macos do
    on_arm do
      url "$base/theo_${tag}_darwin_arm64.tar.gz"
      sha256 "$mac_arm"
    end
    on_intel do
      url "$base/theo_${tag}_darwin_amd64.tar.gz"
      sha256 "$mac_amd"
    end
  end

  on_linux do
    on_arm do
      url "$base/theo_${tag}_linux_arm64.tar.gz"
      sha256 "$lin_arm"
    end
    on_intel do
      url "$base/theo_${tag}_linux_amd64.tar.gz"
      sha256 "$lin_amd"
    end
  end

  def install
    bin.install "theo"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/theo version")
  end
end
E2
echo "wrote $out/theo.json and $out/theo.rb"
