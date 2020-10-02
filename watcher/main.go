package main

import (
	"flag"
	"os"

	"github.com/iDigitalFlame/watcher"
)

func main() {
	c, err := watcher.Cmdline()
	if c == nil && err == nil {
		os.Exit(0)
	}

	if err == flag.ErrHelp {
		os.Exit(2)
	}

	w, err := watcher.New(*c)
	if err != nil {
		os.Stderr.WriteString("Error: " + err.Error() + "!\n")
		os.Exit(1)
	}

	if err := w.Run(); err != nil {
		os.Stderr.WriteString("Error: " + err.Error() + "!\n")
		os.Exit(1)
	}
}
