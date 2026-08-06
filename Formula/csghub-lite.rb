class CsghubLite < Formula
  desc "Lightweight tool for running LLMs locally with CSGHub platform"
  homepage "https://github.com/opencsgs/csglite"
  version "0.9.35"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-arm64.tar.gz"
      sha256 "24c1c7170f4112ca80dbcef956b5f5854a415fa815538591080c040a237faa1d"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-amd64.tar.gz"
      sha256 "5de7b24ef83b68596877a61a6e336bb4808eef416940a8efb378982e5a1b78c2"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-arm64.tar.gz"
      sha256 "594ecc279660c31052df7542d9d05218447c0fa0d05de1584597a94b688bb29f"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-amd64.tar.gz"
      sha256 "378ab6589a50a87364d64862f554f0c97afc5a4dda6605e021b404b3c696bd87"
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
