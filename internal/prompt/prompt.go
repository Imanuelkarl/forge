// Package prompt implements tiny interactive terminal prompts with no dependencies.
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Prompter asks questions on stdin/stdout. With yes=true every question is answered
// with its default, which makes commands scriptable.
type Prompter struct {
	in  *bufio.Reader
	out io.Writer
	yes bool
}

// New returns a Prompter reading from stdin.
func New(yes bool) *Prompter {
	return &Prompter{in: bufio.NewReader(os.Stdin), out: os.Stdout, yes: yes}
}

// Interactive reports whether questions will actually be asked.
func (p *Prompter) Interactive() bool { return !p.yes }

func (p *Prompter) readLine() (string, bool) {
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// Input asks for free text.
func (p *Prompter) Input(label, def string) string {
	if p.yes {
		return def
	}
	if def != "" {
		fmt.Fprintf(p.out, "? %s (%s): ", label, def)
	} else {
		fmt.Fprintf(p.out, "? %s: ", label)
	}
	line, ok := p.readLine()
	if !ok || line == "" {
		return def
	}
	return line
}

// Confirm asks a yes/no question.
func (p *Prompter) Confirm(label string, def bool) bool {
	if p.yes {
		return def
	}
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	fmt.Fprintf(p.out, "? %s (%s): ", label, hint)
	line, ok := p.readLine()
	if !ok || line == "" {
		return def
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}

// Select asks the user to pick one of the options by number or name.
func (p *Prompter) Select(label string, options []string, def int) string {
	if p.yes {
		return options[def]
	}
	fmt.Fprintf(p.out, "? %s\n", label)
	for i, o := range options {
		marker := " "
		if i == def {
			marker = ">"
		}
		fmt.Fprintf(p.out, "  %s %d) %s\n", marker, i+1, o)
	}
	fmt.Fprintf(p.out, "  choose [1-%d] (default %d): ", len(options), def+1)

	line, ok := p.readLine()
	if !ok || line == "" {
		return options[def]
	}
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(options) {
		return options[n-1]
	}
	for _, o := range options {
		if strings.EqualFold(o, line) {
			return o
		}
	}
	return options[def]
}
