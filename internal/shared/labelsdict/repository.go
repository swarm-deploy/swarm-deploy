package labelsdict

const (
	SourceRepository = "org.swarm_deploy.source.repository"
	SourceProvider   = "org.swarm_deploy.source.provider"

	// Deprecated: use SourceRepository with SourceProvider instead.
	GitLabRepository = "org.swarm_deploy.gitlab_repository"
	// Deprecated: use SourceRepository with SourceProvider instead.
	GitHubRepository = "org.swarm_deploy.github_repository"
	// Deprecated: use SourceRepository with SourceProvider instead.
	BitbucketRepository = "org.swarm_deploy.bitbucket_repository"

	OCIImageSource      = "org.opencontainers.image.source"
	OCIImageTitle       = "org.opencontainers.image.title"
	OCIImageDescription = "org.opencontainers.image.description"
)
