# Image policy

Image policy rules can reject a stack before deployment when a service uses a disallowed image reference. Configure the optional policy under `sync.policy.image`:

```yaml
sync:
  policy:
    image:
      tag:
        required: true
        no_latest: true
        only_sha: true
```

- `required` requires an explicit tag or digest.
- `no_latest` rejects explicit and implicit `latest` tags.
- `only_sha` requires a digest reference.

The policy is disabled by default. If `image` is omitted, empty, or contains no enabled rule, swarm-deploy does not add the image policy step to the reconciliation pipeline.

A rejected service emits `deployDenied` and leaves the desired stack out of sync. No deployment is attempted, so the denial does not emit `deployFailed` or increment failed deployment execution metrics. A malformed image reference is reported as `image.invalid`, independently of the enabled tag rules.
