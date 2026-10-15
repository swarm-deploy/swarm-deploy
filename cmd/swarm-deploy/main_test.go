package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepositoryLogValueRemovesCredentialsFromMalformedURL(t *testing.T) {
	const secret = "top-secret-token"
	value := repositoryLogValue("https://robot:" + secret + "%zz@git.example.com/team/app.git")

	assert.NotContains(t, value, secret)
	assert.NotContains(t, value, "robot")
	assert.Regexp(t, `^repository-[0-9a-f]{16}$`, value)
}
