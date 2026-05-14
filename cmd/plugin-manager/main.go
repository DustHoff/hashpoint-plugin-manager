package main

import (
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/plugin"
)

func main() {
	// The host (Hashpoint) drives our lifecycle through go-plugin's stdio
	// protocol: it closes the pipes (sdk.Serve returns) or calls
	// os.Process.Kill / TerminateProcess. OS signals are not part of that
	// contract. On Windows the plugin subprocess inherits the host's
	// console group, so any CTRL_C / CTRL_BREAK broadcast there would
	// otherwise kill us with STATUS_CONTROL_C_EXIT (0xC000013A) mid-flight.
	// Release builds are linked with -H=windowsgui (no console attached),
	// which is the primary defence; this Ignore is the safety net for
	// any path that still delivers a soft signal.
	signal.Ignore(os.Interrupt, syscall.SIGTERM)

	sdk.Serve(plugin.New())
}
