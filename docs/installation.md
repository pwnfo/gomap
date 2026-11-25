# Installation & Building

Gomap is built in 100% pure Go (`CGO_ENABLED=0`), producing a self-contained static binary with zero runtime dependencies.

## Prerequisites

- **Go 1.24+** (tested with Go 1.26) when building from source.

## Install via `go install`

```bash
go install github.com/pwnfo/gomap/cmd/gomap@latest
```

Ensure `$(go env GOPATH)/bin` is in your `PATH`:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
gomap -version
```

## Build from Source

```bash
git clone https://github.com/pwnfo/gomap.git
cd gomap
make build
```

This produces an optimized stripped binary at `./bin/gomap`. To install directly into your Go binary path:

```bash
make install
```

## Cross-Compilation

Because Gomap uses pure-Go compression libraries (`klauspost/compress`, `dsnet/compress`, `ulikunitz/xz`), cross-compiling to Linux, macOS, and Windows requires no C cross-toolchains:

```bash
make cross-build
```

Generated artifacts in `./dist/`:

- `gomap-linux-amd64`
- `gomap-linux-arm64`
- `gomap-darwin-amd64`
- `gomap-darwin-arm64`
- `gomap-windows-amd64.exe`

## Running Tests & Benchmarks

```bash
# Run unit and integration tests
make test

# Run with Go's race detector enabled
make race

# Run engine throughput & allocation benchmarks
make bench
```
