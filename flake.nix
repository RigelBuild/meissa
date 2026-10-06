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
    in
    {
      packages = forAllSystems packagesFor;
      devenvModules.default = import ./devenv-module.nix {
        toolchain = self.packages;
        runtimePkgs = nixpkgs.legacyPackages;
      };
    };
}
