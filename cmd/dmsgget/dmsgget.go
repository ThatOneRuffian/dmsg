package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/skycoin/dmsg/cmdutil"
	"github.com/skycoin/dmsg/dmsgget"
)

func main() {

	skStr := os.Getenv("DMSGGET_SK")

	dg := dmsgget.New(flag.CommandLine)
	flag.Parse()

	ctx, cancel := cmdutil.SignalContext(context.Background())
	defer cancel()

	if err := dg.Run(ctx, skStr, flag.Args()); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}
