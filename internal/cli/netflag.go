package cli

import "strings"

func Parse(args []string) (cleaned []string, preferredNICs []string, debug bool, version bool, requireHTTPS bool, warnings []string) {
	cleaned = make([]string, 0, len(args))
	preferredNICs = make([]string, 0)

	appendNICs := func(val string) {
		for _, part := range strings.Split(val, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				preferredNICs = append(preferredNICs, part)
			}
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--version" || arg == "-v" {
			version = true
			continue
		}

		if arg == "--debug" {
			debug = true
			continue
		}

		if arg == "--require-https" {
			requireHTTPS = true
			continue
		}

		if arg == "--net" {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				warnings = append(warnings, "--net missing value, ignored")
				continue
			}
			val := args[i+1]
			i++
			appendNICs(val)
			continue
		}

		cleaned = append(cleaned, arg)
	}

	return cleaned, preferredNICs, debug, version, requireHTTPS, warnings
}
