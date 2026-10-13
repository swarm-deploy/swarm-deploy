# Image policy

Image policy rules reject a stack before deployment when a service uses a disallowed image reference.
They are optional and live under the synchronization policy:

```yaml
sync:
  policy:
    image:
      tag:
        required: true
        no_latest: true
        only_sha: false
```

- `required` requires an explicit tag or digest.
- `no_latest` rejects both explicit `:latest` and untagged references.
- `only_sha` requires a digest reference such as `image@sha256:...`.

All rules are disabled by default. When every rule is disabled, image references follow the existing deployment path unchanged.

A violation is reported as a structured Compose validation issue with the service, `image` field, policy code, and actionable message. The synchronization is rejected before a deployment attempt is created. The internal failure fact feeds Alert Management, so the first permanent violation opens the stack's `deploy_failed` alert. Repeated observations update that alert without sending another opening notification, and the next successful synchronization resolves it.
