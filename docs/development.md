# Development

Build with Go 1.24.7 or newer:

```sh
go build -o rclone-proxy-tui .
go vet ./...
go test -race ./...
```

Run the end-to-end suite with a current rclone on PATH. It starts local
servers for all five protocols and checks plain and crypt shares, client
isolation, failed logins, configuration reloads, and upload draining during
revocation. Its temporary files are removed on exit; set TMPDIR to choose
where they live.

```sh
./scripts/e2e.sh
```

CI runs formatting checks, vet, unit tests and the end-to-end suite. Tags
matching `v*` build static Linux amd64 and arm64 binaries and publish them
with checksums in a GitHub release.
