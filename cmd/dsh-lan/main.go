// Command dsh-lan gives a loopback-only "dsh web" a network face.
package main

import (
	"os"

	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
