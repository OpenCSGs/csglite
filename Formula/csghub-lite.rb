class CsghubLite < Formula
  desc "Lightweight tool for running LLMs locally with CSGHub platform"
  homepage "https://github.com/opencsgs/csghub-lite"
  version "0.8.92"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/OpenCSGs/csghub-lite/releases/download/v#{version}/csghub-lite_#{version}_darwin-arm64.tar.gz"
      sha256 "df994ae0d5da77e4aea47c71bf3e71aef5d3e63c19cf7faba86f50b131159704"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csghub-lite/releases/download/v#{version}/csghub-lite_#{version}_darwin-amd64.tar.gz"
      sha256 "bba817f1a81cd0d904a5e3f26899c807ff93ab6e88e80aaeac2e99646c2350d5"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/OpenCSGs/csghub-lite/releases/download/v#{version}/csghub-lite_#{version}_linux-arm64.tar.gz"
      sha256 "2c08f8e6623d7114ec7bb37cec388b3028d00a53a42e4274454f4c524bcb88c6"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csghub-lite/releases/download/v#{version}/csghub-lite_#{version}_linux-amd64.tar.gz"
      sha256 "1ecad127e1218cd60908543705263195672418a194a6321cdad595d9c8b032f1"
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
