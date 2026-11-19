package main

import (
	"fmt"
	"strconv"
	"strings"
)

// args is a tiny flag parser: flags may appear anywhere, "--" ends flag parsing,
// both "--flag value" and "--flag=value" are accepted.
type args struct {
	pos    []string
	bools  map[string]bool
	values map[string][]string
}

var globalBools = map[string]string{
	"-p": "pretty", "--pretty": "pretty",
	"-v": "version", "--version": "version",
	"-h": "help", "--help": "help",
}

func parseArgs(raw []string, boolFlags, valueFlags []string) (args, error) {
	a := args{bools: map[string]bool{}, values: map[string][]string{}}
	isBool := map[string]bool{}
	for _, f := range boolFlags {
		isBool[f] = true
	}
	isValue := map[string]bool{}
	for _, f := range valueFlags {
		isValue[f] = true
	}
	for i := 0; i < len(raw); i++ {
		s := raw[i]
		if s == "--" {
			a.pos = append(a.pos, raw[i+1:]...)
			break
		}
		if g, ok := globalBools[s]; ok {
			a.bools[g] = true
			continue
		}
		if !strings.HasPrefix(s, "--") || len(s) == 2 {
			a.pos = append(a.pos, s)
			continue
		}
		name, val, hasVal := strings.Cut(s[2:], "=")
		switch {
		case isBool[name]:
			if hasVal {
				return a, fmt.Errorf("flag --%s takes no value", name)
			}
			a.bools[name] = true
		case isValue[name]:
			if !hasVal {
				if i+1 >= len(raw) {
					return a, fmt.Errorf("flag --%s needs a value", name)
				}
				i++
				val = raw[i]
			}
			a.values[name] = append(a.values[name], val)
		default:
			return a, fmt.Errorf("unknown flag %s", s)
		}
	}
	return a, nil
}

func (a args) value(name, def string) string {
	if v := a.values[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return def
}

func (a args) intValue(name string, def int) (int, error) {
	v := a.value(name, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("--%s: %q is not a number", name, v)
	}
	return n, nil
}

// want checks the number of positional arguments after the (sub)command words.
func (a args) want(n int, usage string) error {
	if len(a.pos) != n {
		return fmt.Errorf("usage: korb %s", usage)
	}
	return nil
}
