{
  description = "gitea-cli - A command line tool to interact with Gitea";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          name = "gitea-cli-dev-environment";
          buildInputs = with pkgs; [
            # flake.lock may pin a nixpkgs that predates Go 1.27; go.mod's toolchain line then fetches it.
            (pkgs.go_1_27 or pkgs.go)
            gopls
            gnumake
            # Add other dependencies here if needed
          ];

          shellHook = ''
            echo 'Welcome to gt. Check out the Makefile for runnable targets.'
          '';
        };
      }
    );
} 