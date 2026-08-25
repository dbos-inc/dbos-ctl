{
  description = "dbosctl — a command-line client for the DBOS Conductor API";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # Non-git sources report the Unix epoch; omit the date rather than stamp 1970-01-01.
      lastModified = builtins.substring 0 8 (self.lastModifiedDate or "19700101");
      isoDate =
        if lastModified == "19700101" then
          null
        else
          builtins.substring 0 4 lastModified
          + "-"
          + builtins.substring 4 2 lastModified
          + "-"
          + builtins.substring 6 2 lastModified;

      # rev exists only for a clean tree; dirtyRev carries a -dirty suffix.
      commit = self.rev or self.dirtyRev or null;

      mkDbosctl =
        pkgs:
        # Go pinned to go.mod's directive (bump together) so gofmt matches CI's.
        (pkgs.buildGoModule.override { go = pkgs.go_1_25; }) {
          pname = "dbosctl";
          # A flake build sees no git tags, so the ldflags stamp only commit and
          # date and keep the "dev" version sentinel (AGENTS.md, Versioning &
          # release).
          version = "0-unstable" + nixpkgs.lib.optionalString (isoDate != null) "-${isoDate}";
          src = self;

          subPackages = [ "cmd/dbosctl" ];
          # Pins the Go dependency closure; refresh on any go.sum change by
          # setting it to nixpkgs.lib.fakeHash and copying the hash from the
          # build error. CI's nix job catches a stale one.
          vendorHash = "sha256-YpwSIgjNGea9oHxHGswThDTg/w87Aj13GJKWep6hBzs=";

          env.CGO_ENABLED = 0;

          ldflags = [
            "-s"
            "-w"
          ]
          ++ nixpkgs.lib.optional (isoDate != null) "-X github.com/dbos-inc/dbos-ctl/internal/cli.date=${isoDate}"
          ++ nixpkgs.lib.optional (commit != null) "-X github.com/dbos-inc/dbos-ctl/internal/cli.commit=${commit}";

          nativeBuildInputs = [ pkgs.installShellFiles ];

          postInstall = nixpkgs.lib.optionalString (pkgs.stdenv.buildPlatform.canExecute pkgs.stdenv.hostPlatform) ''
            installShellCompletion --cmd dbosctl \
              --bash <($out/bin/dbosctl completion bash) \
              --zsh <($out/bin/dbosctl completion zsh) \
              --fish <($out/bin/dbosctl completion fish)
          '';

          meta = {
            description = "Command-line client for the DBOS Conductor API";
            homepage = "https://github.com/dbos-inc/dbos-ctl";
            license = nixpkgs.lib.licenses.mit;
            mainProgram = "dbosctl";
          };
        };
    in
    {
      overlays.default = final: _prev: { dbosctl = mkDbosctl final; };

      packages = forAllSystems (pkgs: rec {
        dbosctl = mkDbosctl pkgs;
        default = dbosctl;
      });

      checks = forAllSystems (pkgs: {
        inherit (self.packages.${pkgs.system}) dbosctl;
      });

      devShells = forAllSystems (pkgs: {
        # gotestsum and oapi-codegen come from go.mod via `go tool`.
        default = pkgs.mkShell {
          packages = with pkgs; [
            # Same pin as the package build.
            go_1_25
            goreleaser
            jq
          ];
        };
      });
    };
}
