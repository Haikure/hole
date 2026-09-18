// hole-desktop-core is the private stdio host for a desktop GUI. It does not
// read CLI configuration, open a control TCP port, or import the mobile facade.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"hole/desktop"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, input io.ReadCloser, output io.WriteCloser, diagnostics io.Writer) int {
	if len(args) != 0 {
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			fmt.Fprintln(diagnostics, "用法：hole-desktop-core\n通过 stdin/stdout 收发逐行 JSON；协议见 desktop/README.md。版本通过 hello 方法查询。")
			return 0
		}
		fmt.Fprintln(diagnostics, "桌面桥接不接受运行参数；使用 --help 查看说明")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Treat a closed stdout pipe as an I/O error so Core gets its normal cleanup
	// path instead of the process terminating immediately on POSIX SIGPIPE.
	signal.Ignore(syscall.SIGPIPE)
	reader := processReader{processIO: newProcessIO(), source: input}
	writer := processWriter{processIO: newProcessIO(), target: output}
	// NotifyContext may carry a signal-specific Cause rather than the plain
	// context.Canceled sentinel. A requested process shutdown still exits cleanly.
	if err := desktop.Serve(ctx, reader, writer); err != nil && ctx.Err() == nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	return 0
}
