package dotenv

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Parse parses explicit KEY=VALUE pairs from an env file.
//
// Values are kept as-is: no interpolation, substitution, quote removal, or
// escaping is applied. Bare keys without "=" are rejected deliberately so
// repository content can never read values from the swarm-deploy process
// environment.
func Parse(content []byte) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	utf8BOM := []byte{0xEF, 0xBB, 0xBF}

	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		lineBytes := scanner.Bytes()
		if !utf8.Valid(lineBytes) {
			return nil, fmt.Errorf("invalid utf8 bytes at line %d", lineNumber)
		}

		if lineNumber == 1 {
			lineBytes = bytes.TrimPrefix(lineBytes, utf8BOM)
		}

		line := strings.TrimLeftFunc(string(lineBytes), unicode.IsSpace)
		if line == "" || line[0] == '#' {
			continue
		}

		key, value, hasValue := strings.Cut(line, "=")
		if key == "" {
			return nil, fmt.Errorf("no variable name at line %d", lineNumber)
		}
		if strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("variable %q contains whitespace at line %d", key, lineNumber)
		}

		if !hasValue {
			return nil, fmt.Errorf(
				"variable %q at line %d must have an explicit value",
				key,
				lineNumber,
			)
		}

		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}
