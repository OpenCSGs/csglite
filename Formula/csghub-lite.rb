class CsghubLite < Formula
  desc "Lightweight tool for running LLMs locally with CSGHub platform"
  homepage "https://github.com/opencsgs/csglite"
  version "0.9.17"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-arm64.tar.gz"
      sha256 "5c8aeb76f61e48fb8325faae569499edf644c4d6844ac53ddf536d4162755532"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-amd64.tar.gz"
      sha256 "1e5511adf7017d6d79fb4452bda22edf1dd41561a5d0c68c7a50ce0bea564ae7"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-arm64.tar.gz"
      sha256 "d061d8cbac6823e8c37bdd50de81025c58d4ef527317a6af43f42279ba8d5221"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-amd64.tar.gz"
      sha256 "fd456c22e3cf46ad63a09d69e82f71d54ddd8669f6947eaa3eee736be0802047"
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
