{
  description = "gbash development tools";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  nixConfig = {
    extra-substituters = [
      "s3://gbash-nix-cache?profile=r2&endpoint=aea0a2c4e5c5c74a3e84a12c855a7e37.r2.cloudflarestorage.com"
    ];
    extra-trusted-public-keys = [
      "gbash-cache-1:snMAp1ltEdxgvSRm9R5SCPk3Dpi/Za9WGPT8NJaEx7c="
    ];
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in {
      packages = forAllSystems (pkgs: {
        bash = pkgs.bash;
        bats = pkgs.bats;
        diffutils = pkgs.diffutils;

        # Full coreutils build tree including test suite (matches Dockerfile)
        coreutils-test-suite = pkgs.stdenv.mkDerivation rec {
          pname = "coreutils-test-suite";
          version = "9.10";

          src = pkgs.fetchurl {
            url = "https://ftp.gnu.org/gnu/coreutils/coreutils-${version}.tar.gz";
            sha256 = "e0bde1fb68509447fc723cf2517e8a8c7fa46769919bb7490ed350a2e9238562";
          };

          nativeBuildInputs = with pkgs; [
            perl
            gawk
          ];

          buildInputs = pkgs.lib.optionals pkgs.stdenv.isLinux (with pkgs; [
            acl
            libselinux
          ]);

          configureFlags = [
            "--disable-nls"
            "--disable-dependency-tracking"
          ];

          # Disable format-security warning treated as error (clang is stricter than GCC)
          env.NIX_CFLAGS_COMPILE = "-Wno-format-security";

          # Build but don't install - keep the full source tree with build artifacts
          buildPhase = ''
            make -j$NIX_BUILD_CORES
          '';

          # Copy entire build tree to output
          installPhase = ''
            mkdir -p $out
            cp -r . $out/
          '';

          # Skip fixup to preserve the build tree structure
          dontFixup = true;
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.bash
            pkgs.bats
            pkgs.diffutils
          ];
        };
      });
    };
}
