<<<<<<< conflict 1 of 1
%%%%%%% diff from: mlzomnyq e661437d "fix(go): resolve M13 lint findings (RIG-8132)" (rebase destination)
\\\\\\\        to: uzmmuouy 4b30872a "feat(go): npm publish tool in Go (RIG-8133)" (parents of rebased revision)
+// SPDX-License-Identifier: MIT OR Apache-2.0
+
+// Package main implements the npm-publish executable.
+package main
+
+import (
+	"context"
+	"os"
+
+	"github.com/RigelBuild/meissa/go/internal/npmpublish"
+)
+
+func main() {
+	os.Exit(npmpublish.Main(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
+}
+++++++ ssoulylw 0beea2e2 (rebased revision)
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the npm-publish executable.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/RigelBuild/meissa/go/internal/npmpublish"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return npmpublish.Main(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
}
>>>>>>> conflict 1 of 1 ends
