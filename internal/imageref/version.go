package imageref

import (
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

const digestPrefixLen = 12

// Version returns a display version for an OCI/Docker image reference (tag or short digest).
func Version(image string) string {
	s := strings.TrimSpace(image)
	if s == "" {
		return ""
	}

	named, err := reference.ParseNormalizedNamed(s)
	if err != nil {
		return versionFallback(s)
	}

	if canonical, ok := named.(reference.Canonical); ok {
		return shortDigest(canonical.Digest())
	}

	if tagged, ok := named.(reference.NamedTagged); ok {
		return tagged.Tag()
	}

	only := reference.TagNameOnly(named)
	if tagged, ok := only.(reference.NamedTagged); ok {
		return tagged.Tag()
	}

	return "latest"
}

func shortDigest(d digest.Digest) string {
	enc := d.Encoded()
	if len(enc) > digestPrefixLen {
		enc = enc[:digestPrefixLen]
	}
	return d.Algorithm().String() + ":" + enc
}

func versionFallback(s string) string {
	at := strings.LastIndex(s, "@")
	if at >= 0 && at+1 < len(s) {
		rest := s[at+1:]
		if d, err := digest.Parse(rest); err == nil {
			return shortDigest(d)
		}
	}

	colon := strings.LastIndex(s, ":")
	if colon < 0 || colon == len(s)-1 {
		return "latest"
	}
	candidate := s[colon+1:]
	if strings.ContainsAny(candidate, "/") {
		return "latest"
	}
	return candidate
}
