# Perigee

Perigee maintains a pool of ephemeral GitHub Actions self-hosted runners on an
Apple Silicon Mac using Apple's `container` CLI. Each runner handles one job,
then its container is deleted and replaced with a fresh container.

Perigee is a Go reimplementation of Orbital for Apple container. Docker, Colima
and Lima are not required. The initial target is macOS 26 and container 1.5.0.

## Quick start

Prerequisites:

- Apple Silicon Mac running macOS 26.
- Apple's `container` CLI 1.5.0, with its service and recommended Linux kernel set up.
- Rosetta 2 installed for `linux/amd64` runners.
- Go 1.26.1 or later to build Perigee.
- A GitHub App installed on the target organization, with organization
  **Self-hosted runners: Read and write** permission and its RSA private key.

Initialize Apple container manually. Perigee never starts or stops its service:

```sh
container system start
# Accept the recommended kernel installation on first start.
# If the first start could not prompt, install it explicitly:
container system kernel set --recommended
container system status
```

Build Perigee and the example Android source image:

```sh
go build -o ./build/perigee ./app
container build --platform linux/amd64 -t perigee-android-source:latest ./example
cp ./example/config.yaml ./config.yaml
```

Edit `config.yaml` with your organization, App ID, installation ID, PEM path,
runner group and count, then start the pool:

```sh
./build/perigee up -c ./config.yaml
```

`Ctrl+C` or `SIGTERM` stops and deletes the pool, including runners executing a
job, and removes remaining owned registrations from GitHub. Failed cleanup is
reported and results in a nonzero exit status.

## Commands

| Command | Behavior |
| --- | --- |
| `perigee build -c config.yaml` | Build the runner image from `container.sourceImage`. |
| `perigee run -c config.yaml` | Maintain runners using an already-built `container.runnerImageName`. |
| `perigee up -c config.yaml` | Build the runner image, then maintain runners. |

All commands run locally through `container`; there is no Docker context or
remote runtime. `build` does not require GitHub credentials or runner settings.
`run` does not require `sourceImage`. `up` requires both sets of settings.

## Configuration

See [example/config.yaml](example/config.yaml) for a complete configuration.
Unknown keys, multiple YAML documents and invalid values are rejected.

| Setting | Meaning |
| --- | --- |
| `container.sourceImage` | Source OCI image containing the job's toolchain. Required by `build`/`up`. |
| `container.runnerImageName` | Tag of the image Perigee builds and runs. Required. |
| `container.platform` | `linux/amd64` (default) or `linux/arm64`; used for both build and run. |
| `container.runOptions` | Additional Apple container resource/network options as an argument array. |
| `github.org` | Target GitHub.com organization. |
| `github.appId`, `github.installationId` | Positive GitHub App and installation IDs. |
| `github.pem` | RSA private key in PKCS#1 or PKCS#8 PEM format; `~/` is supported. |
| `runner.group` | Optional existing organization runner group. |
| `runner.labels` | Optional custom GitHub labels; default GitHub labels remain enabled. |
| `runner.namePrefix` | Required name prefix, up to 40 characters. |
| `runner.count` | Number of containers to maintain, from 1 to 1000. |
| `mount.source`, `mount.target` | Optional host cache directory and absolute guest path. Supply both or neither. |
| `runtime.pollIntervalSeconds` | Poll interval, from 1 to 86400 seconds; default 10. |

Relative mount source paths resolve against the configuration file's directory.
The directory must already exist. Mounts are writable and shared by all runners;
configure job caches explicitly (for example `GRADLE_USER_HOME=/cache/gradle`).
Do not mount `/actions-runner`, its credentials, or `_work`: those must stay
ephemeral. Colons and newlines in mount paths are unsupported.

`runOptions` accepts separate arguments or `--option=value` for:
`--cpus`/`-c`, `--memory`/`-m`, `--network`, `--dns`, `--dns-domain`,
`--dns-option`, `--dns-search`, `--publish`/`-p`, `--shm-size`, `--ulimit`,
plus flags `--rosetta` and `--no-dns`. Other options are rejected so callers
cannot override Perigee's platform, names, labels, environment or entrypoint.
For example, use `['--cpus', '2', '--memory=4G']`.

## Runner image and lifecycle

Perigee layers GitHub Actions runner **2.338.0** onto the source image and verifies
the archive SHA-256 checksum. Runner automatic updates remain enabled. The source
must be a compatible glibc Linux image with `apt-get`, `dnf` or `yum`; the build
installs the runner's download and runtime dependencies. Alpine/musl, scratch and
distroless images are unsupported. Ubuntu 24.04 is the validated reference image.

The source image controls the job toolchain. Perigee resets the image's user to
root, working directory to `/actions-runner`, entrypoint to the runner bootstrap,
and default command to empty. The bootstrap registers an ephemeral runner, then
forwards stop signals to the listener and waits for it to exit. Registration
tokens are passed to the CLI through its environment, never command arguments,
and are cleared before starting the listener. App private keys remain on the host.

The pool identity is derived from organization, runner name prefix and runner
image tag. Containers use `perigee-<identity>-<slot>` names and
`io.perigee.owner`/`io.perigee.runner` labels. GitHub runner names are
`<prefix>-<slot>-<random suffix>` and carry `perigee-<identity>` as an ownership
label. Reserved ownership labels cannot be supplied through `runner.labels`.
No `--replace` registration is used.

A local file lock prevents two Perigee processes from managing the same pool,
even if their config files have different paths. Polling retains live runners,
deletes stopped containers and replenishes empty slots with fresh tokens.
Container listing failures and malformed JSON cause no destructive operations.
Unknown runtime states are left untouched. A failed slot does not prevent other
slots from being reconciled.

On restart, Perigee adopts containers with its ownership label and removes owned
GitHub registrations that have no live container. Reducing `runner.count`
removes excess managed containers, including active jobs. Shutdown attempts all
managed containers within a two-minute total deadline; runners whose containers
could not be stopped are retained in GitHub. A service outage or forced process
kill can leave assets for recovery on the next start.

Changing organization, prefix or runner image tag creates a different pool.
Stop the old pool before doing so. GitHub deletion always checks both the
ownership label and, for individual replacements, the exact runner name.

## Android jobs

[example/Dockerfile](example/Dockerfile) includes JDK 21, Android command-line
tools, platforms 35/36 and build tools 35.0.1/36.1.0. It is built using Apple
container and does not install Docker.

The Android example targets `linux/amd64` because the Linux Android SDK includes
x86 executables. Generic arm64 source images are supported, but an arm64 Android
toolchain needs separate verification and explicit Rosetta configuration.

To check an Android project on the pool:

```yaml
jobs:
  build:
    runs-on: [self-hosted, Linux, X64, android]
    steps:
      - uses: actions/checkout@v4
      - run: java -version && sdkmanager --version
      - run: ./gradlew assembleDebug --no-daemon
```

Only shell and JavaScript actions and ordinary toolchain jobs are supported.
Docker actions, `jobs.container` and `services` are outside this implementation.
There is no webhook autoscaling, macOS guest runner or Docker daemon.

## Migrating from Orbital

Replace the top-level `docker` block with `container`, remove `context`, and add
`platform: linux/amd64`. Keep `sourceImage` and `runnerImageName`, and translate
`runOptions` into the supported Apple container options. Keep the top-level
`github`, `runner`, `mount` and `runtime` blocks.

Orbital's outdated nested `build:`/`run:` sample is not accepted. Use the flat
Perigee example. Build or pull the source image into Apple's image store:
Orbital's Docker image store is separate. Remove Docker-related packages and
socket mounts from custom images and settings.

## Verification

```sh
go test ./...
go vet ./...
go build -o ./build/perigee ./app
```

The unit tests use fake CLI/HTTP interfaces and cover validation, ownership,
token handling, reconciliation, replacement, failures, crash recovery and
shutdown. An optional integration test builds local source and runner images
for both architectures, executes the actual runner binary, verifies the CLI
JSON schema, bind mount and bootstrap flags, and checks signal forwarding using
a fake listener. It deletes its uniquely named images and containers:

```sh
PERIGEE_CONTAINER_INTEGRATION=1 go test ./internal/container \
  -run TestAppleContainerIntegration -v -timeout 25m
```

The integration test does not register runners with GitHub. End-to-end GitHub
verification requires your App credentials: run a job, verify the runner exits
after that job, then verify a fresh runner appears by the next poll. Stop
Perigee and confirm its containers and owned GitHub registrations are removed.

References: [Apple container 1.5.0 CLI](https://github.com/apple/container/blob/1.5.0/docs/command-reference.md),
[multi-platform images](https://github.com/apple/container/blob/1.5.0/docs/multiplatform-images.md),
[runner release/checksums](https://github.com/actions/runner/releases/tag/v2.338.0),
[GitHub runner REST API](https://docs.github.com/en/rest/actions/self-hosted-runners).
