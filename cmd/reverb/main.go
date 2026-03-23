package main

import (
	"fmt"
	"os"
)

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func main() {
	if len(os.Args) < 3 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] + " " + os.Args[2] {
	case "gen types":
		runGenTypes(os.Args[3:])
	case "clean deprecated":
		runCleanDeprecated(os.Args[3:])
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reverb gen types [--db <dsn>] [--driver <sqlite|postgres|mysql>] [--out <file>] [--exclude-admin]")
	fmt.Fprintln(os.Stderr, "  reverb clean deprecated [--db <dsn>] [--driver <sqlite|postgres|mysql>]")
}
