# github-artifact-proxy [![build](https://github.com/alexbakker/github-artifact-proxy/actions/workflows/build.yaml/badge.svg)](https://github.com/alexbakker/github-artifact-proxy/actions/workflows/build.yaml?query=branch%3Amaster)

__github-artifact-proxy__ is a caching proxy for GitHub Actions build artifacts.
It exposes an HTTP service through which you can access the latest build
artifacts, based on configurable criteria (status, branch, etc.). The service
downloads the artifact, extracts the ZIP file and serves the requested file.

It only hits the GitHub API once to fetch the requested artifact. Any subsequent
requests for the same artifact are served from the cache.

Use cases:

- __README badges/images based on build artifacts__. Imagine you generate test
  coverage statistics in your GitHub Actions workflow and don't see the need to
  sign up for a service like Coveralls just to have a nice badge in your README.
  Instead, you generate the badge in your GitHub Actions workflow, upload it as
  an artifact and display the image in your README using a github-artifact-proxy
  link.
- __Pre-release builds__. Make the latest pre-release build produced by your
  GitHub Actions workflow easily accessible to your contributors and users.

This service currently works best for fairly small artifacts, because it first
has to download and extract them, before serving the files to the requester.

## Usage

The service takes a couple of command line arguments:

```
Usage of github-artifact-proxy:
  -config string
    	the filename of the configuration file (required)
  -download-dir string
    	the directory to download artifacts to (required)
  -http-addr string
    	the adddress the HTTP server should listen on (required)
  -http-base-path string
    	the base path prefixed to all URL paths (default "/")
```

### Configuration

The config file specifies a list of "targets" for which github-artifact-proxy
will accept requests and serve artifacts. Each target is accessible through:
``/targets/<target_name>/artifacts/<artifact_name>/<file_name>``.

```yaml
tokens:
  pat: ghp_your-access-token-here
targets:
  menta:
    # Required: A GitHub API token with at least the "public_repo" scope
    token: pat
    # Required: The username of the user who owns the repository
    owner: alexbakker
    # Required: The name of the repository
    repo: menta
    # Required: The workflow filename
    filename: build.yaml
    # Optional: The branch name
    branch: master
    # Optional: The event that kicked off the workflow run
    event: push
    # Optional: The status with which the workflow run finished
    status: success
```

With the configuration of the "menta" target above, one would be able to access
the "coverage.svg" file contained in the "coverage" artifact in the
alexbakker/menta repository with the following URL path:
``/targets/menta/artifacts/coverage/coverage.svg``.
