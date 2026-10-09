# A Dockerfile is an OCI build recipe consumed by Apple container. No Docker is used.
FROM ubuntu:24.04
ARG ANDROID_CMDLINE_TOOLS_VERSION=14742923
ENV ANDROID_SDK_ROOT=/opt/android-sdk
ENV ANDROID_HOME=/opt/android-sdk
ENV JAVA_HOME=/opt/java
ENV DEBIAN_FRONTEND=noninteractive
ENV GRADLE_OPTS=-Dorg.gradle.vfs.watch=false
ENV PATH=/opt/java/bin:/opt/android-sdk/cmdline-tools/latest/bin:/opt/android-sdk/platform-tools:$PATH

RUN apt-get update \
    && apt-get install -y --no-install-recommends openjdk-21-jdk git curl unzip jq tar sudo ca-certificates \
    && ln -s "$(dirname "$(dirname "$(readlink -f "$(command -v javac)")")")" /opt/java \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p "${ANDROID_SDK_ROOT}/cmdline-tools" \
    && curl -fL --retry 3 -o /tmp/cmdline-tools.zip \
       "https://dl.google.com/android/repository/commandlinetools-linux-${ANDROID_CMDLINE_TOOLS_VERSION}_latest.zip" \
    && unzip -q /tmp/cmdline-tools.zip -d "${ANDROID_SDK_ROOT}/cmdline-tools" \
    && mv "${ANDROID_SDK_ROOT}/cmdline-tools/cmdline-tools" "${ANDROID_SDK_ROOT}/cmdline-tools/latest" \
    && rm /tmp/cmdline-tools.zip \
    && mkdir -p /root/.android \
    && touch /root/.android/repositories.cfg \
    && yes | sdkmanager --sdk_root="${ANDROID_SDK_ROOT}" --licenses >/dev/null \
    && sdkmanager --sdk_root="${ANDROID_SDK_ROOT}" \
       "platform-tools" "platforms;android-35" "platforms;android-36" \
       "build-tools;35.0.1" "build-tools;36.1.0"
