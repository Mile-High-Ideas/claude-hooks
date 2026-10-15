package main

import (
	"bufio"
	"os"
	"strings"
)

// commitHeader returns the first line of the message given to git commit
// by -m/--message or -F/--file, and whether one was found. A message read
// from stdin (-F -) or written in an editor is not visible here.
func commitHeader(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() (string, bool) {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		}
		switch {
		case arg == "-m" || arg == "--message":
			if v, ok := next(); ok {
				return firstLine(v), true
			}
			return "", false
		case strings.HasPrefix(arg, "--message="):
			return firstLine(strings.TrimPrefix(arg, "--message=")), true
		case strings.HasPrefix(arg, "-m") && len(arg) > 2:
			return firstLine(arg[2:]), true
		case arg == "-F" || arg == "--file":
			if v, ok := next(); ok && v != "-" {
				return fileFirstLine(v)
			}
			return "", false
		case strings.HasPrefix(arg, "--file="):
			return fileFirstLine(strings.TrimPrefix(arg, "--file="))
		case strings.HasPrefix(arg, "-F") && len(arg) > 2:
			return fileFirstLine(arg[2:])
		}
	}
	return "", false
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func fileFirstLine(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		return sc.Text(), true
	}
	return "", true
}
