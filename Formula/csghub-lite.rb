class CsghubLite < Formula
  desc "Lightweight tool for running LLMs locally with CSGHub platform"
  homepage "https://github.com/opencsgs/csglite"
  version "0.9.15"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-arm64.tar.gz"
      sha256 "ef4e998ae56f5d94b41180717ce3cdef4cc0d3c9c666f268278dc25ab012368b"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-amd64.tar.gz"
      sha256 "df32ffd55c0e1655c6ca9b642f286a3ab22de1c6bafc0732fdd319e2ff40d380"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-arm64.tar.gz"
      sha256 "412df64839291a3a95dce27e7485c839ac339269c756f4853c6554bfd1876128"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-amd64.tar.gz"
      sha256 "42f933989352e5e51fa6e1f91d95a835e01b4ca392cb73307ae08aef2ef5e160"
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
