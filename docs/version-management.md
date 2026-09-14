# GCP HCP version management

Gecko performs OpenShift version validation while reconciling a
`Cluster`. `gcphcpctl` does not communicate with Cincinnati.

## Configuration

The `gecko-version-resolution` Helm chart requires an exact default version and
accepts a configurable minimum supported version:

```yaml
defaultVersion: "4.22.1"
minimumSupportedVersion: "4.22.0"
```

The controller refuses to start when the default is malformed or below the
minimum. Change `defaultVersion` in deployment configuration to promote a new
default; no Go code change is required.

## Cluster behavior

`spec.release.version` is mandatory. Gecko never replaces a missing or invalid
customer version with the configured default. After successful resolution, the
default and latest versions are recorded in private `status.versionResolution`
for future platform upgrade logic. Missing or unresolved versions clear this
status.

For the requested version, Gecko:

1. Validates the requested version against the configured minimum. Semantic-version
   syntax is enforced by the API schema before reconciliation.
2. Derives the Cincinnati channel from the version and `channelGroup`, such as
   `4.22.3` plus `stable` becoming `stable-4.22`.
3. Retrieves releases from Cincinnati and selects the exact requested version.
4. Calculates `latestVersion` as the greatest supported semantic version in
   that same channel.
5. Writes the result and the `VersionResolved` condition to cluster status.

`status.versionResolution`, including the resolved release image, is private to
Gecko controllers and is not exposed to customers or `gcphcpctl`. Public
validation results are reported through the `VersionResolved` condition.

`latestVersion` is channel-specific, not global. This avoids probing unknown
future Cincinnati channels. Versions at or above the configured minimum,
including future OpenShift 4.x and 5.x releases, are validated against their
derived channel without a Gecko code change.

## Conditions and failures

| Status | Reason | Meaning |
|---|---|---|
| `True` | `VersionResolved` | The exact version and release image were resolved. |
| `False` | `UnsupportedVersion` | The requested version is below the GCP HCP minimum. |
| `False` | `VersionNotFoundInCincinnati` | The exact version is absent from its channel. |
| `Unknown` | `CincinnatiDataInvalid` | Cincinnati returned no usable releases. |
| `Unknown` | `CincinnatiUnavailable` | Cincinnati returned an HTTP, decoding, or timeout error. |

This implementation does not persist or cache Cincinnati responses. Caching
and last-known-good fallback can be added separately if operational load or
availability requirements justify it.
