package main

import (
	sdk "github.com/dusthoff/hashpoint/plugin/sdk"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/plugin"
)

func main() {
	sdk.Serve(plugin.New())
}
