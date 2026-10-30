# Installing Hermod

The container image, the Helm chart, packages, building from source, and `go get`.

The current release is **`v1.21.0`**. See [CHANGELOG.md](../CHANGELOG.md) for what
changed, what breaks, and the known gaps, and the
[releases page](https://github.com/gsoultan/hermod/releases) for every version.

**Pin an explicit version** for both the image and the chart. It is what makes a
deployment reproducible, and it is what the examples below do.

## Container image

```bash
docker pull ghcr.io/gsoultan/hermod:1.21.0
docker run --rm ghcr.io/gsoultan/hermod:1.21.0 -version
```

`linux/amd64` and `linux/arm64` are both published under that one tag.

## Helm

```bash
helm install hermod oci://ghcr.io/gsoultan/charts/hermod \
  --version 1.21.0 \
  --set existingSecret=hermod-master-key
```

The chart is versioned with the release. Production settings and the chart's values
are in [Deploying and running Hermod](deployment.md).

## Packages

`.deb`, `.rpm`, `.apk` and tarballs for Linux `amd64`/`arm64` are attached to each
[release](https://github.com/gsoultan/hermod/releases).

## From source

```bash
git clone https://github.com/gsoultan/hermod && cd hermod
go build ./cmd/hermod
```

Go 1.27 or later is required.

## `go get`

`go get github.com/gsoultan/hermod` and `go install github.com/gsoultan/hermod/cmd/hermod@latest`
resolve to the latest release.

Versions `v1.0.0` to `v1.8.0`, and `v1.0.0-rc.1`, are retracted in `go.mod`, so the Go
tooling never selects them. The module proxy is immutable and still serves them, and
`v1.0.0` in particular is bound to a commit from February whose `go.mod` declared
`github.com/user/hermod`, a path matching no repository. Nothing was lost by retracting
them: the module path was a placeholder until 2026-09-02, so none of those releases was
ever importable. Ask for a version above `v1.8.0`.
