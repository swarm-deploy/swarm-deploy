package git

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const proposalBranchSuffixBytes = 5

var invalidBranchPart = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// NewProposalBranch creates a readable, unique name for a GitOps proposal branch.
func NewProposalBranch(parts ...string) (string, error) {
	return newProposalBranch(rand.Reader, parts...)
}

func newProposalBranch(random io.Reader, parts ...string) (string, error) {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(invalidBranchPart.ReplaceAllString(part, "-"), ".-")
		for strings.Contains(part, "..") {
			part = strings.ReplaceAll(part, "..", ".")
		}
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("proposal branch requires at least one non-empty part")
	}

	randomBytes := make([]byte, proposalBranchSuffixBytes)
	if _, err := io.ReadFull(random, randomBytes); err != nil {
		return "", fmt.Errorf("generate proposal branch suffix: %w", err)
	}
	suffix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randomBytes))
	return strings.Join(clean, "-") + "-" + suffix, nil
}
