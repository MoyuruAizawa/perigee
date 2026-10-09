package container

// Version/checksums are from the official actions/runner v2.338.0 release.
const RunnerDockerfile = `FROM __SOURCE_IMAGE__
USER root
ARG RUNNER_TARGET_ARCH
RUN set -eu; \
    if command -v apt-get >/dev/null 2>&1; then \
      export DEBIAN_FRONTEND=noninteractive; \
      apt-get update; \
      apt-get install -y --no-install-recommends bash curl tar gzip ca-certificates libicu-dev libssl-dev zlib1g libkrb5-3 git; \
      rm -rf /var/lib/apt/lists/*; \
    elif command -v dnf >/dev/null 2>&1; then \
      dnf install -y bash curl tar gzip ca-certificates libicu openssl-libs zlib krb5-libs git; dnf clean all; \
    elif command -v yum >/dev/null 2>&1; then \
      yum install -y bash curl tar gzip ca-certificates libicu openssl-libs zlib krb5-libs git; yum clean all; \
    else \
      echo 'Source image must be a glibc Linux image with apt-get, dnf or yum (Alpine/musl is unsupported)' >&2; exit 1; \
    fi; \
    case "$RUNNER_TARGET_ARCH" in \
      amd64) arch=x64; sha=af4b794c1bc41d73d40535e3fe092a39f9679cd8d965954c2aca25a05ca41d32 ;; \
      arm64) arch=arm64; sha=628b4a7258487b80c1d3c221095a7ded349f5ae0175fd3dfab708d07428f041b ;; \
      *) echo 'Unsupported runner architecture' >&2; exit 1 ;; \
    esac; \
    mkdir -p /actions-runner; \
    curl -fL --retry 3 -o /tmp/runner.tar.gz "https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-linux-${arch}-2.338.0.tar.gz"; \
    echo "${sha}  /tmp/runner.tar.gz" | sha256sum -c -; \
    tar xzf /tmp/runner.tar.gz -C /actions-runner; rm /tmp/runner.tar.gz
ENV RUNNER_ALLOW_RUNASROOT=1
ENV RUNNER_WORKDIR=_work
WORKDIR /actions-runner
COPY entrypoint.sh /entrypoint.sh
RUN chmod 755 /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
CMD []
`

const RunnerEntrypoint = `#!/usr/bin/env bash
set -euo pipefail
set -m
cd /actions-runner
: "${GITHUB_URL:?GITHUB_URL is required}"
: "${RUNNER_NAME:?RUNNER_NAME is required}"
: "${RUNNER_TOKEN:?RUNNER_TOKEN is required}"
child=""
stopping=0
forward_signal() {
  stopping=1
  if [[ -n "$child" ]]; then kill -TERM -- "-$child" 2>/dev/null || true; fi
}
trap forward_signal TERM INT
args=(--url "$GITHUB_URL" --token "$RUNNER_TOKEN" --name "$RUNNER_NAME"
      --work "$RUNNER_WORKDIR" --ephemeral --unattended)
if [[ -n "${RUNNER_GROUP:-}" ]]; then args+=(--runnergroup "$RUNNER_GROUP"); fi
if [[ -n "${RUNNER_LABELS:-}" ]]; then args+=(--labels "$RUNNER_LABELS"); fi
./config.sh "${args[@]}" &
child=$!
if [[ "$stopping" == 1 ]]; then forward_signal; fi
set +e
wait "$child"
status=$?
if [[ "$stopping" == 1 ]]; then wait "$child"; exit 130; fi
if [[ "$status" != 0 ]]; then exit "$status"; fi
unset RUNNER_TOKEN
args=()
export RUNNER_MANUALLY_TRAP_SIG=1
./run.sh &
child=$!
if [[ "$stopping" == 1 ]]; then forward_signal; fi
wait "$child"
status=$?
if [[ "$stopping" == 1 ]]; then wait "$child"; status=$?; fi
exit "$status"
`
