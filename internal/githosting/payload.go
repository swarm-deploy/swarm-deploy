package githosting

import (
	"errors"
	"time"
)

var (
	ErrReleaseNotFound = errors.New("release not found")
)

type Release struct {
	Tag         string
	Commit      string
	Body        string
	URL         string
	PublishedAt time.Time
}

type RepositoryReference struct {
	Owner string
	Name  string
}
