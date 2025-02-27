{
  description = "Tea - A command line tool to interact with Gitea";

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
          name = "tea-dev-environment";
          buildInputs = with pkgs; [
            go_1_24
            gopls
            gnumake
            # Add other dependencies here if needed
          ];

          shellHook = ''
            echo 'Welcome to tea. Check out the Makefile for runnable targets.'
          '';
        };
      }
    );
} 