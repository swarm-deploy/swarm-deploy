package fs

import "path/filepath"

type PathInterpreter func(path string) string

func PathAsIs() PathInterpreter {
	return func(path string) string {
		return path
	}
}

func PrefixPath(forAbs, forRel string) PathInterpreter {
	return func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Join(forAbs, path)
		}

		return filepath.Join(forRel, path)
	}
}
