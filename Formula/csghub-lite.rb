class CsghubLite < Formula
  desc "Lightweight tool for running LLMs locally with CSGHub platform"
  homepage "https://github.com/opencsgs/csglite"
  version "0.9.32"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-arm64.tar.gz"
      sha256 "0eb0258f20c4a29e7c6c6efb449885caf36666e42ac01ed6b39758744568d35e"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-amd64.tar.gz"
      sha256 "5535b28b9c55baec24c1f569098b0344f744cd646f39a5e2b9e36aa149f02271"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-arm64.tar.gz"
      sha256 "1e461a94f27cd9397a4dc3ab6cf5101cda163af89bef4c67cb165993bb8fed11"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-amd64.tar.gz"
      sha256 "6cd7a13bf3be125f20640fd13bd2e6f49de43d6a2d2534fcb9495e6cf4d6f1c0"
    end
  end

  depends_on "llama.cpp" => :recommended

  def install
    bin.install "csghub-lite"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/csghub-lite --version")
  end
end
