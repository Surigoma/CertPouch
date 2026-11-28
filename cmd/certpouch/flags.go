package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
)

// parseFlags parses args and reports whether the command should continue.
// Requested help (-h, --help) goes to stdout with exit code 0; parse errors
// and their usage text go to stderr with exit code 2. Later calls to
// flags.Usage, such as for invalid values, write to stderr.
func parseFlags(flags *flag.FlagSet, args []string, stdout, stderr io.Writer) (code int, ok bool) {
	var output bytes.Buffer
	flags.SetOutput(&output)
	err := flags.Parse(args)
	flags.SetOutput(stderr)
	switch {
	case err == nil:
		return 0, true
	case errors.Is(err, flag.ErrHelp):
		_, _ = stdout.Write(output.Bytes())
		return 0, false
	default:
		_, _ = stderr.Write(output.Bytes())
		return 2, false
	}
}

func isHelp(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "-help"
}
