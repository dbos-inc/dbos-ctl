package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// isInteractive reports whether the CLI can prompt for an answer — i.e. stdin is
// a terminal. It's a package var so tests can force either path without a real
// TTY.
var isInteractive = func() bool { return isTerminal(os.Stdin) }

// isTerminal reports whether f is an interactive terminal (a character device).
// This is the no-dependency check; it treats pipes, files, and CI as
// non-interactive.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// confirm writes prompt to out and reads a yes/no answer from in. Only "y" or
// "yes" (case-insensitive) is a yes; anything else — including EOF — is no, so
// the safe default is not to proceed.
func confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
