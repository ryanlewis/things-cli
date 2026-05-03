---
title: Install
icon: fas fa-download
order: 1
---

`things-cli` runs on macOS and ships as a single static Go binary. Three
ways to install — pick whichever fits your workflow.

## One-line install script

```sh
curl -fsSL https://raw.githubusercontent.com/{{ site.repository }}/main/install.sh | sh
```

Downloads the latest release for your architecture, verifies the
checksum against `checksums.txt`, and installs the `things` binary to
`/usr/local/bin`.

The script reads two optional environment variables:

```sh
# Install somewhere other than /usr/local/bin
INSTALL_DIR=$HOME/.local/bin curl -fsSL https://raw.githubusercontent.com/{{ site.repository }}/main/install.sh | sh

# Pin a specific version (defaults to the latest release)
VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/{{ site.repository }}/main/install.sh | sh
```

## go install

If you have a recent Go toolchain:

```sh
go install github.com/{{ site.repository }}/cmd/things@latest
```

The binary lands in `$GOBIN` (or `$GOPATH/bin`) as `things`.

## Prebuilt binary

Grab a `.tar.gz` from the
[latest release](https://github.com/{{ site.repository }}/releases/latest):

- `things_<version>_darwin_arm64.tar.gz` — Apple Silicon
- `things_<version>_darwin_amd64.tar.gz` — Intel

Extract, move `things` onto your `$PATH`, and you're done.

```sh
tar -xzf things_<version>_darwin_arm64.tar.gz
mv things /usr/local/bin/
things --version
```

## Verifying the install

```sh
things --version       # prints the build version
things                 # default view: today
```

The first invocation reads the Things3 database from
`~/Library/Group Containers/JLMPQHK86H.com.culturedcode.ThingsMac/ThingsData-*/Things Database.thingsdatabase/main.sqlite`
in read-only mode. Things3 must be installed and have been launched at
least once for the database to exist.
