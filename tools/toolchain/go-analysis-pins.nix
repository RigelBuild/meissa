# Pins consumed by tools/toolchain/go-analysis.nix. The pinned sources supply Go
# language/IR compatibility that the nixpkgs versions do not yet provide.
{
  nilaway = {
    version = "0-unstable-2026-09-18";
    owner = "uber-go";
    repo = "nilaway";
    rev = "acb8859b9031bb9496be97e027df5573f9fb5340";
    hash = "sha256-GvDZ5tlvOrTI93tYcIcLd45ZHdqwFopVtoBffD/kbuM=";
    vendorHash = "sha256-qVmvDneq6V/q5UHZ/Cjjqd5/XPPNfvVGoxwg9nz4/Ds=";
  };
  golangci-lint = {
    version = "2.14.0";
    owner = "golangci";
    repo = "golangci-lint";
    tag = "v2.14.0";
    hash = "sha256-HATA7JKHwEouM+8jYZbQrkX7p4gut4IpyTvcBexu/4o=";
    vendorHash = "sha256-ekP/zDhYMpMG+tYAyYHNfLOCt/JkxrXUw1jHUEfsM8k=";
  };
}
