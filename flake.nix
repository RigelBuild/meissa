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
    in
    {
      packages = forAllSystems packagesFor;
      devenvModules.default = import ./devenv-module.nix {
        toolchain = self.packages;
      };
      checks = forAllSystems (system: {
        devenv-module = moduleCheck system;
      });
    };
}
