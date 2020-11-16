package main

import (
	"flag"
	"os"

	"github.com/iDigitalFlame/watcher"
)

const usage = `Twitter Watcher Telegram Bot
iDigitalFlame 2020 (idigitalflame.com)

Usage:
  -h              Print this help menu.
  -f <file>       Configuration file path.
  -d              Dump the default configuration and exit.
  -c              Clear the database of ALL DATA before starting up.
`

func main() {
	var (
		args        = flag.NewFlagSet("Twitter Watcher Telegram Bot", flag.ExitOnError)
		file        string
		dump, empty bool
	)
	args.Usage = func() {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}
	args.StringVar(&file, "f", "", "Configuration file path.")
	args.BoolVar(&dump, "d", false, "Dump the default configuration and exit.")
	args.BoolVar(&empty, "c", false, "Clear the database of ALL DATA before starting up.")

	if err := args.Parse(os.Args[1:]); err != nil {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}

	if len(file) == 0 && !dump {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}

	if dump {
		os.Stdout.WriteString(watcher.Defaults)
		os.Exit(0)
	}

	w, err := watcher.New(file, empty)
	if err != nil {
		os.Stdout.WriteString("Error: " + err.Error() + "!\n")
		os.Exit(1)
	}

	if err := w.Run(); err != nil {
		os.Stdout.WriteString("Error: " + err.Error() + "!\n")
		os.Exit(1)
	}
}
