{
  description = "Shared markdown policy and toolchain for RigelBuild repositories";

  inputs.nixpkgs.url = "github:cachix/devenv-nixpkgs/rolling";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f:
        builtins.listToAttrs (map (system: {
          name = system;
          value = f system;
        }) systems);
      packagesFor = system:
        let pkgs = nixpkgs.legacyPackages.${system};
        in {
          rumdl = pkgs.rumdl;
          biome = pkgs.biome;
          "rumdl-base-config" = import ./rumdl-base-config.nix { inherit pkgs; };
        };
      moduleCheck = system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          packageOutputs = self.packages.${system};
          module = self.devenvModules.default { inherit pkgs; };
          expectedPackages = [ packageOutputs.rumdl packageOutputs.biome ];
          hasExpectedPackages =
            builtins.length module.packages == builtins.length expectedPackages
            && builtins.all (package: builtins.elem package module.packages) expectedPackages;
        in
        if !hasExpectedPackages then
          throw "devenvModules.default must add only the exported rumdl and biome packages"
        else
          pkgs.runCommand "meissa-devenv-module-check" { } ''
            test -f "${module.env.RUMDL_BASE_CONFIG}"
            touch "$out"
          '';
      goAnalysisCheck = system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          goAnalysis = self.lib.goAnalysis { inherit pkgs; goToolchain = pkgs.go; };
          evaluated = pkgs.lib.evalModules {
            modules = [
              ({ lib, ... }: {
                options.packages = lib.mkOption {
                  type = lib.types.listOf lib.types.package;
                  default = [ ];
                };
              })
              ({ ... }: { _module.args.pkgs = pkgs; })
              self.devenvModules.go
              ({ ... }: { meissa.go.toolchain = pkgs.go; packages = [ pkgs.hello ]; })
            ];
          };
          packages = evaluated.config.packages;
          expectedPackages = [ pkgs.hello ] ++ builtins.attrValues goAnalysis;
          hasExpectedPackages = builtins.length packages == 5
            && builtins.all (package: builtins.elem package packages) expectedPackages
            && builtins.all (tool: builtins.elem tool packages) (builtins.attrValues goAnalysis);
        in
        if !hasExpectedPackages then
          throw "devenvModules.go must add four analysis tools to configured packages"
        else
          pkgs.runCommand "meissa-go-analysis-check" {
            nativeBuildInputs = packages;
          } ''
            command -v hello golangci-lint govulncheck go-licenses nilaway
            touch "$out"
          '';
    in
    {
      packages = forAllSystems packagesFor;
      devenvModules.default = import ./devenv-module.nix {
        toolchain = self.packages;
      };
      devenvModules.go = import ./devenv-module-go.nix;
      lib.goAnalysis = { pkgs, goToolchain }: import ./tools/toolchain/go-analysis.nix { inherit pkgs goToolchain; };
      checks = forAllSystems (system: {
        devenv-module = moduleCheck system;
      } // (if system == "x86_64-linux" then {
        go-analysis = goAnalysisCheck system;
      } else { }));
    };
}
