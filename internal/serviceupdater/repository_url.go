package serviceupdater

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const repositoryPathSegmentsCount = 2

type repositoryReference struct {
	webScheme string
	host      string
	owner     string
	name      string
}

func (r repositoryReference) repositoryPath() string {
	return fmt.Sprintf("%s/%s", r.owner, r.name)
}

func buildBranchURL(repositoryURL string, branch string) (string, error) {
	ref, err := parseRepositoryReference(repositoryURL)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"%s://%s/%s/tree/%s",
		ref.webScheme,
		ref.host,
		ref.repositoryPath(),
		url.PathEscape(strings.TrimSpace(branch)),
	), nil
}

func parseRepositoryReference(repositoryURL string) (repositoryReference, error) {
	raw := strings.TrimSpace(repositoryURL)
	if raw == "" {
		return repositoryReference{}, fmt.Errorf("repository url is required")
	}

	if strings.Contains(raw, "://") {
		return parseURLRepositoryReference(raw)
	}
	if strings.HasPrefix(raw, "git@") {
		return parseSCPRepositoryReference(raw)
	}

	return repositoryReference{}, fmt.Errorf("unsupported repository url format %q", repositoryURL)
}

func parseURLRepositoryReference(raw string) (repositoryReference, error) {
	parsedURL, err := url.Parse(raw)
	if err != nil {
		return repositoryReference{}, fmt.Errorf("parse repository url %q: %w", raw, err)
	}

	repositoryPath := strings.TrimPrefix(parsedURL.Path, "/")
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")

	segments := strings.Split(repositoryPath, "/")
	if len(segments) < repositoryPathSegmentsCount {
		return repositoryReference{}, fmt.Errorf("repository url %q must contain owner and repository", raw)
	}

	owner := segments[len(segments)-repositoryPathSegmentsCount]
	name := segments[len(segments)-1]
	if owner == "" || name == "" {
		return repositoryReference{}, fmt.Errorf("repository url %q must contain owner and repository", raw)
	}

	webScheme := parsedURL.Scheme
	if strings.EqualFold(parsedURL.Scheme, "ssh") {
		webScheme = "https"
	}

	return repositoryReference{
		webScheme: webScheme,
		host:      parsedURL.Hostname(),
		owner:     owner,
		name:      name,
	}, nil
}

func parseSCPRepositoryReference(raw string) (repositoryReference, error) {
	withoutUser := strings.TrimPrefix(raw, "git@")
	parts := strings.SplitN(withoutUser, ":", repositoryPathSegmentsCount)
	if len(parts) != repositoryPathSegmentsCount {
		return repositoryReference{}, fmt.Errorf("parse repository url %q", raw)
	}

	host := strings.TrimSpace(parts[0])
	repositoryPath := strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
	if host == "" || repositoryPath == "" {
		return repositoryReference{}, fmt.Errorf("parse repository url %q", raw)
	}

	owner := path.Dir(repositoryPath)
	name := path.Base(repositoryPath)
	if owner == "." || owner == "" || name == "." || name == "" {
		return repositoryReference{}, fmt.Errorf("repository url %q must contain owner and repository", raw)
	}

	return repositoryReference{
		webScheme: "https",
		host:      host,
		owner:     owner,
		name:      name,
	}, nil
}
