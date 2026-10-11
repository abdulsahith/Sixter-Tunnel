// Package prompt provides small interactive CLI helpers — a hidden secret
// prompt (like ssh/git asking for a password) and a plain line prompt.
package prompt

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// Secret reads a line WITHOUT echoing it (for tokens/passwords). If stdin isn't
// a terminal (piped/non-interactive), it reads a normal line instead.
func Secret(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text()), nil
	}
	return "", sc.Err()
}

// Line reads a visible line with a label and an optional default (shown in []).
func Line(label, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		v := strings.TrimSpace(sc.Text())
		if v == "" {
			return def
		}
		return v
	}
	return def
}

// IsTTY reports whether stdin is an interactive terminal.
func IsTTY() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
