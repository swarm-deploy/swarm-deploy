// Package yamledit applies minimal-diff edits to YAML documents.
package yamledit

import (
	"bytes"
	"fmt"

	oneyaml "oneyaml.dev/go"
)

const maxInputBytes = 16 << 20

// Set replaces or adds the value at path without reformatting the rest of the document.
func Set(source []byte, path []string, value any) ([]byte, error) {
	// oneyaml does not interpret NEL (U+0085), line separator (U+2028) or paragraph
	// separator (U+2029) as YAML line breaks, although yaml.v3 does. We knowingly reject
	// those rare files instead of falling back to a full-document marshal.
	doc, err := oneyaml.Parse(source,
		oneyaml.Untrusted(),
		oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: maxInputBytes}),
	)
	if err != nil {
		return nil, fmt.Errorf("parse YAML for editing: %w", err)
	}
	pointer := oneyaml.Pointer(path...)
	if setErr := doc.Set(pointer, value); setErr != nil {
		return nil, fmt.Errorf("set YAML value at %q: %w", pointer, setErr)
	}
	return bytes.Clone(doc.Bytes()), nil
}
