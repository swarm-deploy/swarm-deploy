package githosting

import "context"

type ReferencedProvider struct {
	provider  Provider
	reference RepositoryReference
}

func NewReferencedProvider(provider Provider, reference RepositoryReference) *ReferencedProvider {
	return &ReferencedProvider{
		provider:  provider,
		reference: reference,
	}
}

func (p *ReferencedProvider) GetLatestRelease(ctx context.Context) (*Release, error) {
	return p.provider.GetLatestRelease(ctx, p.reference)
}
